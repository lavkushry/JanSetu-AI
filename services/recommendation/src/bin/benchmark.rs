//! Representative transport workload; records measurements, never extrapolates DAU.
use jansetu_recommendation::pb::{
    recommendation_service_client::RecommendationServiceClient, Candidate, RecommendRequest,
};
use std::{
    sync::{
        atomic::{AtomicUsize, Ordering},
        Arc,
    },
    time::{Instant, SystemTime, UNIX_EPOCH},
};
use tokio::{sync::Mutex, task::JoinSet};
#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
    let args: Vec<_> = std::env::args().collect();
    let target = args
        .get(1)
        .cloned()
        .unwrap_or_else(|| "http://127.0.0.1:50051".into());
    let requests: usize = args.get(2).map(|v| v.parse()).transpose()?.unwrap_or(1000);
    let concurrency: usize = args.get(3).map(|v| v.parse()).transpose()?.unwrap_or(8);
    if requests == 0 || concurrency == 0 || concurrency > 512 {
        return Err("invalid benchmark size".into());
    }
    let candidates: Vec<_> = (0..2000)
        .map(|i| Candidate {
            id: format!("item-{i:05}"),
            revision: 1,
            author_id: format!("author-{}", i / 2),
            dedup_key: format!("body-{i}"),
            conversation_key: format!("root-{i}"),
            explicit_interest: f64::from(i % 3 == 0),
            locality: f64::from(i % 4 == 0),
            relationship: f64::from(i % 5 == 0),
            freshness: 1.0 / (1.0 + f64::from(i)),
            bounded_usefulness: f64::from(i % 20) / 20.0,
        })
        .collect();
    let client = RecommendationServiceClient::connect(target.clone()).await?;
    let counter = Arc::new(AtomicUsize::new(0));
    let errors = Arc::new(AtomicUsize::new(0));
    let durations = Arc::new(Mutex::new(Vec::<f64>::new()));
    let mut tasks = JoinSet::new();
    let start = Instant::now();
    for _ in 0..concurrency {
        let (mut client, candidates, counter, errors, durations) = (
            client.clone(),
            candidates.clone(),
            counter.clone(),
            errors.clone(),
            durations.clone(),
        );
        tasks.spawn(async move {
            while counter.fetch_add(1, Ordering::Relaxed) < requests {
                let now = SystemTime::now()
                    .duration_since(UNIX_EPOCH)
                    .unwrap_or_default()
                    .as_millis() as i64;
                let req = RecommendRequest {
                    viewer_context: "benchmark-synthetic".into(),
                    surface: "HOME".into(),
                    snapshot_id: "fixture-v1".into(),
                    deadline_unix_ms: now + 120,
                    limit: 200,
                    candidates: candidates.clone(),
                    ..Default::default()
                };
                let begin = Instant::now();
                let result = client.recommend(req).await;
                if result
                    .as_ref()
                    .map(|r| r.get_ref().items.len() != 200)
                    .unwrap_or(true)
                {
                    errors.fetch_add(1, Ordering::Relaxed);
                }
                durations
                    .lock()
                    .await
                    .push(begin.elapsed().as_secs_f64() * 1000.0);
            }
        });
    }
    while let Some(result) = tasks.join_next().await {
        result?;
    }
    let elapsed = start.elapsed().as_secs_f64();
    let mut latencies = durations.lock().await;
    latencies.sort_by(f64::total_cmp);
    let percentile = |p: usize| {
        latencies[((latencies.len() * p).div_ceil(100))
            .saturating_sub(1)
            .min(latencies.len() - 1)]
    };
    println!("{{\"fixture\":\"eligible-social-v1\",\"candidatesPerRequest\":2000,\"rankedReferences\":200,\"requests\":{requests},\"concurrency\":{concurrency},\"errors\":{},\"elapsedSeconds\":{elapsed:.3},\"requestsPerSecond\":{:.2},\"p50Ms\":{:.3},\"p95Ms\":{:.3},\"p99Ms\":{:.3}}}",errors.load(Ordering::Relaxed),requests as f64/elapsed,percentile(50),percentile(95),percentile(99));
    if errors.load(Ordering::Relaxed) > 0 {
        return Err("benchmark request errors".into());
    };
    Ok(())
}

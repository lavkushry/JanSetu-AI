//! Process-wide admission for CPU ranking. Cancellation of an RPC does not
//! release capacity while its detached blocking worker is still executing.
use std::sync::Arc;
use tokio::sync::Semaphore;
use tonic::Status;

use crate::{pb::RecommendRequest, pb::RecommendResponse, rank};

pub const DEFAULT_MAX_IN_FLIGHT: usize = 8;
pub const MAX_IN_FLIGHT: usize = 128;

#[derive(Clone)]
pub struct AdmittedRanker {
    slots: Arc<Semaphore>,
}

impl AdmittedRanker {
    pub fn new(limit: usize) -> Result<Self, &'static str> {
        if !(1..=MAX_IN_FLIGHT).contains(&limit) {
            return Err("recommendation max in flight must be 1..128");
        }
        Ok(Self {
            slots: Arc::new(Semaphore::new(limit)),
        })
    }

    pub async fn recommend(&self, request: RecommendRequest) -> Result<RecommendResponse, Status> {
        self.execute(move || rank(request)).await?
    }

    async fn execute<T, F>(&self, work: F) -> Result<T, Status>
    where
        T: Send + 'static,
        F: FnOnce() -> T + Send + 'static,
    {
        // try_acquire never adds callers to a waiting queue. All connections use
        // this same semaphore, and the worker owns its permit until it stops.
        let permit = self
            .slots
            .clone()
            .try_acquire_owned()
            .map_err(|_| Status::resource_exhausted("ranking capacity exhausted"))?;
        tokio::task::spawn_blocking(move || {
            let _permit = permit;
            work()
        })
        .await
        .map_err(|_| Status::internal("ranking worker failed"))
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::sync::mpsc;
    use std::time::Duration;
    use tokio::sync::oneshot;
    use tonic::Code;

    #[test]
    fn bounds_are_explicit() {
        for limit in [0, MAX_IN_FLIGHT + 1, usize::MAX] {
            assert!(AdmittedRanker::new(limit).is_err());
        }
        for limit in [1, DEFAULT_MAX_IN_FLIGHT, MAX_IN_FLIGHT] {
            assert!(AdmittedRanker::new(limit).is_ok());
        }
    }

    #[tokio::test(flavor = "multi_thread", worker_threads = 2)]
    async fn clones_share_capacity_without_waiting_and_recover_after_work() {
        let ranker = AdmittedRanker::new(2).unwrap();
        let mut releases = Vec::new();
        let mut jobs = Vec::new();
        for _ in 0..2 {
            let clone = ranker.clone();
            let (release, hold) = mpsc::channel();
            let (started, ready) = oneshot::channel();
            releases.push(release);
            jobs.push(tokio::spawn(async move {
                clone
                    .execute(move || {
                        started.send(()).unwrap();
                        hold.recv_timeout(Duration::from_secs(5)).unwrap();
                        42
                    })
                    .await
            }));
            tokio::time::timeout(Duration::from_secs(2), ready)
                .await
                .unwrap()
                .unwrap();
        }
        let rejected = tokio::time::timeout(
            Duration::from_millis(100),
            ranker
                .clone()
                .execute(|| panic!("overload must never start work")),
        )
        .await
        .expect("overload queued instead of rejecting")
        .unwrap_err();
        assert_eq!(rejected.code(), Code::ResourceExhausted);
        for release in releases {
            release.send(()).unwrap();
        }
        for job in jobs {
            assert_eq!(job.await.unwrap().unwrap(), 42);
        }
        assert_eq!(ranker.execute(|| 7).await.unwrap(), 7);
    }

    #[tokio::test(flavor = "multi_thread", worker_threads = 2)]
    async fn cancelled_rpc_keeps_capacity_until_worker_really_stops() {
        let ranker = AdmittedRanker::new(1).unwrap();
        let clone = ranker.clone();
        let (release, hold) = mpsc::channel();
        let (started, ready) = oneshot::channel();
        let rpc = tokio::spawn(async move {
            clone
                .execute(move || {
                    started.send(()).unwrap();
                    hold.recv_timeout(Duration::from_secs(5)).unwrap();
                })
                .await
        });
        tokio::time::timeout(Duration::from_secs(2), ready)
            .await
            .unwrap()
            .unwrap();
        rpc.abort();
        assert!(rpc.await.unwrap_err().is_cancelled());
        assert_eq!(
            ranker.execute(|| ()).await.unwrap_err().code(),
            Code::ResourceExhausted
        );
        release.send(()).unwrap();
        tokio::time::timeout(Duration::from_secs(2), async {
            while ranker.slots.available_permits() != 1 {
                tokio::task::yield_now().await;
            }
        })
        .await
        .expect("cancelled worker leaked its permit");
        assert_eq!(ranker.execute(|| 9).await.unwrap(), 9);
    }

    #[tokio::test]
    async fn panics_and_invalid_requests_release_capacity() {
        let ranker = AdmittedRanker::new(1).unwrap();
        assert_eq!(
            ranker
                .execute(|| panic!("synthetic worker failure"))
                .await
                .unwrap_err()
                .code(),
            Code::Internal
        );
        assert_eq!(
            ranker
                .recommend(RecommendRequest::default())
                .await
                .unwrap_err()
                .code(),
            Code::DeadlineExceeded
        );
        assert_eq!(ranker.execute(|| 11).await.unwrap(), 11);
    }
}

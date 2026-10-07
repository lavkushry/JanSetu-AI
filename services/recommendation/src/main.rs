use jansetu_recommendation::{
    pb::{
        recommendation_service_server::{RecommendationService, RecommendationServiceServer},
        RecommendRequest, RecommendResponse,
    },
    rank,
};
use tonic::{transport::Server, Request, Response, Status};
#[derive(Default)]
struct Service;
#[tonic::async_trait]
impl RecommendationService for Service {
    async fn recommend(
        &self,
        request: Request<RecommendRequest>,
    ) -> Result<Response<RecommendResponse>, Status> {
        let start = std::time::Instant::now();
        let result = rank(request.into_inner());
        eprintln!(
            "recommend elapsed_us={} success={}",
            start.elapsed().as_micros(),
            result.is_ok()
        );
        result.map(Response::new)
    }
}
#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
    let addr = std::env::var("JANSETU_RECOMMENDATION_ADDR")
        .unwrap_or_else(|_| "127.0.0.1:50051".into())
        .parse()?;
    Server::builder()
        .concurrency_limit_per_connection(128)
        .timeout(std::time::Duration::from_millis(120))
        .add_service(
            RecommendationServiceServer::new(Service)
                .max_decoding_message_size(1024 * 1024)
                .max_encoding_message_size(128 * 1024),
        )
        .serve_with_shutdown(addr, shutdown_signal())
        .await?;
    Ok(())
}

async fn shutdown_signal() {
    #[cfg(unix)]
    {
        let mut term = tokio::signal::unix::signal(tokio::signal::unix::SignalKind::terminate())
            .expect("install termination signal handler");
        tokio::select! {
            _ = tokio::signal::ctrl_c() => {},
            _ = term.recv() => {},
        }
    }
    #[cfg(not(unix))]
    {
        let _ = tokio::signal::ctrl_c().await;
    }
}

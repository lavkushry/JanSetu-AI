use jansetu_recommendation::{
    admission::{AdmittedRanker, DEFAULT_MAX_IN_FLIGHT},
    pb::{
        recommendation_service_server::{RecommendationService, RecommendationServiceServer},
        RecommendRequest, RecommendResponse,
    },
};
use tonic::{transport::Server, Request, Response, Status};
mod transport;
struct Service {
    ranker: AdmittedRanker,
}
#[tonic::async_trait]
impl RecommendationService for Service {
    async fn recommend(
        &self,
        request: Request<RecommendRequest>,
    ) -> Result<Response<RecommendResponse>, Status> {
        let start = std::time::Instant::now();
        let result = self.ranker.recommend(request.into_inner()).await;
        eprintln!(
            "recommend elapsed_us={} success={} status={:?}",
            start.elapsed().as_micros(),
            result.is_ok(),
            result.as_ref().err().map_or(tonic::Code::Ok, Status::code)
        );
        result.map(Response::new)
    }
}
#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
    let addr = std::env::var("JANSETU_RECOMMENDATION_ADDR")
        .unwrap_or_else(|_| "127.0.0.1:50051".into())
        .parse()?;
    let limit = match std::env::var("JANSETU_RECOMMENDATION_MAX_IN_FLIGHT") {
        Ok(value) => value.parse::<usize>()?,
        Err(std::env::VarError::NotPresent) => DEFAULT_MAX_IN_FLIGHT,
        Err(err) => return Err(err.into()),
    };
    let ranker = AdmittedRanker::new(limit)?;
    let mut server = Server::builder();
    if let Some(tls) = transport::from_env()? {
        server = server.tls_config(tls)?;
    }
    server
        .concurrency_limit_per_connection(128)
        .timeout(std::time::Duration::from_millis(120))
        .add_service(
            RecommendationServiceServer::new(Service { ranker })
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

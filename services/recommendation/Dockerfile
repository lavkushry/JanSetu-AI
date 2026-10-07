FROM rust:1.98-bookworm AS build
WORKDIR /src
COPY contracts/proto contracts/proto
COPY services/recommendation services/recommendation
RUN cargo build --locked --release --manifest-path services/recommendation/Cargo.toml --bin jansetu-recommendation
FROM debian:bookworm-slim
RUN groupadd --gid 10001 recommendation && useradd --uid 10001 --gid recommendation --no-create-home recommendation
COPY --from=build /src/services/recommendation/target/release/jansetu-recommendation /usr/local/bin/recommendation
USER 10001:10001
ENV JANSETU_RECOMMENDATION_ADDR=0.0.0.0:50051
EXPOSE 50051
ENTRYPOINT ["recommendation"]

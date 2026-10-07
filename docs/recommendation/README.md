# JanSetu recommendation delivery

JanSetu optimizes discovery for useful local conversations and creators, with satisfaction and user control as the objective. Rust serves recommendations, Go owns authentication and authorization, and Python evaluates experiments and will train models. This directory records the implementation and the remaining evidence gates for the supplied million-DAU plan.

## Runnable foundation

- [Algorithm PRD](PRD.md): behavior, controls, measurement and release gates.
- [Architecture](ARCHITECTURE.md): implemented service boundary and phased distributed design.
- [Events and privacy](EVENTS.md): current authenticated event API, generation fencing and retention.
- [Replayable event and feature foundation](STREAMS.md): dedicated outbox, Kafka delivery, Redis projections and isolated replay proof.
- [Redis snapshot cache](SNAPSHOTS.md): optional frozen pagination reads, commit boundaries, consent fencing and PostgreSQL fallback.
- [Shared rollback](ROLLOUT.md): restricted operator command, versioned switch, replica-wide ranking override and stale ranked cursor rejection.
- [First aggregate concurrency migration](CONCURRENCY.md): independent post count projection and the remaining lock-order audit.
- [References](REFERENCES.md): pinned repository revisions, source links and licensing decisions.
- [Validation record](VALIDATION.md): tests, browser/container proof and measured limits.
- [Benchmark protocol](BENCHMARKS.md): reproducible workloads and capacity/cost reporting.
- [Internal protobuf](../../contracts/proto/recommendation/v1/recommendation.proto): versioned Go/Rust contract.

Run `make recommendation-browser-proof` for the isolated end-to-end browser proof (local PostgreSQL and Chromium required). Use `make recommendation-generate` to regenerate Go protobuf code with pinned generators and protoc 3.21.12 (Ubuntu 24.04's `protobuf-compiler`).

Run `make recommendation` for the Rust service, `make recommendation-check` for Rust and Python checks, and `make test-integration` for isolated PostgreSQL checks. `JANSETU_RECOMMENDATION_TEST_TARGET=127.0.0.1:50051` also exercises the real Rust gRPC service in the integration suite. Compose includes the recommendation service on a dedicated internal network shared only with the API. Rust receives no database, media, operations or vault credentials.

Defaults are `JANSETU_RECOMMENDATION_MODE=shadow` and `JANSETU_RECOMMENDATION_ROLLOUT=0`. Select Recommended in the feed to use the snapshot/controls path; shadow mode observes Rust while returning chronological social order. For a local baseline demonstration, use mode `serve` and rollout `100`. Modes `off` and `shadow` return authorized chronological recommendations. Deployment configuration controls sticky rollout through 1%, 5%, 25% and 100%; changing it requires an API restart or replacement. The [shared rollback command](ROLLOUT.md) disables ranking without a restart and expires ranked cursors, including cached pages. Ordinary configuration/model changes preserve existing snapshot order until its five-minute expiry; emergency rollback requires a refresh into fallback.

## Delivery status and gates

| Stage | Implementation | Remaining gate |
| --- | --- | --- |
| 1. Specification and evaluation | PRD, privacy/event contracts, reference inventory, transport benchmark, viewer-cluster experiment evaluator | Real randomized satisfaction studies and representative complete-API load results |
| 2. Rust baseline | Tonic service, documented formula, up to 2,000 multi-source candidates, 200 ranked references, 20-item pages, batch hydration, explanations, consent controls, stable server snapshots, telemetry, shadow, fallback and audited shared rollback | Pilot evaluation before enabling serving traffic |
| 3. Distributed data | Dedicated transactional outbox, restricted worker role, Kafka publisher, Redis projections, optional snapshot cache, replay/deduplication, live consent fencing and independent post count projections | Redis serving features/parity, ClickHouse/S3 datasets, public retrieval backfill, load-tested aggregate locks for existing civic mutations |
| 4. Learned discovery | Evaluation contract and statistical promotion gate | Eligible real data, temporal training/holdout datasets, two-tower retrieval, multi-outcome ranker, Qdrant and tested ONNX adapter |
| 5. Session and exploration | Explicit More feedback influences future baseline interest; Less/Skip suppress posts immediately | Separate session/long-term feature stores, randomized 10% exploration with conditional selection probabilities, measured creator exposure; sequence models only after simpler baselines |
| 6. Video | Events reject video actions | Video publishing, transcoding, delivery and moderation before watch/replay features |

The later stages depend on real consented interaction data and operational release decisions. They are deliberately gated; synthetic fixtures establish correctness and reproducibility, not model quality. This implementation makes no million-user capacity or superiority claim. The existing app still limits deployment to local/test mode.

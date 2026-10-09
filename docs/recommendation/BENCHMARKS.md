# Recommendation benchmark protocol

## Repeatable baseline workload

Start `make recommendation`, then `make recommendation-benchmark`. The benchmark invokes the actual Tonic transport with 2,000 synthetic eligible social candidates/request and 200 ranked references, default 1,000 requests and concurrency eight. IDs, author distribution, explicit interest/locality/relationship features and usefulness are generated deterministically in [benchmark.rs](../../services/recommendation/src/bin/benchmark.rs). Record JSON output alongside runtime/hardware metadata. Errors fail the benchmark. Candidate generation/dataset size is published; this is not a PostgreSQL or complete-feed capacity test.

Run a release server for representative latency. Sweep concurrency 1, 8, 32, 128 with at least five-minute steady state and a separate warmup. Record successful RPS, all-response p50/p95/p99, error rate, CPU/memory, saturation point, retrieval deadline/fallback rate and model/policy versions. Test dependency delays and outages separately. Do not report successful-response latency while discarding timeout/error samples.

## Full API release workload

The [isolated complete-feed smoke runner](API-BENCHMARK.md) measures real loopback HTTP Go/PostgreSQL/Rust feeds with a published synthetic generator and structured artifact. `make recommendation-api-benchmark` creates fresh test databases and its own ranker. It validates authorization sentinels, author caps, consent-bound exposures and cursor ownership while recording all-response and RPC latency, errors and fallback modes. Viewer population is configurable from three to 1,024 independent accounts, with measured coverage recorded. This closed-loop fixture is a diagnostic starting point; the larger release workload below remains required.

An isolated environment must contain realistic active authors, follow/community graphs, language/locality distributions and post revisions: at least a million eligible posts, public civic receipts and current preference/consent generations. Include a long-tail creator distribution, 30% cold/nonconsenting viewers and 80/20 first-page/continuation requests. Include concurrent blocks, revocations and resets. Use restricted runtime database roles and preserve row-level authorization. Do not migrate or load-test an active user database.

One million DAU, twenty feed requests/day and a 20× peak multiplier imply 4,629.63 peak requests/s. Target 10,000 complete-feed requests/s for headroom, Rust p95 <150 ms and complete-feed p95 <500 ms. Verify authorization under load, not just latency. Publish dataset generator/seed and hardware/replica/database/storage configuration with each result. A local microbenchmark cannot prove these targets.

Report `costPerThousandFeeds = hourlyServingCost / successfulFeedsPerHour * 1000` and `costPerMillionEvents = hourlyEventCost / durablyProcessedEventsPerHour * 1000000`, including database/cache/vector/stream/analytics/storage allocations. Unknown cost is recorded as unavailable, never zero. Record network bytes and retained storage alongside latency. Evaluate cost and quality separately.

## Relevance evidence

Use real randomly sampled, consented session surveys. [The Python evaluator](../../services/training/evaluate.py) accepts one experiment, stable pseudonymous viewer assignment, mature seven/28-day retention, negative feedback, satisfaction in [0,1], language/locality/creator/user cohorts and allocation probabilities. It rejects unexpected fields, duplicate sessions, nonfinite values and changed viewer assignment. It records the dataset digest and deterministic viewer-cluster bootstrap seed and blocks an unmeasured cohort.

Use temporal holdouts and exposure-aware evaluations for learned models once eligible data exists. A deterministic baseline has no randomized exploration propensity; its logged outcomes cannot support unbiased counterfactual conclusions for unseen candidates. Synthetic evaluator tests check statistical plumbing only. Real study design must randomize survey invitation independently of engagement and record nonresponse rates. Human review, safety/repetition audits, statistical power analysis and feature parity remain promotion requirements after the statistical gate.

The checked-in [transport smoke result](benchmark-local.json) records the initial 1,000-request Rust transport test on a four-vCPU ARM host. The [complete-feed smoke results](API-BENCHMARK.md#local-smoke-results--2026-10-08) add isolated HTTP runs at concurrency one and eight, with all-response latency and correctness counts. Both workloads use synthetic data; representative full-API capacity and recommendation quality remain unvalidated. Costs are unavailable in this workspace.

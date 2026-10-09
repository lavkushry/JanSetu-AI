# Isolated complete-feed smoke workload

`make recommendation-api-benchmark` runs a synthetic loopback HTTP workload through the real Go feed handler, restricted PostgreSQL roles and a release Rust process. The integration harness creates and removes fresh application and vault databases. The script owns its temporary loopback ranker; it cannot send workload requests to the deployed pilot API. Docker's existing local PostgreSQL service and the installed Go/Rust toolchains are required.

```sh
make recommendation-api-benchmark BENCHMARK_REQUESTS=500 BENCHMARK_CONCURRENCY=8 BENCHMARK_VIEWERS=96 BENCHMARK_OUTPUT=/tmp/jansetu-api-result.json
python3 scripts/recommendation_api_benchmark.py --requests 500 --concurrency 1 --output /tmp/jansetu-api-single.json
```

The script builds with the locked Cargo dependencies, starts Rust on a temporary loopback port, and runs the Go fixture without the race detector for measurement. Its Go test timeout covers measured requests, viewer warmups and a cursor ownership check at the ten-second HTTP deadline, plus ten minutes for setup and cleanup. The artifact records that budget; accepted large workloads are not cut off by a fixed smoke timeout. Rust terminates after the workload exits. CI runs 50 requests at concurrency four with 12 viewers on a smaller fixture to verify the harness and feed invariants, without applying latency thresholds. Use `make test-integration` separately for race-detector regression coverage.

## Dataset and request mix

[The fixture generator](../../services/backend/internal/app/recommendation_benchmark_integration_test.go) uses deterministic UUIDs, bodies, language distribution, publication state, follow/block edges and relative publication ages. The default adds 128 authors with eight posts each to the versioned local seed. Every seventeenth post is hidden; each viewer blocks the last author and follows every eighth remaining author. The artifact distinguishes eligible fixture posts from excluded sentinels. Two thirds of generated revisions use en-IN, the remainder hi-IN. Chosen interests/language/locality are empty for this smoke fixture.

`--viewers` (or `BENCHMARK_VIEWERS`) configures 3–1,024 distinct test principals and profiles, default three. Random session secrets are hashed in the disposable database. The normal HTTP authentication path verifies each session's profile; the normal preferences API enables personalization for every viewer except indices congruent to two modulo three. These accounts have no platform or organization grants. This `api-smoke-v2` fixture includes viewer identities, consent policy, graph policy and the local seed digest in its dataset hash. `fixtureContentSHA256` separately hashes generated posts, so identical generated inventory can be identified across viewer populations. It replaces v1's three demo accounts, so archived v1 runs are not interchangeable baselines.

One excluded first-page warmup per viewer establishes the actual RPC path and initial continuation cursors, using up to the configured worker count. A separate excluded request verifies that a different viewer's cursor returns 410. Measurement uses a deterministic 70/30 consenting/nonconsenting mix, balances selection within each group independently of worker scheduling, and requests a continuation every fifth request. The artifact records the actual mix, since an exhausted cursor can require a first page. Every worker keeps viewer-bound cursor state. Consenting viewers receive exposures; nonconsenting viewers receive none. Snapshot expiry and live authorization remain enforced during this workload; an expired cursor is a reported HTTP error.

The harness reads each HTTP body before stopping its latency timer. Validation then checks nonempty 20-item bounds, fixture-hidden/blocked exclusion, unique posts, the two-per-author cap and consent-bound exposures. Any HTTP, transport, read or content validation error remains in the latency population and fails the command after writing its result. Successful ranked, shadow and fallback modes are counted separately; a fallback is a successful authorized feed with degraded ranking, not an invisible success of the Rust path. An initial fallback fails warmup rather than benchmarking an unavailable ranker accidentally.

## Result contract

The JSON artifact records all-response p50/p95/p99, separate first/continuation and consenting/nonconsenting latency, elapsed wall time, successful feeds/second, HTTP statuses, error categories, response bytes and feed modes. `seededViewers`, the consent group sizes, `measuredViewers` and `maximumRequestsPerViewer` distinguish configured population from actual measured coverage; a short run need not visit every seeded viewer. Warmups and the cursor ownership check are excluded from latency and throughput. The ranker wrapper records RPC calls, errors, maximum candidate count and RPC latency including transport/client validation. These RPC samples cover first-page ranking only; they are distinct from complete-feed measurements. CPU scheduling and instrumentation overhead are included.

`--ranker-max-in-flight` configures the Rust process-wide admission bound (default 8, valid 1–128). New artifacts record it as `rustMaxInFlight`; the archived local runs below predate admission control, as identified by their source and binary digests.

Dataset and generator hashes, source revision/dirty state, Rust binary digest, toolchain versions, PostgreSQL version, runtime role/pool limit, host architecture/logical CPUs/memory and replica/cache/TLS settings accompany the result. Source and binary hashes permit auditing a run from a dirty worktree. Results omit session cookies, database URLs, viewer/post IDs and response bodies. Serving/event costs stay `null` until actual allocations are available.

## Database pool measurements

New artifacts include `databasePools` for the isolated social, authentication, operations, publication, vault and vault-authentication pools. Counters are sampled after warmup, immediately before releasing workload workers, and again after all workers finish. Their differences exclude fixture construction and warmup: successful acquisitions and their cumulative duration, successful acquisitions that encountered an empty pool and their cumulative wait, canceled acquisitions and newly opened connections. No query text, arguments, connection URLs or viewer identifiers are recorded.

An empty-pool acquisition can wait for either a connection release or construction; it does not prove the pool reached its configured maximum. Acquisition durations exclude SQL execution and transaction lock waits after checkout, and successful-wait totals exclude canceled acquisitions. Totals aggregate concurrent waits and can exceed elapsed wall time. Start/end acquired and total connection gauges are snapshots, not peak utilization. These counters describe this isolated harness, including its in-process vault; they do not cover an external database server's internal waits. Use them alongside profiles and a controlled comparison before changing pool sizes.

## Optional Go profiling

Use a new output directory for each diagnostic run:

```sh
python3 scripts/recommendation_api_benchmark.py --requests 500 --concurrency 8 --profile-dir /tmp/feed-profile-001 --output /tmp/feed-profile-001.json
go tool pprof -top /tmp/feed-profile-001/app.test /tmp/feed-profile-001/cpu.pprof
go tool pprof -top /tmp/feed-profile-001/app.test /tmp/feed-profile-001/block.pprof
go tool pprof -top /tmp/feed-profile-001/app.test /tmp/feed-profile-001/mutex.pprof
```

The runner creates a private directory and refuses to reuse an existing directory. It retains the exact Go test binary and CPU, blocking and mutex profiles, with SHA-256 digests in `goProfiling.artifacts`. Successful profile runs require all four nonempty artifacts. Failed runs may leave partial diagnostic files; use a fresh directory when retrying. Ordinary runs record `goProfiling.enabled=false` and produce no profiles.

These profiles cover the Go test runner interval, including benchmark fixture setup, warmup, request generation, response validation and test cleanup. Database bootstrap and teardown surrounding `m.Run()` in `TestMain` are outside that interval. They are not limited to the timed HTTP interval and do not profile the PostgreSQL or Rust processes. Blocking samples use a 1,000,000 ns rate; mutex sampling records one in five contention events. Profiling adds overhead, so use a separate unprofiled run for latency comparisons. Blocking totals aggregate wait time across goroutines and can exceed wall time; network waits alone do not identify an expensive SQL query. Inspect caller stacks and use a focused follow-up experiment before changing serving behavior.

Profiles and the binary remain local and can contain source paths and runtime metadata. The runner does not expose an HTTP profiling endpoint or upload these files. Keep them outside the repository; share only reviewed aggregate findings.

## Interpretation and remaining capacity gate

This is a fixed-concurrency closed-loop smoke workload. It shares one host with local services, uses configurable synthetic viewers with identical graph/preferences and uniform creator sizes, and omits realistic communities/localities, diverse behavior, public civic inventory, concurrent revocations/resets, background event workers, cache dependencies, production TLS and the web BFF/proxy. API logs are discarded during timing; Rust logs go to a temporary local file. Repeat runs can vary with host load. Closed-loop latency does not correct for coordinated omission under a prescribed arrival rate.

Shared-viewer locking can constrain throughput differently from a deployment with many independent viewers. Compare viewer populations at the same source revision, inventory, concurrency and host configuration before attributing a difference to contention. A host process without explicit CPU/memory limits is not equivalent to the earlier constrained Rust-container result. The script reports these settings instead of extrapolating to a million users.

The [full release protocol](BENCHMARKS.md#full-api-release-workload) still requires realistic million-post inventory, long-tail graphs, independent viewers, steady-state arrival-rate sweeps, authorization changes under load, dependency failures, resource saturation and cost accounting on a published deployment configuration. `completeFeedCapacityValidated` and `qualityValidated` remain false. Use measured first-page/continuation and RPC differences to choose the next profiling experiment; they do not by themselves identify a database or lock bottleneck.

## Local smoke results — 2026-10-08

Both runs use the default 1,024 generated posts (956 eligible, 68 hidden/blocked sentinels), 500 measured requests, 100 continuations and 150 nonconsenting requests. All responses are valid ranked feeds; HTTP, content and RPC error counts are zero. The artifact records the common dataset/generator and binary hashes and clean source revision. The host has four logical ARM CPUs and PostgreSQL 18.6; the Rust release process and Go test process have no explicit resource limit. The local pilot is also running on that host.

| Run | Successful feeds/s | Complete API p95 | First page p95 | Continuation p95 | Recommendation RPC p95 |
| --- | --- | --- | --- | --- | --- |
| [Concurrency 1](benchmark-api-single-local.json) | 5.70 | 224.465 ms | 224.727 ms | 36.632 ms | 2.846 ms |
| [Concurrency 8](benchmark-api-eight-local.json) | 18.41 | 682.632 ms | 688.909 ms | 204.593 ms | 6.674 ms |

The concurrency-eight API p95 exceeds the 500 ms engineering target in this fixture. First-page work and shared-viewer/database contention need further profiling; the low RPC latency alone does not establish their cause. These short runs neither locate saturation nor validate the representative capacity gate.

The subsequent [viewer-retrieval experiment](RETRIEVAL.md) compares statement-local history/mute assembly against a fresh baseline. Its artifacts and per-path results are recorded separately from these earlier runs.


## Independent viewer comparison — 2026-10-09

Four sequential runs use the same clean source revision, fixture generator, release Rust binary, eight workers and 1,024 generated posts. Each measures 500 requests, 100 continuations and 150 nonconsenting requests. All configured viewers participate in measurement: the three-viewer runs send at most 175 requests to one viewer, compared with six for 96 viewers. All runs have zero HTTP, content and RPC errors. Each population has a distinct full dataset hash and the same generated-content hash. Warmup counts and associated snapshot/exposure state differ with population. The host has four logical ARM CPUs and PostgreSQL 18.6; local services share it with the unconstrained API and Rust processes.

| Population and run order | Successful feeds/s | Complete API p95 | First page p95 | Continuation p95 | RPC p95 | Ranked / fallback feeds |
| --- | --- | --- | --- | --- | --- | --- |
| [3 viewers, first](benchmark-api-viewers-three-local.json) | 21.11 | 646.147 ms | 661.172 ms | 455.349 ms | 36.573 ms | 499 / 1 |
| [96 viewers, first](benchmark-api-viewers-ninety-six-local.json) | 27.49 | 416.698 ms | 424.121 ms | 194.927 ms | 7.933 ms | 500 / 0 |
| [3 viewers, repeat](benchmark-api-viewers-three-repeat-local.json) | 26.17 | 438.657 ms | 444.644 ms | 247.801 ms | 6.472 ms | 500 / 0 |
| [96 viewers, repeat](benchmark-api-viewers-ninety-six-repeat-local.json) | 27.50 | 415.163 ms | 419.415 ms | 191.918 ms | 7.558 ms | 500 / 0 |

The first three-viewer run includes one authorized fallback and 399 RPC calls; the other runs have 500 ranked feeds and 400 RPC calls. With timing logs discarded, its exact fallback cause was not captured. The three-viewer p95 varies substantially between repetitions, while the two 96-viewer runs are close. These observations do not isolate the contribution of viewer locks or establish a statistically supported throughput improvement. This fixture allows that hypothesis to be investigated with independent accounts; representative arrival-rate, saturation and authorization-change tests remain required.

The subsequent [hydration metadata investigation](HYDRATION.md) uses this fixture and optional profiling to remove redundant JSON decoding. It records repeated latency observations separately from sampled CPU, without claiming an API latency or capacity improvement.

## Continuation progression

New runs record `paginationPolicy=advance-on-success-v2`. Each worker retains a separate cursor for each viewer and replaces it after every successful response, including continuation responses. An exhausted cursor clears that state, so the next scheduled continuation starts a new first page. A continuation response returning its input cursor fails with `cursor_not_advanced`. Failed requests preserve the previous cursor for a possible retry.

Earlier artifacts lack this field: their workers refreshed cursors only after first pages, so repeated continuation requests could replay the same page. Those results remain historical measurements of that workload and should not be compared as equivalent scrolling sessions. The actual continuation count remains recorded because exhausted cursors can change the nominal 80/20 request mix.

Runs with `crossPagePostValidation=true` also retain worker-local, viewer-specific post history for the current snapshot. Continuations must not repeat posts from the warmup/first page or earlier accepted continuations. A duplicate fails with `duplicate_across_pages` without advancing cursor or history; a new first page resets history. Post IDs are used only in memory for validation and are omitted from result artifacts. These checks add client-side validation work outside each HTTP latency timer, which is included in overall throughput timing.

## Optional query timings

Pass `--query-timings` to the Python runner to record `databaseQueryTimings`. The default is disabled, including when the environment contains a prior timing setting. This installs a test-only pgx tracer on a new social runtime pool with the same connection configuration and limits; the shared integration application and other pools retain their existing configuration. Recording starts after fixture setup, viewer warmups and cursor ownership validation, and ends when measured HTTP workers finish. CI enables it on the small smoke workload and checks query counts against actual first and continuation pages.

Fixed labels distinguish candidate retrieval, post hydration, principal/profile locking, preference reads, serving control, snapshot reads/writes and exposure writes. Unclassified statements use `other`. Each group records calls, pgx-reported failures, cumulative milliseconds and p50/p95/p99 milliseconds. SQL, query arguments, post/viewer identifiers, credentials and error messages are never retained by the tracer. The artifact records the tracer source hash for reproducibility.

These intervals run from pgx query start to Exec completion or Rows close. They exclude pool acquisition, include network transfer and application row scanning/iteration, and can include database lock waits. They are not PostgreSQL execution-only measurements or complete API phases. Concurrent queries overlap, so totals and percentiles cannot be added to derive feed latency. Only the social pool is traced; authentication and vault timings are outside this view. Tracing adds allocations, synchronization and memory proportional to query count, and uses a separate pool; compare ordinary throughput with tracing disabled. No production tracing endpoint or configuration is introduced.

[The local diagnostic smoke](benchmark-api-query-timings-local.json) records 50 requests, four workers, 12 independent viewers and 256 generated posts on the shared four-CPU ARM host with PostgreSQL 18.6. It returns 50 ranked feeds with zero errors, including 10 continuations. Counts match 40 candidate queries, 50 hydrations, 100 preference reads, 50 principal and profile lock queries, and 10 snapshot reads. Post hydration has p95 49.940 ms versus candidate retrieval 13.068 ms in this single small run; hydration is a useful next investigation, but these intervals include row consumption and are not evidence of an isolated SQL bottleneck or representative capacity.

The same small traced workload passed with the race detector. Separate 100-request/eight-worker/24-viewer runs on the default content fixture passed with timings both enabled and disabled. The enabled default-fixture run shared the host with the separate race check; those runs are correctness proofs and do not measure tracing overhead. The disabled run deliberately inherited `JANSETU_BENCHMARK_QUERY_TIMINGS=1`, confirming the runner explicitly disables tracing without the CLI flag. Privacy/interval and concurrent tracer unit tests, all Go unit tests and Go vet also pass.

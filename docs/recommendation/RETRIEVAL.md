# Viewer features assembled once per retrieval

The PostgreSQL candidate query previously correlated recent More feedback and active mutes with every eligible post. On the isolated 1,024-post fixture, an exploratory `EXPLAIN ANALYZE` showed these subplans executing 960 times. They repeatedly checked the same viewer scope even when the history and mute sets were empty.

[Retrieval](../../services/backend/internal/app/recommendation_feed.go) now materializes two viewer relations within the current SQL statement: distinct community/author pairs from current-generation More events in the last 30 days, and current active profile/community mutes. Eligible posts consult those relations. They are temporary query results, with no cross-request cache, new permissions or database configuration.

The restricted social role still enforces owner RLS on both relations. More events require enabled personalization, the requested generation, current publication and the exposed revision. Explicit community interests continue to work with personalization off. Mutes remain effective with personalization off and expire against the same statement timestamp. Retrieval order, source limits, ranking features and final hydration checks retain their existing behavior.

## Correctness proof

[The restricted-role integration test](../../services/backend/internal/app/recommendation_retrieval_integration_test.go) checks author and community interests, duplicate community signals, explicit preferences, old generations, events older than 30 days, non-More actions, mismatched revisions and revoked publication. It covers active profile/community mutes, expired mutes, another viewer's mute, anonymous scope, a forged viewer parameter and a live history reset. The complete PostgreSQL suite also exercises snapshots, permission revocation and event/consent controls under the race detector.

## Local paired smoke — 2026-10-08

Both runs use [the complete-feed runner](API-BENCHMARK.md), 500 measured requests, eight workers, 1,024 fixture posts, three viewers, 100 continuations and 150 nonconsenting requests. They share the dataset and fixture-generator digests. Each worktree builds the same Rust source independently; the artifacts record their separate binary digests. The host has four logical ARM CPUs and PostgreSQL 18.6, with the local pilot also running. API/Rust processes have no explicit resource limit. All 500 responses in each run are ranked feeds, with zero HTTP, content and RPC errors.

| Run | Feeds/s | API p95 | First page p95 | Continuation p95 | RPC p95 |
| --- | --- | --- | --- | --- | --- |
| [Existing query](benchmark-api-viewer-baseline-eight-local.json) | 18.56 | 688.620 ms | 699.172 ms | 200.864 ms | 5.954 ms |
| [Materialized viewer relations](benchmark-api-viewer-eight-local.json) | 26.57 | 467.770 ms | 485.735 ms | 250.048 ms | 8.987 ms |

The overall and first-page p95 improve in this short run. Continuation and RPC p95 increase; these requests do not perform candidate retrieval, and the shared-host, three-viewer workload cannot isolate the cause of that variation. Repeat runs and a broader viewer workload are needed to assess those paths. The result is diagnostic evidence for this query change, with no statistically supported recommendation-quality or representative capacity claim.

The eligible relation still spans matching public inventory before source limits. Indexed/distributed retrieval, realistic graphs and independent viewers, arrival-rate load tests, revocations under load and cost accounting remain part of the [capacity gate](BENCHMARKS.md#full-api-release-workload). The artifacts keep `completeFeedCapacityValidated` and `qualityValidated` false.

The subsequent [candidate hash budget](CANDIDATE-HASHING.md) defers deduplication hashing until after source selection. It preserves the features and source order above, while the eligible relation now carries published body values. Its larger-inventory observations and temporary-storage tradeoff are recorded separately; the inventory scan remains an open scaling prerequisite.

[Negative-feedback filtering](SUPPRESSION.md) adds a third statement-local viewer relation: distinct post IDs from retained current-generation Less/Skip events when personalization is enabled. These posts are excluded before source limits, rather than consuming slots and being removed only during final hydration. The measured comparison above predates this addition; it does not establish the updated query's latency or capacity.

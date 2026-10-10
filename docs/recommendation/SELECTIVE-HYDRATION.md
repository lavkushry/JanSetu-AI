# Select recommendation references before fetching bodies

The authorized feed query previously built public post JSON for every eligible remaining snapshot reference, up to 200, before the handler applied page selection. Only up to 20 social posts could reach the response, with fewer when civic updates occupied slots.

`RecommendationPosts` now has an explicit `IncludeData` parameter. Its default returns typed ID, published revision and nullable author ID with a null payload. The same statement retains all publication, community, block, mute, repost-source and restricted-role visibility predicates. A conditional expression builds JSON only when a caller requests it; there is no new permission implementation or schema migration.

The feed first reads eligible metadata for the remaining references and selects in frozen snapshot order, applying revision checks, Less/Skip suppression, the two-post author limit and the existing civic allocation. It requests bodies only for the selected references, at most 20. This second query rechecks the same live eligibility rules. The handler checks the returned revision against the snapshot and reapplies the author limit using the final query's author metadata before returning posts or creating exposures.

Feature-shadow comparisons still receive every eligible matching-revision reference from the first read. Their separate eligibility checks and non-READ feedback reads omit payloads. READ explicitly requests the published body for Unicode-aware duration normalization. Consent-generation checks, principal/profile lock order, serving rollback, batched exposure writes, durable child snapshots and cache-after-commit behavior remain in place.

## Concurrent changes and pagination

Stable inventory retains page selection, explanations, exposure identities and cursor progression. If publication or visibility changes between the metadata and body reads, the final read can drop selected posts. A changed published revision is also dropped rather than substituted into a frozen snapshot.

Such a page can be shorter even when later snapshot references remain eligible. Selected references stay consumed by its cursor; the handler does not make additional body queries to refill social slots. Remaining eligible civic receipts can still fill space under the existing policy. A subsequent page continues after the consumed offset, and every page rechecks live eligibility. This bounds body requests without asserting that every response always has 20 items. Changes after the final query retain the existing in-flight transaction semantics.

## Validation

Go vet, reproducible pinned SQL generation and the complete isolated PostgreSQL suite passed with the race detector, a real Rust target and the local image/pothole inference binaries enabled (130.159 seconds for the application package). The new tests verify that metadata omits JSON and that withdrawing or republishing a post after metadata selection excludes it from the response and creates no exposure for it. Existing tests cover consent withdrawal/reset, account deactivation, blocks/mutes, stale snapshots, duration normalization, feature parity and consenting cursor replay.

The first full-suite attempt overlapped another isolated integration suite and hit PostgreSQL's connection limit (`53300`) in a civic test. The serial full run above passed. No database connection limit or deployed configuration was changed.

## Diagnostic reference counts — 2026-10-10

The [baseline](benchmark-api-selective-hydration-before-local.json) and [selected-body run](benchmark-api-selective-hydration-after-local.json) each use 50 requests, four workers, 12 viewers, 256 generated posts, 10 continuations and 15 nonconsenting requests. Both return 50 ranked feeds with zero HTTP/content/RPC errors and the same aggregate response byte count. Dataset, generated-content and query-tracer hashes match; source trees are clean. Each worktree independently builds the unchanged Rust source, with its binary digest recorded.

| Measured work | All remaining bodies | Selected bodies |
| --- | --- | --- |
| Metadata-only queries | 0 | 50 |
| References sent to metadata queries | 0 | 9,680 |
| Body queries | 50 | 50 |
| References sent to body queries | 9,680 | 920 |
| Maximum references per body query | 200 | 20 |
| Candidate queries | 40 | 40 |
| Exposure commands | 35 | 35 |
| Preference reads | 100 | 100 |
| Principal/profile lock queries | 50 each | 50 each |
| Snapshot reads/writes | 10 / 50 | 10 / 50 |

These are requested-reference counts, not returned-row counts. The tracer keeps vector lengths and fixed labels, never identifiers or argument values. The metadata query still performs eligibility joins, and selected posts undergo a second permission read. Removing unnecessary JSON construction and transfer adds that extra query; these counts do not prove an isolated SQL-time or complete-API improvement. One traced run per version on the shared four-logical-CPU ARM host is diagnostic evidence, not a capacity benchmark.

The fixture-generator file hashes differ because the changed version adds assertions for metadata query counts and the 20-reference body budget; inventory and request generation are unchanged. The baseline is local commit `8e2144730b17e9348c437b0ea72f6ec54749a6f5`: main `4cd8b3d23e34a82b32e793526b4347faa910db20` with only the query tracer copied from implementation commit `abd5941df349444ea052a1a120ba33a2b673a121`. Reproduce that baseline by applying the implementation's `services/backend/internal/app/recommendation_benchmark_queries_test.go` to the specified main revision. Run each checkout sequentially:

```sh
python3 scripts/recommendation_api_benchmark.py --requests 50 --concurrency 4 --authors 64 --posts-per-author 4 --viewers 12 --query-timings --output /tmp/hydration-result.json
```

A separate [untraced default-fixture run](benchmark-api-selective-hydration-default-local.json) returned 100 ranked feeds with zero errors and 20 continuations, using eight workers, 24 viewers and 1,024 generated posts. It validates the normal uninstrumented path. Representative arrival-rate workloads, retrieval costs, operational costs and real recommendation-quality studies remain the [release gate](BENCHMARKS.md#full-api-release-workload). No rollout or pilot deployment was performed.

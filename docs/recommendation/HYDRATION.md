# Recommendation hydration metadata

The authorized batch query previously returned complete post JSON. Feed hydration decoded every returned body to recover its ID, author and published revision, then decoded chosen bodies again to check the revision. Feature parity decoded the same bodies for IDs/revisions; non-READ feedback also decoded a body it did not use.

`RecommendationPosts` now returns typed ID, published revision and nullable author ID beside the existing JSON payload. They come from the same statement and joins, with the existing publication, community, block, mute, repost-source and restricted-role visibility predicates. No schema migration or permission change is required. A null published revision maps to zero, matching the previous JSON-to-integer behavior; a missing author remains nullable.

The feed uses the typed columns for revision fences and the two-post author limit, retaining the raw payload for its public response. Feature parity uses typed revision metadata. Feedback still rechecks current eligibility and the served revision; READ alone decodes the published body for Unicode-aware duration normalization. Consent/history generation, snapshot expiry, live rollback checks and transactional exposure insertion retain their existing checks.

## Validation

Go vet, unit checks and the full isolated PostgreSQL suite passed with the race detector and a real Rust target (76.500 seconds for the application package). Existing regression cases cover stale/revoked revisions, consent withdrawal/reset, blocks/mutes, account deactivation, duration validation, duplicate feedback and feature parity. Regenerating SQL code leaves no diff.

## Unprofiled observations — 2026-10-09

Four sequential runs use the [isolated complete-feed harness](API-BENCHMARK.md), eight workers, 96 measured viewers, 1,024 generated posts, 500 requests, 100 continuations and 150 nonconsenting requests. Dataset and generator hashes match and source trees are clean. Both worktrees independently build the unchanged Rust source; artifacts record their binary digests. All runs have zero HTTP, content and RPC errors. The final existing-query run includes one authorized fallback and 399 RPC calls; the other runs have 500 ranked feeds and 400 calls.

| Query and run order | Successful feeds/s | Complete API p95 | First page p95 | Continuation p95 | Ranked / fallback feeds |
| --- | --- | --- | --- | --- | --- |
| [Existing, first](benchmark-api-hydration-before-local.json) | 27.63 | 405.404 ms | 414.246 ms | 195.640 ms | 500 / 0 |
| [Typed metadata, first](benchmark-api-hydration-after-local.json) | 25.22 | 472.896 ms | 482.659 ms | 246.507 ms | 500 / 0 |
| [Typed metadata, repeat](benchmark-api-hydration-after-repeat-local.json) | 26.21 | 436.616 ms | 442.108 ms | 206.773 ms | 500 / 0 |
| [Existing, repeat](benchmark-api-hydration-before-repeat-local.json) | 26.65 | 444.053 ms | 453.097 ms | 204.500 ms | 499 / 1 |

The typed runs do not show a consistent API latency improvement over the existing query. These short shared-host observations are not a controlled saturation experiment and do not isolate the cause of the variation. The change removes redundant decoding; it does not establish greater feed capacity.

## CPU profile observations

Separate 500-request profiles use the same fixture and request mix. Both return 500 ranked feeds with no errors. The JSON summaries record the source, binary and local profile artifact digests: [existing query](benchmark-api-hydration-profile-before-local.json), [typed metadata](benchmark-api-hydration-profile-after-local.json). Profiles and test binaries remain outside the repository.

| Sample | Existing query | Typed metadata |
| --- | --- | --- |
| Profile duration | 24.38 s | 24.82 s |
| Total Go CPU samples | 8.13 s | 6.92 s |
| Cumulative `recommendedFeed` samples | 5.26 s | 3.97 s |
| Cumulative hydration callback samples | 3.07 s | 1.91 s |
| `encoding/json.Unmarshal` beneath `recommendedFeed` | 1.53 s | No samples |

These totals include fixture setup and warmup in the Go test runner. Function values are cumulative sampled CPU, not request latency; the absence of samples is not an exact zero-cost measurement. One profile per implementation is diagnostic evidence of removed decoding, with no statistically supported CPU/capacity claim. PostgreSQL, Rust and other local services share the four-logical-CPU ARM host and are outside the Go profile.

The query still builds JSON for every eligible remaining snapshot reference, up to 200, and retrieval still scans matching inventory before its candidate ceiling. Those costs require separate investigation. Representative arrival-rate, authorization-change, resource and cost tests remain the [release gate](BENCHMARKS.md#full-api-release-workload); recommendation quality and million-user capacity remain unvalidated.

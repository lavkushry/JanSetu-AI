# Hash published bodies after candidate selection

The candidate query previously calculated a body deduplication key for every matching eligible post before applying source limits. Matching inventory can exceed the 2,000-candidate budget, so the budget did not bound hash evaluations.

The shared eligible relation now carries the approved published body. The existing source union still selects up to 500 followed candidates, 500 interest/locality candidates and 1,000 fresh candidates. A materialized selection applies the final 2,000-reference ceiling before the outer projection calculates `body:` plus MD5. Published revision, conversation keys, source membership, features and timestamp/ID ordering retain their previous behavior. Raw bodies stay inside the SQL statement; the Go/Rust candidate contract still receives deduplication keys and numeric features.

Publication, author/community state, language, repost-source, block/mute and restricted-role predicates are unchanged. Viewer history still uses the consent generation and 30-day fence. No migration, new permissions, model-policy change or rollout configuration is introduced. Final feed hydration continues to recheck live eligibility and the frozen revision.

This bounds hash work, not the whole retrieval query. The shared eligible relation still spans matching inventory and now carries body values instead of fixed-length hashes. Temporary storage can therefore increase, especially for larger inventory or longer inline bodies. These costs require representative memory/spill tests and indexed retrieval; this change does not establish a fully bounded inventory scan.

## Correctness and hash budget

The restricted-role test seeds 3,500 posts with disjoint fresh, followed and interest sources. A frozen copy of the previous query from main `6eacea21001284df33b6e25861d2b13459e6d675` is compared with the new query in one statement, sharing its timestamp and visibility snapshot. Ordered candidate identities and every feature match with no language filter, en-IN and hi-IN. The unfiltered case returns 2,000 candidates. Repeated published bodies retain their common key, while a different current draft does not enter that key.

A disposable invoker function wraps MD5 and increments a sequence to count evaluation in the two query shapes. It observes 3,504 calls in the baseline and 2,000 after selection, returning 2,000 candidates in both cases. This is an instrumented query-shape proof, not a production CPU profile or PostgreSQL internal function statistic. Its schema and fixture content events are removed afterward; grants apply only to the isolated test database. Existing viewer-history/mute tests also pass under the race detector.

Go vet, documentation and whitespace checks pass. The complete serial PostgreSQL Go race suite passes with a real Rust target and image/pothole inference binaries (154.366 seconds for the application package). It includes publication and permission revocation, consent withdrawal/reset, generation fencing, stable snapshots, exposure replay and stream proofs.

## Paired diagnostic workloads — 2026-10-10

Four sequential runs use 50 requests, four workers, 12 viewers and 4,096 generated posts spread across 1,024 authors. Each has 10 continuations, 15 nonconsenting requests, 40 ranking RPCs and a maximum of 1,354 candidates. Every run returns 50 ranked feeds with zero HTTP/content/RPC errors. Dataset, generated-content, fixture-generator and query-tracer hashes match; source trees are clean. Each worktree independently builds unchanged Rust source, with its binary digest recorded.

| Run order | Feeds/s | Complete API p95 | Candidate query p95 |
| --- | --- | --- | --- |
| [Baseline, first](benchmark-api-candidate-hashing-before-local.json) | 2.60 | 2,583.487 ms | 2,547.083 ms |
| [Selected hashes, first](benchmark-api-candidate-hashing-after-local.json) | 2.63 | 2,334.426 ms | 2,273.917 ms |
| [Selected hashes, repeat](benchmark-api-candidate-hashing-after-repeat-local.json) | 2.06 | 3,653.263 ms | 3,599.667 ms |
| [Baseline, repeat](benchmark-api-candidate-hashing-before-repeat-local.json) | 2.04 | 3,386.294 ms | 3,334.739 ms |

API and candidate-query p95 vary and do not show a consistent latency improvement. The runs are short, traced, closed-loop observations on a shared four-logical-CPU ARM host with PostgreSQL 18.6. Query intervals include row consumption and can overlap; they are not SQL execution-only measurements. Both versions exceed the 500 ms API engineering target on this fixture. The measured hash budget does not establish greater complete-feed capacity.

Reproduce the pair at baseline main above and implementation `082656db7db4bf09f35f0426feb414c703e52849`:

```sh
python3 scripts/recommendation_api_benchmark.py --requests 50 --concurrency 4 --authors 1024 --posts-per-author 4 --viewers 12 --query-timings --output /tmp/candidate-hashing-result.json
```

An initial baseline fixture with 64 authors and 64 posts each returned valid ranked pages without continuation cursors and was rejected by the harness's warmup requirement. It is not a measurement of scrolling under creator concentration. An additional untraced 100-request/eight-worker/default-inventory attempt returned a warmup fallback; the harness rejected it and wrote no timed artifact. Its exact fallback cause was not captured by the harness's discarded logs. Neither attempt is reported as a successful capacity result.

The [release gate](BENCHMARKS.md#full-api-release-workload) still requires representative inventory/graphs, arrival-rate loads, authorization changes, memory/spill/resource measurements and cost accounting. Real satisfaction studies remain separate. The deployed pilot is unchanged.

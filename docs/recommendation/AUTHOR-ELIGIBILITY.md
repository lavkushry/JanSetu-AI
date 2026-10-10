# Active authors in candidate retrieval

Candidate retrieval now checks that a post's author belongs to the active profile set, using an `IN` predicate wrapped in `IS TRUE`. The wrapper keeps membership inside the eligibility predicate instead of allowing PostgreSQL to flatten it into an inventory/profile join. Authorless civic updates remain excluded from social candidates; the separate labeled civic section retains its existing allocation and ordering.

This addresses a measured planner problem. An isolated PostgreSQL 18.6 `EXPLAIN (ANALYZE, BUFFERS)` on 4,096 generated posts, 1,024 authors and 12 viewers estimated 19 eligible rows but observed 3,855. The old author join compared those posts against 1,042 active profiles, removing 4,017,219 nonmatching join pairs. The membership form built a hashed active-profile subplan once and avoided that cross product. A sequential diagnostic observed SQL execution of 711.574 ms before and 148.023 ms after, including EXPLAIN instrumentation and planner-selected JIT work. This is a local query-plan observation, not a complete-feed capacity result.

Both queries use the same restricted application role and statement snapshot. The profile read policy and all publication, community, language, review, repost-source, block/mute, consent/history and feature predicates remain in force. Source limits, chronological tie ordering, the 2,000-candidate ceiling and the published-body hash budget are unchanged. No migration, permission grant, ranker policy or rollout setting changes.

## Correctness

The restricted-role author test compares every ordered candidate and feature against the frozen pre-optimization query in one statement. It checks active, suspended, deactivated and authorless posts; hidden posts and rejected revisions; private, frozen and restricted communities; blocks in both directions; explicit person/community follows and active membership; reposts of active and suspended sources; language/community filters; authenticated, foreign-session and anonymous scopes; and personalization on/off. Explicit graph relations retain their existing public read semantics. Reactivating authors makes their posts and eligible reposts available in the next statement.

The existing 3,500-post disjoint-source parity test continues to compare all features and candidate order, with and without language filters, and verifies the 2,000 hash evaluation ceiling. The final-hydration race test now suspends an author after metadata selection and before body hydration. Both posts from that author disappear, and neither receives an exposure.

Go vet, specification and whitespace checks pass. The complete isolated PostgreSQL suite passes with `go test -race -p=1 ./... -count=1 -timeout=8m`, a real Rust target and actual image/pothole inference binaries (146.054 seconds for the application package, 15.206 seconds for media). An earlier run overlapping Rust builds passed the application package but hit the pothole readiness deadline; the clean serial rerun passes with unchanged code.

## Paired complete-feed diagnostics — 2026-10-10

Four sequential traced runs use 50 requests, four workers, 12 viewers and 4,096 generated posts across 1,024 authors. Each returns 50 ranked feeds, with zero HTTP/content/RPC errors, 10 continuations, 15 nonconsenting requests and a maximum of 1,354 candidates. Dataset, generated-content, fixture-generator and query-tracer digests match. Source trees are clean, and independently built Rust binary digests are recorded.

| Run order | Feeds/s | Complete API p95 | Candidate query p95 |
| --- | --- | --- | --- |
| [Author join, first](benchmark-api-author-eligibility-before-local.json) | 2.55 | 2,426.338 ms | 2,379.747 ms |
| [Active membership, first](benchmark-api-author-eligibility-after-local.json) | 20.65 | 248.239 ms | 199.349 ms |
| [Active membership, repeat](benchmark-api-author-eligibility-after-repeat-local.json) | 14.06 | 455.420 ms | 328.758 ms |
| [Author join, repeat](benchmark-api-author-eligibility-before-repeat-local.json) | 2.61 | 2,327.243 ms | 2,268.408 ms |

Both membership runs improve API and candidate-query p95 relative to both baseline runs. These are short closed-loop loopback observations on a shared four-logical-CPU ARM host with PostgreSQL 18.6, without explicit API/Rust memory or CPU limits. The query tracer includes row consumption and excludes pool acquisition; overlapping intervals are not SQL execution-only timings. The improvement is local diagnostic evidence, without a representative throughput or statistical quality claim.

A separate [untraced run](benchmark-api-author-eligibility-untraced-local.json) uses 100 requests, four workers, 24 viewers and the same generated content. All 100 feeds are ranked with zero HTTP/content/RPC errors and 20 continuations; API p95 is 320.661 ms. Its dataset digest changes with the viewer population, while content and generator digests match the paired runs.

Reproduce the traced pair at baseline main `0c02a9a8f054dc95e6dd8a57ab8480c25e26524a` and implementation `0f5c9e0e2a308dc6f346939021231f8206b78103`:

```sh
python3 scripts/recommendation_api_benchmark.py --requests 50 --concurrency 4 --authors 1024 --posts-per-author 4 --viewers 12 --query-timings --output /tmp/author-eligibility-result.json
```

For the untraced path, use `--requests 100 --viewers 24` and omit `--query-timings`. All artifacts retain false capacity/quality validation flags and null cost fields.

## Operating limits

Active-profile membership is statement-local, with no cross-request cache. PostgreSQL chooses whether to hash that set; sufficiently large sets or different memory/statistics settings can produce another plan. The active profile scan, matching-post inventory scan and eligible-body temporary storage remain scaling prerequisites. Representative graph sizes, memory/spill diagnostics and arrival-rate complete-feed load tests are still required by the [capacity gate](BENCHMARKS.md#full-api-release-workload). Synthetic parity and latency observations do not validate recommendation quality or million-user capacity. The deployed pilot is unchanged.

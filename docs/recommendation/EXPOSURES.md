# Batched recommendation exposures

Feed hydration previously inserted each selected social exposure separately. A consenting page can contain up to 20 posts, so those inserts required up to 20 database commands inside its transaction.

The handler now collects exposure IDs, post IDs and published revisions only for selected, eligible posts, then uses one parameterized `INSERT ... SELECT ... FROM unnest(...)` for the page. Viewer, consent generation, model/policy versions and expiry remain shared page values. Parallel arrays are appended together, and the page limit bounds the batch to 20 rows. Anonymous, nonconsenting and civic-only pages do not issue this insert.

The write remains inside the existing principal/profile transaction, after the same consent-generation, serving-control, publication and visibility checks and before the durable child snapshot. Row policies, foreign keys and `ON CONFLICT DO NOTHING` remain active. A failed insert rolls back the page transaction. Responses and snapshot-cache writes occur only after commit. Exposure identities, author limits, civic allocation and snapshot ordering are unchanged; replaying a cursor preserves exposure IDs and avoids duplicate records.

The strengthened consent/snapshot integration test verifies every returned social exposure against its persisted viewer, post, revision, generation and model/policy values on first and continuation pages. It also verifies exact consenting-page replay and unchanged exposure row count on retry. Existing regression tests cover withdrawal/reset/deactivation, current publication eligibility, blocks/mutes, event identity and dependency failures. Full Go vet/unit checks and the complete isolated PostgreSQL race suite passed with a real Rust target (140.382 seconds for the application package, with local vision executables available).

## Diagnostic evidence

The [existing diagnostic](benchmark-api-query-timings-local.json) and [batch diagnostic](benchmark-api-exposure-batch-local.json) share fixture, generator and workload hashes: 50 requests, four workers, 12 viewers, 256 generated posts, 10 continuations and 15 nonconsenting requests. Both use the same query-timing tracer and unchanged Rust source, return 50 ranked feeds and record zero HTTP/content/RPC errors. Each worktree independently builds its release Rust binary; digests are recorded.

| Query group | Individual inserts | Batch insert |
| --- | --- | --- |
| Exposure writes | 644 | 35 |
| Candidate retrieval | 40 | 40 |
| Post hydration | 50 | 50 |
| Preference reads | 100 | 100 |
| Principal/profile lock queries | 50 each | 50 each |
| Snapshot reads/writes | 10 / 50 | 10 / 50 |

The 35 batch commands correspond to the 35 consenting requests. This proves fewer database commands for the same synthetic page workload; one traced run per implementation on a shared four-CPU ARM host does not establish a latency improvement or representative capacity. Query durations overlap and include row consumption as described in the [benchmark guide](API-BENCHMARK.md#optional-query-timings).

A separate [untraced default-fixture smoke](benchmark-api-exposure-batch-default-local.json) returned 100 ranked feeds, zero errors and 20 continuations with eight workers, 24 viewers and 1,024 generated posts. Larger hydration payload costs, representative arrival-rate loads, operational costs and real recommendation-quality studies remain open gates. Rollout defaults and the deployed pilot are unchanged.

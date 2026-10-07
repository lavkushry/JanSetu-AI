# Baseline validation — 2026-10-07

## Passed

- Full Go `go vet ./...` and PostgreSQL integration suite with `JANSETU_INTEGRATION=1 go test -race ./...`. The integration harness creates and removes isolated databases; it does not migrate the active pilot database.
- Targeted recommendation integration suite after final deduplication changes, with the actual release Rust container through `JANSETU_RECOMMENDATION_TEST_TARGET=127.0.0.1:50051` and the race detector.
- Consent off by default, required/versioned preferences, reset/withdrawal generations, stale and foreign cursors, stable pagination/model changes, author cap, civic allocation, guest cookie binding, replay, expiry/retention, live hide/block/mute checks, duplicate/conflicting events, served-revision validation, duration manipulation, logical account deactivation and restricted-role denial.
- Invalid ranker IDs/revisions, explanations, duplicate content and dependency timeouts cause fallback. Rust rejects expired deadlines, over-budget requests and nonfinite/out-of-range features.
- Rust formatting, Clippy with warnings denied and three ranker tests; Python's three evaluation tests.
- TypeScript typecheck and Next.js production build.
- [Browser proof](../../scripts/recommendation_browser_proof.py): real browser → BFF → Go → Rust → isolated PostgreSQL, including consent, explanations, More feedback, reset, stale cursor rejection and chronological Following. One Chromium test passed; browser page errors were absent.
- Go protobuf generation with pinned generators, SQL generation, OpenAPI type generation, Markdown spec validation, Compose configuration validation and whitespace check.
- Container SIGTERM shutdown after live RPC requests: clean exit code 0 within the three-second stop budget.
- Rust Docker image build; read-only nonroot container with capabilities dropped, two-CPU limit and 512 MiB memory limit exercised through the Go client.

## Stage-3 stream slice

- Full Go vet/unit checks and the PostgreSQL integration suite with the race detector passed after the stream migration and role changes.
- `make recommendation-stream-proof` passed against pinned Apache Kafka 4.2.2 and Redis 8.2.3, with isolated PostgreSQL databases. Its final proof includes transactional event/outbox rollback, identical event deduplication, publisher outage/retry, expired lease fencing, consent reset, hard-deletion tombstones, a real consumer group, Redis outage with uncommitted Kafka offsets, consumer restart/replay, late withdrawal events, loss/rebuild of Redis state, independent field retention and bigint ordering beyond the float64 integer range.
- The dedicated worker cannot read the mixed private/civic outbox, private tables, personal event ledger, raw published text or the stream outbox itself. Unknown/private envelope fields stop projection.
- The proof removes its Kafka/Redis containers/network on exit. PostgreSQL fixtures are removed by the integration harness. The active pilot database is unchanged.
- This is a correctness proof. No sustained event-throughput/cost benchmark, online feature serving, analytics export or replicated deployment has been measured. [Remaining stage-3 work](STREAMS.md#remaining-stage-3-work) stays explicit.

## Redis snapshots

- `make recommendation-stream-proof` passed with the real Redis snapshot adapter and race detector (15.305 seconds for the final stream/snapshot tests). Snapshot cache writes were checked from an independent database connection after commit.
- Identical pages/exposure IDs replay across API instances. A cached cursor continues after removing its durable row in the isolated fixture, proving the hit skips the snapshot read; the fixture then restores that row. Cache loss and read/write outages recover through PostgreSQL with stable cursors.
- Corrupt scope, schema, expiry, root/offset, explanation and duplicate exposure entries fall back safely. Authenticated/anonymous bindings, publication revocation, changed revisions, blocks, mutes and immediate Less feedback remain authoritative. Reset and withdrawal reject cursors while their old-generation entries still exist.
- The unresponsive Redis socket test passes with enlarged URL timeout/retry settings, confirming the adapter's bounded optional dependency. The cache is disabled by default; no pilot database or public service binding changed. [Snapshot contract](SNAPSHOTS.md) documents subsequent parity, deployment and load-measurement gates.
- Full isolated PostgreSQL Go regression tests passed with the race detector (66.635 seconds for the application package); `go vet ./...` and documentation validation passed.

## First aggregate concurrency migration

- Full Go vet and PostgreSQL integration suite with the race detector passed after moving vote/repost count projections onto their post aggregate lock.
- A real database gate holds the pilot ordering lock and pauses the first post projection while a second post's count projection and acknowledgement finish. Both vote and repost totals remain correct.
- The existing reply-notification/command deadlock regression and worker replay/compatibility tests passed. [Reviewed write set](CONCURRENCY.md) records the narrow allowlist and remaining civic/API migration.
- The exposure/reply regression reproduces a real PostgreSQL deadlock with the old profile lock. With `FOR NO KEY UPDATE`, both transactions complete and record exactly one served exposure and reply notification. Consent/reset/deactivation and owner-scope recommendation tests also pass.

## Measured limits

The [transport smoke result](benchmark-local.json) uses 2,000 synthetic candidates/request and 200 ranked references, 1,000 requests at concurrency eight. It measured p95 14.893 ms, p99 17.071 ms, 718.87 requests/second and zero errors on a four-vCPU ARM host with a two-CPU container limit. This is a short transport test, not a steady-state complete-feed benchmark. Hardware/build/budget metadata is in the JSON artifact. Serving/event costs are unavailable.

No real session-satisfaction study, learned-model promotion, complete-feed 10,000-RPS test or million-DAU capacity claim is included. [Delivery status](README.md#delivery-status-and-gates) records those dependencies. Deployment remains local/test, with the recommendation rollout defaulting to shadow/0.

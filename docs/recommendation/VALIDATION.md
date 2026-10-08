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

## Shared serving rollback

- The actual `recommendation-control` binary was built and executed against an isolated database. Get/disable/enable work with the restricted operator role; missing/stale expected versions fail. The API cannot change control, and the operator cannot read private/application/stream/control tables or connect to the vault.
- The race-detector integration proof verifies transition audit records, live behavior across two API instances, skipped ranker calls while disabled, permanent invalidation of old ranked cursors after re-enabling, stable chronological cursors, no-op versions, competing compare-and-set writers, control-read failure and a disable during ranking before final hydration.
- Full isolated PostgreSQL Go tests passed with the race detector (72.139 seconds for the application package). Real Redis/Kafka stream/snapshot proof passed (17.100 seconds), including rollback and re-enable while the old ranked entry remains cached. Go vet and documentation validation passed.
- The switch defaults to enabled authority over the existing shadow/0 deployment settings; it does not expand rollout. [Operator and serving contract](ROLLOUT.md) records in-flight behavior and the requirement to upgrade every replica before relying on fleet-wide rollback. Production transport/credential/deployment/load gates remain explicit.

## First aggregate concurrency migration

- Full Go vet and PostgreSQL integration suite with the race detector passed after moving vote/repost count projections onto their post aggregate lock.
- A real database gate holds the pilot ordering lock and pauses the first post projection while a second post's count projection and acknowledgement finish. Both vote and repost totals remain correct.
- The existing reply-notification/command deadlock regression and worker replay/compatibility tests passed. [Reviewed write set](CONCURRENCY.md) records the narrow allowlist and remaining civic/API migration.
- The exposure/reply regression reproduces a real PostgreSQL deadlock with the old profile lock. With `FOR NO KEY UPDATE`, both transactions complete and record exactly one served exposure and reply notification. Consent/reset/deactivation and owner-scope recommendation tests also pass.

## Authenticated Go/Rust transport

- `make recommendation-transport-proof` passed with the race detector (1.237 seconds). Temporary ECDSA identities and separate client/server CAs exercise the actual Rust listener and production Go client. Valid ranking succeeds before and after rejection of plaintext, missing/untrusted/expired/wrong-purpose client identities, untrusted/expired servers and wrong server names. TLS does not downgrade to a plaintext endpoint.
- Partial TLS configuration, malformed CA material, missing keys and mismatched certificate/key pairs fail startup. Rust local-only plaintext validation, formatting, Clippy with warnings denied, Rust tests and Python evaluation tests passed.
- Full Go vet and isolated PostgreSQL integration tests passed with the race detector (64.406 seconds for the application package). The optional TLS Compose overlay and Markdown/whitespace validation passed. Public bindings, pilot data and rollout settings are unchanged.
- [Transport operations](TRANSPORT.md) describe the dedicated client trust domain, secret mounts and restart-based rotation. Certificate provisioning, revocation/rotation drills, container deployment and sustained TLS throughput are not claimed by the process-level proof.

## Behavioral feature shadow — 2026-10-08

- `make recommendation-feature-proof` passed with the race detector (4.683 seconds for the application package), using a disposable pinned Redis on a random loopback port and isolated PostgreSQL databases. Six feedback types match the real ledger after projection. Missing, extra and changed observations produce separate counts; legacy dedup replay fills the new projection without extending expiry.
- The real adapters enforce revision isolation, absolute per-field retention, latest timestamp/UUID ordering, bigint generation precision, bounded references/state, malformed cache rejection, live publication/block/mute checks and owner RLS. Reset during the Redis read discards the sample; replay consults live authority and cannot restore pre-reset behavior. Withdrawal and anonymous feeds do not read behavioral features. Cache outage preserves frozen pagination and exposure IDs.
- Full isolated PostgreSQL Go tests passed with the race detector (69.024 seconds for the application package), along with Go vet and feature/stream/config unit tests. The existing real Kafka/Redis stream and snapshot proof passed (16.585 seconds). Proof containers and test databases were removed; the pilot remains unchanged.
- The reader is disabled by default and stays in shadow when enabled. [Feature contract](FEATURES.md) describes incomplete historical coverage, transient stream lag, dependency budgets and the remaining serving/production/model evaluation gates. No learned-quality or sustained capacity claim follows from this proof.
- The background-admission refinement passed the real Redis/PostgreSQL proof with the race detector (5.195 seconds): at most two jobs run per API instance, excess samples drop without waiting, canceled HTTP contexts retain owner scope, input references are copied and shutdown stops admission and drains accepted jobs.

## Initial public-content backfill

- Full Go vet and isolated PostgreSQL integration tests passed with the race detector (71.630 seconds for the application package). Coverage includes restricted-role denial, private/hidden/unapproved content exclusion, atomic rollback, concurrent callers, lock timeout without skipped rows, resumed checkpoints, completed-pass idempotency and the actual command without Kafka/Redis availability.
- After refining post locks to `FOR NO KEY UPDATE`, targeted race-detector backfill tests passed (7.505 seconds), including compatibility with foreign-key key-share locks while still rejecting conflicting edits.
- A new publication above the frozen scan bound produces its live event exactly once while the historical command finishes. Historical behavioral events are not copied.
- The real Kafka/Redis stream proof passed (18.967 seconds). One historical fixture has no live publication envelope and enters the projection through backfill; a later hide wins over replay of its older backfill envelope.
- Markdown and whitespace checks passed. The [backfill guide](BACKFILL.md) records the single-pass scope, cursor privacy, timeout/rate bounds, delivery distinction and future reconciliation/capacity gates. The active pilot database was not migrated or backfilled.

## Measured limits

The [isolated complete-feed workload](API-BENCHMARK.md) passed at concurrency one and eight, each with 500 requests, 100 continuations, 150 nonconsenting requests and no HTTP/content/RPC errors. The checked-in artifacts record complete API p95 of 224.465 ms and 682.632 ms, respectively, alongside first-page/continuation and RPC measurements, source/fixture/binary hashes and hardware/runtime configuration. The concurrency-eight result exceeds the 500 ms target for this three-viewer smoke fixture; profiling and representative load validation remain necessary. CI's smaller 50-request/four-worker harness also passed locally. The workload uses isolated databases and a temporary release Rust process; the pilot was not rebuilt or mutated.

The [transport smoke result](benchmark-local.json) uses 2,000 synthetic candidates/request and 200 ranked references, 1,000 requests at concurrency eight. It measured p95 14.893 ms, p99 17.071 ms, 718.87 requests/second and zero errors on a four-vCPU ARM host with a two-CPU container limit. This is a short transport test, not a steady-state complete-feed benchmark. Hardware/build/budget metadata is in the JSON artifact. Serving/event costs are unavailable.

No real session-satisfaction study, learned-model promotion, complete-feed 10,000-RPS test or million-DAU capacity claim is included. [Delivery status](README.md#delivery-status-and-gates) records those dependencies. Deployment remains local/test, with the recommendation rollout defaulting to shadow/0.

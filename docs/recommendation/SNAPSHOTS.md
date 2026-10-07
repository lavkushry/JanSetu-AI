# Optional Redis pagination cache

Recommended pages can load their frozen server-side snapshots from Redis through `JANSETU_RECOMMENDATION_SNAPSHOT_REDIS_URL`. The default is empty, preserving PostgreSQL-only pagination. PostgreSQL keeps the durable snapshot rows and remains the fallback for a cache miss, connection failure, timeout, corrupt entry or incompatible schema. This slice removes the snapshot lookup on a cache hit; it does not remove live database hydration or establish a capacity claim.

For an explicitly migrated development database, run the isolated infrastructure in [STREAMS.md](STREAMS.md), then start the API with a snapshot URL:

```sh
JANSETU_RECOMMENDATION_SNAPSHOT_REDIS_URL=redis://127.0.0.1:16379/0 make api
```

The pilot Compose does not start Redis or enable this option automatically. Use a separate snapshot credential/deployment from the feature consumer before production; the local proof intentionally uses one disposable Redis instance. Transport authentication, ACLs, replication and backup/deletion procedures remain production gates.

## Frozen references, live authority

Cache keys contain a SHA-256 digest of the viewer/browser-bound query, the consent/history generation and the opaque cursor UUID. Values contain version 1, the same binding digest/token/generation, absolute expiry, frozen public post references with approved revision numbers and safe explanation codes, opaque exposure IDs, sanitized civic receipt references, scan offsets and internal model/policy versions. They contain no post body, media URL, preferences, identity binding, private report, evidence or OCR. Payloads are limited to 64 KiB and at most 200 social and 200 civic references.

The cache decoder checks schema, scope, expiry and bounds. Go also checks the snapshot shape, reference IDs/revisions/explanation codes, scan offsets and the cursor derived from the frozen root/offsets. Invalid cached state falls back to the durable row. Replayed cursors preserve model ordering and exposure IDs across API replicas; ranking runs only when a new snapshot is created.

Every page reads current PostgreSQL preferences before consulting the cache, then rechecks the generation inside the principal/profile hydration transaction. Publication, approved revision, author/community state, blocks, mutes and current Less/Skip feedback are still checked against live PostgreSQL state. The cache cannot grant permission or substitute for current consent. Generation checks synchronously reject reset/withdrawal cursors even while an old cache entry remains; old references expire asynchronously within the original five-minute lifetime. Account/session checks remain authoritative.

## Commit, expiry and outages

New child snapshots and served exposures commit in PostgreSQL before any cache write. A miss is filled only after successful hydration and commit. Cache writes never hold database locks and their failure does not invalidate a durable cursor or fail the feed. The API closes its cache client at shutdown.

Each optional Redis operation has a 20 ms context and socket/pool/dial budget, with command retries disabled. URL timeout/retry parameters cannot increase those limits. A page can perform a load, a fill and a child write. Requests retain their overall API deadline. Cache logs record operation and hit/miss/invalid/error status without query bindings, viewer IDs, payloads or connection credentials.

Writes use atomic `SET NX` with the original absolute millisecond expiry. Replays and later fills cannot restart the five-minute lifetime or overwrite the original frozen state. Redis loss/restart recovers from PostgreSQL; cache outages retain the same opaque cursor, references and explanations. PostgreSQL outages still fail closed because permissions and consent require it.

## Verification and remaining work

`make recommendation-stream-proof` now also runs the real Redis snapshot proof with isolated PostgreSQL databases. It checks writes after durable commit, identical cross-replica replay, a hit that avoids the snapshot row read, cache loss/read-through, read/write outages, strict scope/schema/expiry/offset/reason validation, authenticated and anonymous binding, live publication/revision/block/mute/feedback checks, and reset with a stale cache entry still present. A socket test verifies prompt fallback when a Redis endpoint accepts a connection but never responds, including attempts to enlarge URL timeouts. The proof removes its Kafka/Redis containers and test databases on exit.

Online feature parity, session features, shared immediate rollback, ClickHouse/S3 datasets, retrieval backfill, remaining aggregate concurrency audits and representative full API throughput/latency/cost measurements are subsequent milestones. Snapshot caching makes no recommendation-quality or million-user capacity claim.

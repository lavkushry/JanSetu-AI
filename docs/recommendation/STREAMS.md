# Replayable event and feature foundation

This is the first stage-3 delivery slice. PostgreSQL now writes a separate allowlisted recommendation outbox in the same transaction as consent changes, accepted feedback and post publication changes. A Go publisher delivers it to Kafka; an independent consumer projects bounded features to Redis. The serving baseline still uses PostgreSQL snapshots and features, so a broker/cache outage adds no feed dependency. Redis projections are preparation for a measured serving integration.

## Run and verify

`make recommendation-stream-proof` starts pinned Kafka 4.2.2 and Redis 8.2.3 containers, runs isolated PostgreSQL integration tests with the real broker/cache, and removes its containers/network/anonymous volumes on exit. It requires the existing local PostgreSQL service. The proof ports bind only to loopback, 19092 and 16379. Its Compose project has no connection to application, media or vault networks. The local pilot's data is not migrated by this command.

For development against an explicitly migrated local application database, start `docker compose -f infra/recommendation/compose.proof.yaml up -d --wait`, then run these in separate terminals from `services/backend`:

```sh
go run ./cmd/recommendation-stream -mode publish
go run ./cmd/recommendation-stream -mode project
```

Configure `JANSETU_RECOMMENDATION_STREAM_DATABASE_URL`, `JANSETU_RECOMMENDATION_KAFKA_BROKERS`, `JANSETU_RECOMMENDATION_REDIS_URL` and `JANSETU_RECOMMENDATION_CONSUMER_GROUP` when endpoints differ. The database URL must authenticate as `js_recommendation_stream`; `RuntimePool` rejects owners and elevated roles. Local/test mode remains mandatory. The Docker backend build includes the command, but the pilot Compose does not start publishers automatically.

## Contract and privacy

The topic is `jansetu.recommendation.v1`, with three partitions in the proof and a seven-day broker retention configuration. Records contain schema version 1, a UUID event ID, event type, occurrence time and a monotonic bigint entity version. The strict decoder rejects unknown fields, unsupported versions/actions, malformed references and trailing payloads.

| Record | Allowlisted fields | Kafka key |
| --- | --- | --- |
| CONTROL | Random stream subject, generation, enabled/deleted flags | `viewer:<subject>` |
| INTERACTION | Random subject, generation, exposure/public post/revision, action, normalized reading observation, model/policy versions | `viewer:<subject>` |
| CONTENT | Public post reference, published revision or revocation, eligibility hint | `post:<post-id>` |

Stream subjects are independent random UUIDs. Their profile mapping stays in PostgreSQL and is inaccessible to the worker. Hard deletion removes the mapping and retains only a pseudonymous disabled tombstone. No raw post text, reports, case evidence, OCR, media URLs, identity bindings, interests or fine location are exported. The mixed `infra.outbox` is inaccessible to this worker. The worker can execute only fixed lease/ack/retry, current-consent and cleanup functions; it cannot select even the recommendation outbox directly.

Publication triggers run after the transaction's revision inserts. Content records are references, not a permission cache. Profile/community changes, blocks and source-conversation visibility still require the authoritative Go eligibility check; consumers must not expose content based on the stream hint. Initial historical content/retrieval backfill remains a later delivery step. Existing preference controls bootstrap at migration; historical behavioral events are not backfilled.

## Delivery and replay

Claims use `FOR UPDATE SKIP LOCKED`, a 30-second random lease and one outstanding record per aggregate key. Other publishers can process independent keys. Kafka publication uses all-ISR acknowledgements and the client's default idempotent producer. Only a successful publish can mark the matching live lease delivered. Ambiguous acknowledgement or process failure replays the same event identity. Retry delay grows to 60 seconds; a failure is never silently dropped. A malformed record stops the consumer without logging its payload or committing offsets.

The consumer blocks rebalance during bounded batches of at most 20 records and five seconds, disables auto-commit, applies Redis changes, then commits Kafka offsets. A failed batch exits without acknowledging it. Restarting the same consumer group replays that batch. Redis scripts apply atomically, use common cluster hash tags and compare bigint versions as decimal strings to avoid floating-point rounding.

Every personal record consults current PostgreSQL authority before projection. Reset, preference change, withdrawal and deactivation advance the generation and delete undelivered behavior; hard deletion emits a newer disabled control. Redis clears prior-generation fields, ignores stale generations, and deduplicates event IDs. Even a complete Redis rebuild cannot resurrect old behavior because replay consults live authority. In-flight pre-withdrawal Kafka records may remain in the transport log, but cannot create eligible personal features. Future serving integration must check current generation before using cached features; asynchronous cleanup alone is insufficient.

## Retention and bounds

Personal Redis fields and dedup IDs expire independently at their original event's 30-day cutoff. Later unrelated activity cannot extend an old field. READ keeps the most recent normalized observation, not a lifetime sum or maximum. A generation stores at most 10,000 dedup IDs; excess actions are intentionally excluded from this shadow projection until earlier fields expire. Control/content fences expire after 90 days; current PostgreSQL authority protects personal replay beyond that time. Deletion authority tombstones remain indefinitely until a reviewed retention policy safely covers all backups and replay sources.

Delivered outbox records expire after seven days; all interaction outbox rows expire after 30 days even when no Kafka publisher is running. The existing projection worker calls the fixed cleanup each minute. Kafka's seven-day transport retention starts from broker record time and includes segment/cleanup grace; a delayed publish may retain an envelope beyond the original ledger's cutoff. Redis refuses to use expired behavior. This local transport retention is separate from the 30-day usable feature policy; production must validate broker retention, encryption, backups and deletion acknowledgements before behavioral export outside the pilot.

## Remaining stage-3 work

Redis serving adapters/snapshots, online feature parity, session/long-term separation, ClickHouse and S3 exports with deletion manifests, public content backfill, operational backpressure/metrics, authenticated transport, replicated broker/cache deployment, complete API load/cost measurements and the civic aggregate-lock audit remain outstanding. This slice establishes replay correctness; it makes no million-user capacity claim.

The implementation follows the [franz-go producer and consumer contract](https://github.com/twmb/franz-go/blob/v1.22.1/docs/producing-and-consuming.md), [Redis atomic scripting contract](https://redis.io/docs/latest/develop/programmability/eval-intro/) and [hash-field expiry](https://redis.io/docs/latest/commands/hpexpire/). The proof uses the [official Kafka image](https://kafka.apache.org/42/getting-started/docker/). Module versions and image digests are pinned in the repository.

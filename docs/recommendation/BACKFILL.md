# Initial public-content backfill

Live recommendation content events begin with migration 32. Posts published before that migration otherwise have no initial reference in the stream. Migration 34 adds a bounded, resumable pass that enqueues each currently eligible historical post's published revision into the existing dedicated outbox. The publisher and consumer remain responsible for Kafka delivery and Redis projection. This prepares reference discovery for future retrieval indexes; it does not build embeddings or enable personalized serving.

## Run and resume

After applying migrations and the normal role grants to the intended local/test database, run from `services/backend`:

```sh
go run ./cmd/recommendation-stream -mode backfill -batch-size 50
```

Use `JANSETU_RECOMMENDATION_STREAM_DATABASE_URL` with the restricted `js_recommendation_stream` login. Batch sizes are 1–100, default 50. Each call has a five-second context deadline, database lock waits are capped at 250 ms, and the command pauses 100 ms between committed batches. It logs cumulative scanned/enqueued counts and completion only. No post IDs, private cursors or content appear in its output.

Backfill needs only PostgreSQL. Run the existing `-mode publish` and `-mode project` processes to deliver queued records; their endpoints and outage/replay contract are documented in [Streams](STREAMS.md). Enqueued counts do not mean delivered or indexed counts. Plan database/outbox capacity for the historical pass and monitor publisher lag; the local command does not implement fleet-wide resource admission or automatic backlog control.

Stop with SIGINT/SIGTERM. On contention, database failure or timeout, the command exits; rerun the same command to continue from its last committed checkpoint. If a commit succeeded but its acknowledgement was lost, the next call advances from that committed checkpoint. Completed passes are no-ops. This is one initial pass per database, with no worker-accessible reset operation. A future full index rebuild after stream retention needs its own reviewed reconciliation design.

## Transaction and ordering

The first successful batch freezes the maximum post UUID as its upper scan bound. A single internal checkpoint serializes callers, and subsequent pages scan primary-key order through that bound. New insertions are covered by live publication triggers, including IDs behind the current cursor or above the upper bound. The pass therefore terminates even while publication continues.

Every batch locks the selected post rows, rechecks current eligibility, inserts new CONTENT envelopes and advances the checkpoint in one transaction. It does not skip locked rows: doing so could permanently omit historical content. An error rolls back both the checkpoint and that batch's envelopes. Locking posts serializes their backfill sequence numbers with concurrent post edits, hiding or deletion. Later live changes receive later outbox sequence numbers; the existing Redis decimal-version fence ignores late replay of older historical references.

This is not a repeatable-read snapshot of the whole database. Eligibility is evaluated as each row is processed. Source posts, profiles and communities can change afterward; CONTENT remains a hint rather than permission authority. Consumers and future indexes must never expose a post solely because its hint says eligible. Go must still check live revision, visibility, consent, blocks and mutes at serving time.

## Data boundary

The pass emits only CONTENT envelopes: random event ID, post ID, approved published revision, eligibility flag, observation time and the outbox's sequence-based entity version. Eligible posts require an active author and a public/restricted active community, where present. Quotes also require an approved, published and publicly visible immediate source. Drafts, hidden/deleted posts, rejected revisions, inactive authors and private/inactive communities are excluded. Authorless civic receipt posts do not enter this social-post pass.

Historical reading/feedback events, preferences, identity bindings, raw text, media, reports, private evidence and OCR are not exported. Scanned counts include ineligible rows, but their IDs and scan cursor remain in the inaccessible checkpoint table. The worker gets execute permission on one fixed security-definer function with a fixed search path; it cannot read post tables, mutate the checkpoint, choose cursors or insert arbitrary outbox payloads. The application login cannot start a backfill.

## Validation

The isolated PostgreSQL tests exercise excluded content, SQL/Go batch validation, role restrictions, transaction rollback, concurrent callers, restart, lock contention, completed-pass idempotency and a real backfill command with unavailable Kafka/Redis endpoints. A live publication above the initial upper bound is delivered by its trigger without being added to the historical pass.

`make recommendation-stream-proof` also removes one fixture's live publication envelope and uses the backfill to introduce it through actual Kafka and Redis. A later hide reaches the projection, then replay of the historical envelope fails to undo that hide. Proof databases and broker/cache containers are isolated from the active pilot. No production backfill, retrieval completeness benchmark or million-user capacity claim is included.

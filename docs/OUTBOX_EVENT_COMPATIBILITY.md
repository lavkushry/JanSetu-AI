# Durable outbox event compatibility

The core worker validates each unprocessed event before updating post counts or delivering Activity. Unfamiliar event types and payload versions remain failed deliveries instead of being silently acknowledged. Projection, deduplication and acknowledgement still commit together. This uses existing outbox columns and the restricted worker role; no migration, API change or additional grant is required.

## Supported contracts

Current contracts require payload version **1**, a nonzero aggregate UUID, a positive aggregate version and a JSON object payload:

| Aggregate           | Event types                                                                                                                                                  | Projection                                                      |
| ------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------ | --------------------------------------------------------------- |
| POST                | PostReviewRequested, PostVoteChanged, PostRepostChanged, HelpfulResponseChanged, PublicationReviewed, ContentRevoked, CommentReviewRequested, CommentRevoked | Rebuild current counts; no additional Activity                  |
| POST                | CommentPublished                                                                                                                                             | Rebuild counts; eligible approved-reply Activity                |
| REPORT              | ReportReceived                                                                                                                                               | Intentionally no public projection                              |
| CASE                | CaseCreated, ObligationChanged, VerificationRecorded                                                                                                         | Intentionally no public projection; publication requires review |
| CASE                | SafeReceiptPublished                                                                                                                                         | Eligible followed-receipt progress                              |
| MODERATION_DECISION | ModerationDecisionRecorded                                                                                                                                   | Eligible private author review Activity                         |
| APPEAL              | AppealOutcomeRecorded                                                                                                                                        | Eligible private appeal outcome Activity                        |
| CONTENT_REPORT      | ContentReportOutcomeRecorded                                                                                                                                 | Eligible private reporter outcome Activity                      |

Reply events also require a valid comment UUID and positive revision; receipt events require a valid receipt UUID. Existing projections enforce eligibility, source/version, consent, blocks/mutes and ownership. A supported event with no eligible recipient is intentionally acknowledged; recovery must not replay those consent or visibility skips.

For a new producer event, add its supported envelope and handler (or document its intentional lack of projection), test producer and worker together, and upgrade workers before enabling emission. A new payload version needs a matching consumer. Extra object fields remain accepted within version 1; future versions are not treated as the old schema.

## Retry and diagnostics

Failures roll back projection writes and the processed-event insert. The separately committed claim counts as one attempt. Retry releases the fenced lease and delays availability by five seconds; attempt eight enters the existing dead-letter state, which excludes further automatic claims. The command runner already restarts its loop after an error, so later valid work continues while the failed event waits.

| Fixed code                  | Meaning                                                     |
| --------------------------- | ----------------------------------------------------------- |
| UNSUPPORTED_EVENT_TYPE      | No contract for this type                                   |
| UNSUPPORTED_PAYLOAD_VERSION | Known type, unsupported version                             |
| INVALID_EVENT               | Invalid aggregate metadata, object shape or required fields |
| PROJECTION_FAILED           | Other transaction, database or projection failure           |

Only the code is retained in `last_error_code` and added to error logs. Raw types, payloads, principal references and database errors are not added to diagnostic fields or logs. Original payloads remain in the restricted outbox. Retry-storage failures are returned instead of discarded; if retry cannot commit, the existing lease expires before another claim. Successful delivery clears the diagnostic and retains attempt history.

Already processed events skip validation and projection on redelivery. This preserves upgrade deduplication and does not replay records already acknowledged by older workers. It does not recover notices lost before this guard.

## Deployment and recovery

Replace only the worker, preserving application/media volumes and the identity provider:

```bash
docker compose build worker
docker compose up -d --no-deps --wait worker
```

Workers predating this guard can still acknowledge unknown types. Avoid rollback to them while unsupported events might be queued. Worker-first deployment remains necessary; this guard retains failures but does not negotiate producer/consumer capabilities.

Inspect metadata through an authorized operator connection without copying payloads into shared diagnostics:

```sql
SELECT id, aggregate_type, payload_version, attempts, last_error_code,
       available_at, lease_until, delivered_at, dead_lettered_at
FROM infra.outbox
WHERE delivered_at IS NULL AND last_error_code IS NOT NULL
ORDER BY created_at, id;
```

After deploying a compatible consumer, events below the attempt limit retry automatically. For a dead-lettered compatibility failure, verify support and select one exact event ID. This SQL accepts a psql `event_id` variable containing that UUID:

```sql
UPDATE infra.outbox o
SET available_at = now(), dead_lettered_at = NULL, attempts = 0,
    lease_until = NULL, lease_owner = NULL, lease_token = NULL,
    last_error_code = NULL
WHERE o.id = :'event_id'::uuid
  AND o.delivered_at IS NULL AND o.dead_lettered_at IS NOT NULL
  AND o.last_error_code IN ('UNSUPPORTED_EVENT_TYPE', 'UNSUPPORTED_PAYLOAD_VERSION')
  AND (o.lease_until IS NULL OR o.lease_until < now())
  AND NOT EXISTS (SELECT FROM infra.processed_event p WHERE p.event_id = o.id)
RETURNING o.id;
```

Require exactly the reviewed ID in the result. Zero rows means it is ineligible; do not broaden the update. Investigate malformed events and generic failures separately. Never bulk reset delivered/processed history, consent-skipped events, volumes or notifications. Recovery rechecks eligibility and preserves duplicate protection.

## Verification and boundaries

Race-enabled integration tests cover unknown types, future versions, eight-attempt dead lettering, rolled-back counts/deduplication, valid work behind failures, wrong aggregates, malformed objects/fields, private diagnostics, corrected reply retry, diagnostic clearing and read-state-preserving redelivery. Full producer workflows cover recognized events, private operational isolation, worker privileges and lease fencing.

Operator recovery remains manual. Capability negotiation, queue dashboards/metrics, operator recovery authorization/audit, configurable backoff, production operations and recovery of previously acknowledged unsupported events remain pending.

# Reviewed public progress lifecycle

Publishers can correct a reviewed public preview, withdraw it, and explicitly review it for republication. Private case work, reports, obligations, verification and the original first-reported time continue independently. This is synthetic local human review; the [canonical specification](spec/README.md) remains the production target.

## Publisher experience

The staff case panel shows the current reviewed title, summary, broad area, publication revision and approved case revision. A private publication reason and preview confirmation are required for each changed publication. Withdrawal requires its own reason and confirmation. Cancelling keeps the current public progress and preserves the reason draft; reopening requires confirmation again.

The panel retains title, summary, area and reason drafts when another review or operational change wins. **Refresh publication review** reads the latest case and publication, keeps the draft for comparison, and clears confirmation. The publisher must review again before retrying. After a lost response, a stale retry conflicts; refreshing and submitting the already committed preview is a no-op. The newest twenty private decisions show action, reason, time and case/publication revision. Historical decisions with no recorded publication revision or separate reason are labelled legacy reviews.

Only an active publisher with case access receives the review controls, current unpublished preview and history. Agency staff continue case work without these private publication fields. Live role and session checks apply both to commands and to the narrow database views, including previously established transaction scope.

## Commands and versions

The executable contract is [OpenAPI 0.18.0](../contracts/openapi/core.yaml).

| Command                                              | Preconditions                                                                                                                                                                     | Result                                                                                                         |
| ---------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------- |
| `POST /authority/cases/{id}/publications`            | `If-Match` operational case version, expected `publicationVersion` (zero before first publication), safe fields, private reason, `reviewed: true`, and current sharing permission | Initial publication or republication records PUBLISH; a changed published preview records CORRECT.             |
| `POST /authority/cases/{id}/publication-withdrawals` | `If-Match` operational case version, current positive `publicationVersion`, private reason and `reviewed: true`                                                                   | Records WITHDRAW and immediately hides the receipt. Existing private-only sharing does not prevent revocation. |

Both commands require an authenticated publisher and normal CSRF/origin checks. A stale case returns 412; a stale publication returns 409; missing `If-Match` returns 428. Case and receipt locks fence concurrent decisions. Each changed command atomically stores its immutable decision, current binding/receipt and safe outbox event. Returning the current identical published preview at the same operational revision, or withdrawing an already withdrawn current revision, succeeds without another decision or event.

The publication counter increments on each changed publication or withdrawal. It is separate from operational case versions and does not advance case state or reset first-reported time. Public `Receipt.version` is this publication revision. Command results return the public receipt ID, unchanged case version, publication version and state. Case IDs and private reasons do not enter public receipt DTOs, timelines or event payloads.

## Visibility and subscriptions

Withdrawal makes public receipt reads, feed/search hydration and new follows unavailable. It removes the receipt link from owner report progress while retaining the private report. CASE_PROGRESS Activity pages, counts and read commands suppress withdrawn sources. A committed withdrawal epoch keeps earlier notices suppressed even after fresh republication. Queued older publication events cannot restore progress or deliver notices through that epoch.

Existing follows are retained. A follower can explicitly unsubscribe while the receipt is withdrawn; this operation also returns an opaque success for an unknown receipt ID. Fresh reviewed republication uses the same independent receipt ID and can notify retained, eligible followers once for its new publication revision. Following later never backfills an older event. A correction within the same operational case version is a new reviewed publication; submitting its identical preview again is not.

Successful publisher commands clear that browser's feed, search, receipt and Activity caches. Other sessions observe withdrawal on their next server read or refresh; already rendered pages are not remotely erased. No realtime cross-session invalidation or content purge is claimed.

## Database, events and upgrade

[Migration 00023](../db/migrations/00023_public_progress_lifecycle.sql) preserves receipts, decisions, follows and media. It seeds existing publication counters from the highest legacy receipt/notice revision to avoid uniqueness collisions. Existing withdrawn receipts receive a matching withdrawal epoch. Historical decision revisions/reasons stay unknown. Append-only decision privileges remain in place.

Two publisher-scoped security-barrier views expose only the current review and bounded-history fields. The operations role gets these views without raw publication-decision access. Worker insertion policies require the exact canonical receipt revision and eligible recipient. The worker cannot read private publisher history.

`SafeReceiptPublished` payload version 2 contains only `receiptId` and positive `publicationVersion`; aggregate metadata still carries the operational case version. Legacy version 1 remains supported with its operational-version interpretation. `SafeReceiptWithdrawn` version 1 contains the same two safe fields and is acknowledged without projection or notification, because visibility changed in the publisher transaction. Unsupported versions and malformed revisions remain durable failures under the [outbox compatibility guard](OUTBOX_EVENT_COMPATIBILITY.md).

Build compatible images first, then use a brief maintenance window because old generated case-receipt scanners do not accept the new column shape. Upgrade the worker before enabling the new API producer:

```bash
docker compose build migrate api worker web
docker compose stop api worker
docker compose run --rm --no-deps migrate
docker compose up -d --no-deps --wait worker
docker compose up -d --no-deps --wait api web
```

Keep the existing volumes and keys. Historical processed events are not replayed. Runtime migration rollback is deliberately rejected; changes to retained review history require a reviewed forward migration.

## Verification and remaining boundaries

Race-enabled integration tests exercise same-case corrections, no-op and conflicting retries, concurrent publishers, live role/session revocation, forced row policies, private reason isolation, withdrawal across public surfaces and owner progress, delayed events, unsubscribe and fresh republication. Browser journeys cover real OIDC review, agency work after withdrawal, retained follower Activity, stale and lost responses, and 320px light/dark confirmation layouts.

[Resident withdrawal requests](RESIDENT_PUBLIC_SHARING.md) now pause new publication and allow reviewed withdrawal; approved requests prevent republication. Resident opt-in after approval, automatic redaction/detection, second-person publication policy, emergency remediation, history pagination, production retention, external purge and realtime invalidation remain pending. This milestone does not complete all FR37 acceptance contracts or authorize real/protected intake.

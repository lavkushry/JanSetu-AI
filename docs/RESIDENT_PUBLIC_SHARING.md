# Resident requests to withdraw public progress

Residents can request withdrawal of the current reviewed public service preview from their own report progress. The request remains private, pauses new publication, and enters a publisher review queue. Approval withdraws the public receipt and keeps republication blocked until the owner explicitly allows a fresh publisher review. See [resident permission renewal](RESIDENT_SHARING_RENEWAL.md). Private reports, first-reported time, agency work and verification continue independently. This is the fictional local workflow described in the [implementation status](IMPLEMENTATION_STATUS.md), with production requirements still governed by the [canonical specification](spec/README.md).

## Resident and publisher experience

**My reports → Manage public sharing** shows the current published title, summary, broad area and revision, plus the resident's newest twenty requests. A resident chooses one fixed concern: possible identification, excessive location detail, or changed sharing preference. There is no additional free-text concern field. Explicit confirmation is required. Current progress stays public while review is pending; the UI explains this before submission. A pending request can be cancelled through a separate confirmation in the same dialog. Cancellation leaves current public progress unchanged and permits a new request.

**Staff workspace → Public sharing** is available only to active publishers. Pending requests are oldest first; approved, declined and cancelled queues are newest first. Each view is bounded to one hundred items. The review includes the current safe preview, requested publication revision, current case revision and concern category. It omits requester identity, report ID, vault alias, original statement and client request ID. Publishers give separate private and resident-facing reasons and confirm the decision. Only the shared reason enters the owner's private request history. No request or reason is posted into public timelines or Activity.

A stale request, case or publication prevents submission. Refresh keeps concern or reason drafts and clears confirmation so the current version can be reviewed again. Lost request responses recover through stable IDs and live owner history; lost decision responses recover through the recorded outcome. Approval clears the deciding browser's public receipt/feed/search/Activity caches. Other browsers observe withdrawal on their next read or refresh. Already rendered content is not remotely erased.

## Commands and concurrency

The executable contract is [OpenAPI 0.19.0](../contracts/openapi/core.yaml). Normal sign-in, CSRF and origin checks apply.

| Route (under `/v1`)                                                               | Preconditions and result                                                                                                                                             |
| --------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `GET /my-reports/{id}/public-sharing`                                             | Verified owner claim; currently published safe preview or null, owner-only history and sharing pause state. Foreign reports remain unavailable.                      |
| `POST /my-reports/{id}/publication-withdrawal-requests`                           | Stable `Idempotency-Key`, `clientRequestId`, current positive `publicationVersion`, fixed `reasonCode`, `confirmed: true`; returns the canonical live request.       |
| `POST /my-reports/{id}/publication-withdrawal-requests/{requestId}/cancellations` | Verified ownership, `If-Match` request version, `confirmed: true`; only a pending request changes to CANCELLED. A retry at its current cancelled version is a no-op. |
| `GET /authority/publication-withdrawal-requests`                                  | Publisher queue; optional REQUESTED, APPROVED, DECLINED or CANCELLED state filter.                                                                                   |
| `GET /authority/publication-withdrawal-requests/{id}`                             | Live publisher access to the safe review and outcome.                                                                                                                |
| `POST /authority/publication-withdrawal-requests/{id}/decisions`                  | `If-Match` request version; current `caseVersion` and `publicationVersion`; APPROVED or DECLINED; separate reasons; `reviewed: true`.                                |

Missing `If-Match` returns 428; a stale request or case returns 412; a stale publication or already decided request returns 409. Invalid semantic fields return 422; unknown JSON fields return 400. Owner creation retries with identical content preserve the request ID, including after command-key expiry. Stored command responses hold only that ID and hydrate the current outcome. Changed content conflicts. One non-cancelled request records each report/receipt/publication snapshot, preventing repeated declined requests against an unchanged preview.

Cancellation and publisher review serialize through the pilot mutation lock before taking row locks. Exactly one wins; the stale loser cannot write another outcome. Pending requests and the latest approved request without active owner permission veto the normal publication endpoint, including an identical-preview retry. Decline and cancellation remove their own veto, but still require a fresh publication review and never override an original PRIVATE preference or another source's veto. Approval does not increment operational case versions. If the public receipt is already withdrawn, approval records the request outcome without another withdrawal event.

## Persistence and isolation

[Migration 00024](../db/migrations/00024_resident_publication_withdrawal.sql) adds immutable request provenance, independent request versions, append-only publisher decisions and owner cancellation markers. It changes no existing statement, report sharing preference, hash, ownership alias, receipt ID or case age. Cancellation markers store no principal mapping. Publisher reviewer references stay private in the canonical decision table.

Forced row policies require independently verified vault ownership for owner reads, inserts and cancellations. The insertion helper validates the canonical report/case/receipt binding and exact published revision. A transition trigger requires an existing matching decision or cancellation marker and preserves provenance. Approval additionally requires the public receipt to be withdrawn. Outcome insertion locks the canonical request and prevents both outcome kinds from committing, including through direct scoped SQL. Runtime roles cannot rewrite decision or cancellation history or supply decision timestamps.

Security-barrier views expose owner outcomes and minimal publisher review fields, with live session, role and case guards. A separate publisher-scoped eligibility view checks original sharing preferences and active requests without granting publishers raw report preference/provenance access. Social and worker roles receive no request or review access. See the [database/vault guide](DATABASE_VAULT_ISOLATION.md).

Approval reuses the reviewed withdrawal transaction and existing `SafeReceiptWithdrawn` version 1 envelope. It hides public receipt reads, feed/search hydration and earlier CASE_PROGRESS Activity through the existing withdrawal epoch. No worker grant or new private event type is introduced; the previous compatible worker remains supported.

## Volume-preserving upgrade

Build images first, briefly stop API writes, apply the forward migration and privilege grants, then restart API and web:

```bash
docker compose build migrate api web
docker compose stop api
docker compose run --rm --no-deps migrate
docker compose up -d --no-deps --wait api web
```

Retain the existing database/media volumes and vault keys. The schema-23 worker remains compatible with this event contract. Runtime rollback of retained sharing history is rejected; a reviewed forward migration is required.

## Verification and boundaries

Race-enabled integration tests cover owner-only access, forged binding/version denial, unknown fields, confirmation, command-key expiry and lifetime deduplication, pending publication veto, independent agency work, stale review, approved withdrawal across public surfaces, current-outcome retries, cancellation/approval races, decline and fresh correction, private reason separation, restricted grants and live role/session revocation. Browser journeys cover real OIDC, resident confirmation/cancellation, publisher approval/decline, stale drafts, lost responses and 320px light/dark layouts.

[Resident opt-in after approval](RESIDENT_SHARING_RENEWAL.md) is implemented. History pagination, emergency takedown automation, policy-based second-person publication, evaluated redaction, protected intake, production retention and purge, outbound notifications and realtime cache invalidation remain pending. An approved withdrawal requires fresh owner permission and publisher review for later publication; merged multi-source cases will require an explicit sharing policy. Local global serialization remains a throughput limit. This milestone does not complete all FR37 contracts or authorize real/protected intake.

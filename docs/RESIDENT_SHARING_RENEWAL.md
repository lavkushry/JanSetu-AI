# Resident permission for future public progress

After an approved withdrawal, the report owner can allow future publisher review and undo that permission while progress is withdrawn. Permission alone leaves the receipt hidden. A publisher must review current case work, sharing rules and a sanitized preview before publishing a new revision. Original statements, photos, report preferences, hashes, case age and withdrawal decisions remain unchanged. This extends the fictional local [resident sharing workflow](RESIDENT_PUBLIC_SHARING.md).

## Resident experience

**My reports → Manage public sharing → Future public updates** shows the latest approved withdrawal and its latest permission record. This separate current record stays available even when the approved request falls outside the newest twenty requests. Historical requests retain their original outcomes; complete request and permission-history pagination remains pending.

**Allow future publisher review** opens an explicit confirmation explaining that a publisher may review a sanitized title, summary and broad area again. Hidden publisher drafts are not exposed or approved by the resident. The statement and private photos stay private. Confirming records permission and enables normal publisher review if every other sharing rule allows it. It never publishes, restores an old decision, sends Activity or changes operational work.

**Undo sharing permission** requires a separate confirmation while the receipt is withdrawn. If publication succeeds first, undo cannot withdraw that published revision; the resident reviews its current preview and requests another withdrawal. Permission can also be undone after a later direct publisher withdrawal, provided this remains the latest approved resident request. A later approved request requires its own fresh permission.

Refresh reads current server state and clears confirmation. A changed withdrawn revision uses a fresh client ID and command key. Lost acknowledgements can retry the same command or refresh: retries hydrate the live record, including cancellation, and cannot reactivate old permission. If the requested action is no longer available, refresh closes that confirmation and shows the current controls. Protected ownership/session errors remove the cached owner review.

## API and state rules

The executable contract is [OpenAPI 0.19.0](../contracts/openapi/core.yaml). All owner routes require sign-in, CSRF/origin validation and independently verified vault ownership. Foreign report/request/permission identifiers are unavailable. No case ID, private reason, principal reference, client ID or unpublished preview enters the owner permission DTO.

| Route under `/v1/my-reports/{id}/publication-withdrawal-requests` | Preconditions and result                                                                                                                                                                                                                                                                      |
| ----------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `/{requestId}/sharing-renewals`                                   | POST with `If-Match` withdrawal-request version, `Idempotency-Key`, stable `clientRequestId`, current withdrawn `publicationVersion`, `confirmed: true`. Targets the latest approved request and records ACTIVE permission at version 1. Returns the live owner request with `sharingReview`. |
| `/{requestId}/sharing-renewals/{renewalId}/cancellations`         | POST with `If-Match` permission version, current withdrawn `publicationVersion`, `confirmed: true`. Changes ACTIVE to CANCELLED at version 2. Retrying its current cancelled version is a no-op and cannot cancel a newer permission.                                                         |

`GET /my-reports/{id}/public-sharing` returns the current published preview or null, bounded request history, owner pause state and `permissionRequest`. Each approved request's nullable `sharingReview` contains the current publication revision, `canRenew`, `canUndo` and latest permission or null. These versions are independent: renewing or undoing permission does not increment the approved request, operational case or public receipt version.

Missing `If-Match` returns 428; stale request/permission versions return 412; changed publication or unavailable permission returns 409. Unknown JSON fields return 400; missing command keys, invalid inputs and absent confirmation return 422. Client IDs are unique for the report's lifetime. Changed content or target conflicts. Command keys are principal-scoped and expire independently of canonical history. Stored command receipts contain only the request ID and hydrate current state after replay.

For each report/case, REQUESTED always vetoes publication. The latest APPROVED request vetoes publication until it has ACTIVE permission. Older approvals become retained history after a newer approval; an older undone permission cannot permanently prevent renewal of the newest request. Another source's PRIVATE preference or effective withdrawal still vetoes the entire case. No permission overrides an original PRIVATE preference. Multi-source merge/rebinding needs an explicit product policy before implementation.

Renewal, undo and publisher commands use the existing pilot mutation lock. Concurrent confirmations converge on one ACTIVE permission. Undo racing with publication permits exactly one outcome: a hidden receipt with cancelled permission, or a newly published receipt with active permission. Every actual republication still checks the operational version, publication version, live publisher access, all source restrictions and explicit publisher review. Earlier Activity remains suppressed through the existing withdrawal epoch.

## Persistence and privileges

[Migration 00025](../db/migrations/00025_resident_sharing_renewal.sql) adds `ops.publication_sharing_renewal` with immutable target/client/snapshot provenance, server timestamps, one ACTIVE record per request and retained CANCELLED history. A guarded owner view exposes only the minimal current review. Forced row policies require a verified live owner; fixed-search-path security-definer helpers validate the canonical report/case/receipt binding, original preference, latest approved request and exact withdrawn revision. The transition trigger locks the request and receipt, forbids provenance rewrites and permits only versioned cancellation while withdrawn.

The operations role can read owner-scoped records, insert narrow provenance columns and update only state/version. It cannot delete history, supply timestamps or reactivate cancelled records. Publishers receive eligibility through their existing guarded view, with no raw permission/history grants. Social and worker roles receive no new grants. No principal mapping, private event type or outbox write is added.

## Upgrade and verification

Build images before briefly stopping API writes; retain existing database/media volumes and vault keys:

```bash
docker compose build migrate api web
docker compose stop api
docker compose run --rm --no-deps migrate
docker compose up -d --no-deps --wait api web
```

The schema-23 worker remains compatible because event contracts are unchanged. Rollback of retained permission history requires a reviewed forward migration. Existing approved withdrawals stay blocked until their owners explicitly opt in.

Race-enabled integration checks cover owner isolation, immutable original data, forged targets/revisions, confirmation, missing versions/keys, lifetime retries after key expiry, undo and fresh consent, repeated withdrawals, latest-approval supersession, current permission outside bounded history, PRIVATE source veto, restricted grants, direct scoped SQL, session revocation, concurrent confirmation and undo/publication races. Browser journeys cover real OIDC, confirmation/dismissal, hidden progress after opt-in, fresh publisher review, stale undo, lost acknowledgements and 320px light/dark layouts.

Production policy, emergency takedowns, evaluated redaction, independent publication oversight, retained-history pagination, outbound notifications, external purge and realtime invalidation remain pending. Existing rendered content is not remotely erased. Local global serialization limits throughput. These checks do not establish all FR37 contracts or authorize real/protected intake.

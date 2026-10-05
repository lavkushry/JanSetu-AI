# Private content reports and moderator decisions

Residents can report another person's published post or comment from its actions. The dialog captures the published revision being viewed, offers seven reasons, and accepts up to 1,000 characters of private detail. Choosing **Another concern** requires at least five characters. Submitting a report creates a private receipt; it does not automatically hide the source.

**Account → Your content reports** shows the owner's reports, source availability, and recorded outcomes. **Staff workspace → Reported content** gives platform moderators a separate queue with reasoned dismissal or confirmed removal. These content reports are separate from private civic-service intake and its vault-backed case workflow.

This implementation uses fictional local accounts and human moderation. It does not establish production moderation policy, emergency response, or full P0 acceptance.

## Resident experience and privacy

Only the currently published revision is eligible. An author's pending edit is never shown in a report preview. The dialog retains its opening revision if the underlying page refreshes; a fresh command rejects a changed source rather than silently reporting its replacement. Authors manage their own content through edit/delete controls instead of self-reporting.

A report receipt contains the target type, ID and reported revision, complaint reason/details, creation time, version and decision. Owner and moderator DTOs omit reporter, principal and staff actor identifiers. Another resident, including the source author, cannot read the owner's receipt. Moderators use their dedicated queue; the owner endpoint still requires ownership even for staff.

Read-time source previews show only the same still-public reported revision. A newer published revision yields `CHANGED` with a null preview. Deletion, removal, an inactive author/community, private community visibility, or an owner/source block yields `UNAVAILABLE` with a null preview. Moderators may review public sources across personal blocks. Private mutes affect recommendations and Activity; they do not prevent explicitly reporting readable public content. Published comments retained under a deleted post remain reportable without exposing the deleted post's title.

Complaint details and the private outcome remain available when the source becomes unavailable. Residents should use fictional details for local testing. Reporter IDs omitted from DTOs do not prevent a person from identifying themselves in their own free-text complaint.

## API and retry contracts

The executable [OpenAPI contract](../contracts/openapi/core.yaml) is version `0.10.0`.

| Endpoint                                             | Contract                                                                                                                                                                           |
| ---------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `POST /v1/content-reports`                           | Authenticated session, CSRF and `Idempotency-Key`; `targetType`, `targetId`, positive `targetRevision`, `reasonCode`, optional `details`; returns a private receipt with HTTP 201. |
| `GET /v1/me/content-reports`                         | Owner-only, newest first, twenty rows per signed keyset page.                                                                                                                      |
| `GET /v1/me/content-reports/{id}`                    | Owner-only receipt and current safe source preview; foreign IDs return 404.                                                                                                        |
| `GET /v1/moderation/content-reports`                 | Platform moderator only, oldest first, twenty open reports per signed keyset page; excludes reports the moderator filed and their own authored targets.                            |
| `POST /v1/moderation/content-reports/{id}/decisions` | Platform moderator, CSRF and `If-Match`; `action` of `DISMISS` or `REMOVE`, reason of 5–1,000 characters and the reported `targetRevision`.                                        |

Reasons are `SPAM`, `HARASSMENT`, `HATE`, `THREATS`, `PRIVACY`, `MISINFORMATION` and `OTHER`. Text limits count Unicode characters after trimming whitespace. Receipt state is `OPEN` or `DECIDED`; decision fields are action, reason and decision time. Read views add `targetState: AVAILABLE | CHANGED | UNAVAILABLE` and a nullable source containing post ID, published title/body and chosen author name. Response fields are explicit projections, not serialized database rows.

Normal command-key replay lasts 72 hours and caches no source text. Failed acknowledgements keep the same key and draft in the dialog. Independently of key expiry, one report per principal, target type/ID and published revision is retained for the lifetime of the row. An identical submission under a new key returns that existing receipt after checking current source eligibility. It does not reopen a decided report; the UI directs the resident to its recorded outcome. Changing the complaint for an already-reported revision returns `409 REPORT_ALREADY_EXISTS`. A changed source returns `409 REPORT_TARGET_CHANGED`; inaccessible sources return 404, and self-reports return 422.

Each account may create ten new content reports per rolling hour. Identical retries are checked before this quota and remain usable above it. New reports above the quota return `429 REPORT_LIMIT_REACHED`. Shared pilot command serialization makes the count and insertion atomic; replacing that serialization for scale requires preserving this property.

Both lists use signed cursors bound to the endpoint and viewer, with expiry and a stable time/ID order. Cross-owner or cross-endpoint cursors are rejected. Pages have no total-row cap; refresh restarts from the beginning.

## Decisions and visibility

Dismissal records a reason without changing source visibility. Removal requires confirmation in the UI and a fresh check that the reported revision is still published and public. A changed or unavailable source returns `409 OBSOLETE_REPORT`; staff can dismiss that report and separately review current content. A stale case version, revision or state returns 412. Staff cannot decide reports they filed or reports against their own authored content; another moderator must review them.

The command transaction rechecks the session, active profile and current platform grant after acquiring its locks. Removal hides the target, increments its aggregate version, inserts an immutable decision, closes the report and emits the existing `ContentRevoked` outbox event atomically. That event contains no complaint or reporter metadata. Reporting and dismissal do not emit a public complaint event. Each report is decided separately; removing a source does not silently decide other residents' reports about it.

Removed targets disappear from public reads, feed/search hydration, bookmark results and eligible Activity sources. Retained descendant comments keep their independent visibility; new replies to a removed parent are rejected. Existing publication-review decisions cannot revive a removed target through a pending edit. Removal does not erase authorized private author revision access or audit records, and there is no implemented restoration or appeal flow.

Publication review remains a separate staff tab. Its queue selects only `PUBLICATION_REVIEW` cases without a reporter. Each decision endpoint rejects the other workflow's case IDs.

## Persistence and upgrade

[Migration 00015](../db/migrations/00015_content_reporting.sql) reuses the canonical `social.moderation_case` and `social.moderation_decision` tables. Partial unique indexes enforce report identity; additional indexes serve owner and staff pagination. A security-invoker source view selects only eligible published content. No private candidate or source-text snapshot is copied into a report row.

Both moderation tables have forced row-level security. Residents select only their own reports and corresponding decisions. Runtime insert policies enforce reporter ownership, public target revision, self-report exclusion and blocks; publication-review insertion remains restricted to the author's candidate. Current moderators can review/decide cases; report decision insertion excludes both the reporter and the target author. Runtime grants restrict inserted/updated columns, prevent forging report creation timestamps and prohibit modifying or deleting decision audit rows. Internal reporter references remain in the restricted canonical table.

Upgrade existing named volumes with:

```bash
docker compose build migrate api worker web
docker compose run --rm --no-deps migrate
docker compose up -d --no-deps api worker web
docker compose ps
```

The migrator applies the additive schema and refreshes restricted grants. Existing service reports, posts, media, case data and volumes are preserved. Existing worker event contracts are unchanged. Retained reports require a reviewed forward migration rather than destructive rollback. See the [database/vault guide](DATABASE_VAULT_ISOLATION.md) for the broader deployment boundary.

## Verification and remaining work

[Go integration tests](../services/backend/internal/app/content_reports_integration_test.go) use temporary real PostgreSQL databases and restricted roles. They cover concurrent retries, lifetime deduplication, published-revision privacy, cross-owner denial, forged reporters/timestamps, immutable decisions, revoked staff grants, cursor ownership/expiry, quotas, blocks, changed/deleted sources, stale decisions, reporter/author self-moderation denial and independent reviewer acceptance, post/comment removal, retained replies and pending-edit revival rejection. A [worker concurrency regression](../services/backend/internal/app/worker_concurrency_integration_test.go) coordinates real notification insertion and a command targeting its recipient/post to detect the profile/post deadlock. Commands and projections share the pilot mutation lock before row locks; notification delivery, lease fencing and deduplication remain atomic. The full suite runs with race detection.

[Browser journeys](../tests/e2e/core.spec.ts) cover native modal cancellation, required Other detail, Unicode code-point minimums in resident/staff forms, a lost acknowledgement and same-key recovery, owner history, staff dismissal, an identical submission after dismissal, confirmed post/comment removal, retained replies, suppressed Activity and 320-pixel light/dark layouts. The production build, documentation validation and generated SQL/TypeScript drift checks accompany these tests.

Still pending: anonymous reporting; media/profile/community report targets; author decision notices; independent appeals/restoration; community-scoped moderation; evaluated risk triage and emergency staffing; externally retained moderation audit and evidence-retention policy; production quota/scale evaluation; multilingual and full assistive-technology acceptance. Passing the local synthetic workflows does not satisfy these production gates.

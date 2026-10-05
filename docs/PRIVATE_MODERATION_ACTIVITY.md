# Private moderation Activity

Authors receive IN_APP Activity notices when a new publication review restricts their post/reply, a content report removes their published content, or an independent review finalizes their appeal. Each notice opens the exact private record. The [author decision history](AUTHOR_MODERATION_DECISIONS.md), [independent appeal workflow](INDEPENDENT_MODERATION_APPEALS.md) and public publication rules retain their existing ownership and revision checks.

[Publication approval Activity](PUBLICATION_APPROVAL_ACTIVITY.md) now covers fresh ALLOW publication decisions with a separate fixed approval card.

## Resident experience

The Activity **Moderation** filter has independent twenty-item signed cursor pages, alongside All, Conversations and Service progress. [Private content report outcomes](CONTENT_REPORT_OUTCOME_ACTIVITY.md) also appear in this filter. Notices use fixed messages and generic titles. They expose no content preview, complaint grounds, internal note, shared reason, reporter/reviewer identity or principal/provider binding. Their actor is null. Mark as read/unread persists and opening a record does not automatically mark it read.

| Notice                | Exact owner page                             | Delivery rule                                                              |
| --------------------- | -------------------------------------------- | -------------------------------------------------------------------------- |
| `MODERATION_DECISION` | `/account/moderation-decisions/{decisionId}` | New RESTRICT publication decisions and REMOVE report outcomes only.        |
| `APPEAL_OUTCOME`      | `/account/appeals/{appealId}`                | Final UPHELD or REVERSED outcomes, including reversal without restoration. |

Exact pages fetch the existing owner-only record endpoint. Links remain usable when records move beyond the first history page. Account history also provides exact-record links. A signed-out visitor sees sign-in; another account receives the same unavailable state as an unknown record, including a platform moderator using these owner routes. Current-thread links still follow current public visibility rules. Soft deletion preserves the decision/appeal notice and private record. A notice records a historical action and does not assert that content remains restricted or has been restored.

## Consent and retries

Account → Activity preferences controls IN_APP consent for all Activity kinds. Turning consent off hides delivered notices, read commands and unread counts, and prevents queued delivery when the worker processes the event. Manual owner history and exact-record reads remain available. Re-enabling consent restores eligible delivered notices with their existing read state. Events processed while paused or while the recipient is inactive are skipped and are not backfilled.

Blocks and person/community mutes do not suppress the owner's own institutional review notices. They retain their existing effects on public content and reply Activity. Browser views refresh every thirty seconds or on manual refresh; changes in another session are not instantly streamed into rendered content.

Negative moderation commands atomically record a `ModerationDecisionRecorded` outbox event with the decision as aggregate and version 1. Final appeal commands atomically record `AppealOutcomeRecorded` with the appeal and its finalized version. Both payloads are empty objects. Fresh publication approvals separately record `PublicationApprovalRecorded`. Report dismissal sends only the reporter outcome, while opening/claiming an appeal and historical decisions without new events generate no author review alert.

The worker resolves the recipient from the canonical source, honors current eligibility, inserts the notice, records event deduplication and acknowledges its fenced lease in one transaction. Source/recipient uniqueness protects both event redelivery and equivalent events. Failed projections roll back and use existing retry/dead-letter handling. Existing public-source events and publication projections continue independently.

## Database and upgrade

[Migration 00020](../db/migrations/00020_private_moderation_activity.sql) adds nullable decision/appeal foreign keys, constrained source shapes and unique per-owner source indexes. Existing notification history remains intact. Rolling back retained history requires a reviewed forward migration.

The privileged security-barrier view `social.activity_review_source` projects only kind, source ID, recipient profile ID and version. Only the worker receives SELECT. It receives no new read privileges on complaint details, canonical moderation/appeal notes, principal IDs or account bindings. Its notification INSERT policy verifies the exact eligible source, recipient and version for private review kinds.

The API selects the separate security-barrier `social.activity_review_target`, scoped by the live authenticated profile, and the existing security-invoker inbox view. It cannot select the unfiltered worker source view. Forced recipient RLS and read-at-only update grants continue to protect notification rows. Canonical complaint and appeal record policies are unchanged.

Preserve the application/media volumes and identity provider. Upgrade the worker before new API commands can produce these events: an older worker does not understand the new types and could acknowledge them without delivery.

Workers updated with the [outbox compatibility guard](OUTBOX_EVENT_COMPATIBILITY.md) retain unsupported types/versions for bounded retry and recovery. Workers predating that guard can still silently acknowledge them; worker-first ordering and compatible rollback remain necessary.

```bash
docker compose build migrate api worker web
docker compose run --rm --no-deps migrate
docker compose up -d --no-deps --wait worker
docker compose up -d --no-deps --wait api web
```

Do not replay historical events or reset volumes. OpenAPI 0.14.0 adds the MODERATION filter, new kinds and private target IDs without changing Activity's fields or adding API endpoints.

## Verification and boundaries

Go integration checks cover restrictions and comment removals, finalized appeals, exact recipient ownership, fixed DTO/event/row privacy, limited worker grants, wrong-recipient insert denial, live session revocation, persistent read state, consent, block/mute behavior, suspended recipients, duplicate events, soft deletion, historical/positive-action suppression and signed pagination with equal timestamps.

Browser journeys use real OIDC sessions and worker delivery for private links, foreign-record denial, outcome access after source deletion, consent changes and persistent read state. They check 320px overflow and light/dark screens. Existing reply/case/media/social/civic checks remain part of CI.

This remains a local IN_APP milestone. Account-security alerts, outbound channels, quiet hours, configurable retention, realtime delivery, production moderation policy, accessibility acceptance and large-scale delivery are pending. Pilot command serialization and process-local cursor keys retain their existing capacity/restart limits.

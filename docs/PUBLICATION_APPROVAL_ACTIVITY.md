# Private publication approval Activity

Authors now receive a **Publication review** Activity card when a fresh publication review approves their post or reply. Its fixed message is “A publication approval is available for your content.” The card opens the exact private decision at `/account/moderation-decisions/{decisionId}`, including the reviewed revision and safe approval wording. It appears in All and Moderation, alongside the existing restriction/removal, appeal and reporter outcome notices.

## Revisions and privacy

First publication, approved edits and corrected initial resubmissions each create an independent immutable decision and one approval notice. Pending drafts/edits do not notify. The approval card contains no source title/body, internal note, reviewer identity, principal reference, complaint detail or public source ID. Its actor is null. The existing private owner endpoint also denies access to other residents and platform moderators.

An approval records a historical review. It does not promise that the source is still public, that its latest revision is approved, or that it cannot later be restricted or deleted. Older exact decisions remain accessible after subsequent edits and source deletion. **Open current thread** follows current visibility rules. Opening a decision does not automatically mark the Activity card read; explicit read/unread changes survive reload and redelivery. Approvals do not offer the restriction/removal appeal action.

The first approved reply still gives its conversation recipient a separate REPLY notice. The reply's author receives the private approval, while the post/direct-parent author receives the conversation notice. Approved reply edits notify the reply author for the new decision without repeating the original conversation alert. Self-reply suppression remains unchanged.

## Delivery and consent

Fresh publication ALLOW commands atomically append `PublicationApprovalRecorded`, with the decision ID, aggregate type MODERATION_DECISION, source version 1 and an empty object payload. RESTRICT commands retain `ModerationDecisionRecorded`. Content-report dismissal is an ALLOW decision in a different workflow and never produces a publication approval. Appeal reversal retains its existing appeal outcome and is not a new publication review.

The worker explicitly supports the new type through the [outbox compatibility guard](OUTBOX_EVENT_COMPATIBILITY.md). Projection, deduplication and acknowledgement commit together. A unique recipient/decision index covers both approval and negative decision kinds; retries and equivalent events preserve the original card and read state. Historical approvals without the new event are not backfilled.

Account IN_APP consent applies at delivery and read time. A paused/inactive recipient at processing receives no notice or later backfill. Pausing hides existing cards, unread counts and read commands, while manual private history/exact decisions remain accessible. Resuming restores eligible delivered cards and their read state. Person/community mutes and interpersonal blocks do not suppress the owner's own institutional approval history.

## Database and upgrade

[Migration 00022](../db/migrations/00022_publication_approval_activity.sql) adds PUBLICATION_APPROVAL to notification/source-shape rules, widens the existing unique decision index, and derives approval kind from canonical publication ALLOW decisions. It preserves the shared four-field worker projection and live owner-scoped API projection. Notification insertion still requires the exact source kind, recipient and version. No new foreign key, endpoint, identity/complaint read grant or canonical record policy is added. OpenAPI 0.16.0 adds the Activity kind; the target remains MODERATION_DECISION.

Preserve application/media volumes and the identity provider. Apply the migration, upgrade the worker, then replace API/web before enabling fresh approval events:

```bash
docker compose build migrate api worker web
docker compose run --rm --no-deps migrate
docker compose up -d --no-deps --wait worker
docker compose up -d --no-deps --wait api web
```

A worker with the compatibility guard but without this event type retains it as an unsupported delivery; workers predating the guard may acknowledge it silently. Use compatible worker rollback and the targeted recovery rules in the guide. Retained approval history requires a reviewed forward migration for rollback. Do not replay old approvals or reset volumes.

## Verification and boundaries

Race integration checks cover post/reply approvals, approved edits, corrected resubmission, pending and historical suppression, separate reply audiences, fixed metadata/empty events, foreign records, restricted worker grants, wrong-kind/recipient/version row-policy denial, read idempotence, duplicate delivery, paused consent, inactive authors, source deletion, moderator blocks/mutes and revoked-session scope. Existing report dismissal/removal, appeal and pagination checks remain part of the full suite.

Browser journeys use real OIDC, staff publication review, real worker delivery, exact private decision links, edited-revision history, foreign denial, source deletion, reply audiences, consent, read-state persistence and 320px light/dark layouts. This remains local IN_APP delivery. Outbound channels, realtime updates, production moderation/retention operations and complete accessibility acceptance remain pending.

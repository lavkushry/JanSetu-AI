# Private content report outcome Activity

Residents receive IN_APP Activity notices when a moderator dismisses their content report or removes the reported post/comment. Notices open the exact private receipt at `/account/content-reports/{reportId}`. This extends [content reporting](CONTENT_REPORTING.md) and [private moderation Activity](PRIVATE_MODERATION_ACTIVITY.md); it does not change the civic-service reporting workflow.

## Experience and privacy

The **Moderation** Activity filter includes `CONTENT_REPORT_OUTCOME`, alongside author decisions and appeal outcomes. The fixed message is “A review outcome is available for your content report.” The target contains only the private report ID, kind `CONTENT_REPORT` and generic title. Actor is null. Complaint text, source IDs/previews, report reason, outcome explanation, shared author reason, reporter/reviewer principal IDs and provider bindings never enter the notice or its empty event payload.

The exact page fetches the existing reporter-owned endpoint and reuses the Account receipt card. Account history also links directly to each receipt. Pages remain reachable when records move beyond the first history page. A source author or moderator cannot use the reporter's owner route; foreign and unknown record IDs produce the same unavailable page. Sign-out and account changes clear private query caches.

Receipt details and the moderator's explanation remain private to the reporter. Source previews still use the existing read-time checks: only the same published, accessible reported revision appears. Changed, removed, deleted or blocked sources have no replacement preview. Receipt and notice history remain available when the source disappears. The fixed notice describes a historical outcome without claiming the content's current publication state.

REMOVE produces distinct alerts and exact records: the reporter receives their content report outcome, and the author receives their own moderation decision with the separate author-facing reason. Neither notice reveals the other recipient's private record. DISMISS produces only the reporter outcome; publication approval and new pending reports generate no report-outcome notice.

## Consent and delivery

Activity preferences govern IN_APP delivery, inbox visibility, unread count and read/unread commands. Manual private receipt access remains available while notifications are paused. Re-enabling consent restores eligible delivered notices and their read state. Events processed while consent is paused or the recipient is inactive are terminally skipped; they are not replayed when eligibility returns. Person/community mutes and interpersonal blocks do not suppress the reporter's own institutional review outcome.

The final moderation command commits the decision, finalized report version and `ContentReportOutcomeRecorded` event atomically. Its aggregate is `CONTENT_REPORT`, ID is the private report ID, and version is the finalized report version. The worker resolves the recipient from the canonical report, rechecks eligibility, inserts the alert and commits deduplication/lease acknowledgement in its existing transaction. A unique recipient/report index suppresses redeliveries and equivalent events. Historical finalized reports without the new event are not backfilled.

## Database and deployment

[Migration 00021](../db/migrations/00021_content_report_outcome_activity.sql) adds the nullable report foreign key, source-shape constraints and unique index while retaining existing notifications. The shared privileged source projection still exposes exactly four fields: kind, source ID, recipient profile ID and version. No new runtime grants expose canonical complaint text, outcome explanations, identities or account bindings.

The API's source view remains scoped by the live authenticated profile. Forced recipient RLS, read-at-only API updates and the worker INSERT policy enforce source/recipient/version matching. Canonical report ownership and moderator authorization are unchanged. OpenAPI 0.15.0 adds the new notice/target kinds without adding API endpoints or Activity fields.

Preserve the existing application/media volumes and identity provider. Upgrade the worker before new API commands produce the event; older workers could acknowledge unknown event types without delivering notices.

```bash
docker compose build migrate api worker web
docker compose run --rm --no-deps migrate
docker compose up -d --no-deps --wait worker
docker compose up -d --no-deps --wait api web
```

Notification/report history requires a reviewed forward migration for rollback. Do not reset volumes or replay historical events.

## Verification and boundaries

Go integration tests cover post dismissal, comment removal, pending/historical suppression, owner-only notice/receipt access, source/identity/text isolation, restricted worker privileges, forged recipient/version denial, persistent read state, event redelivery, consent, inactive recipients, deleted/blocked/muted sources and live session revocation. Existing signed cursor and moderation workflows remain part of the full race suite.

Browser journeys use real OIDC accounts and worker delivery for dismissal/removal, distinct author/reporter records, exact links, receipt retention, consent, persistent read state and sign-out/account switching. They inspect 320px light/dark screens. CI also runs the complete social, civic and private-photo workflows.

This is local IN_APP feedback for post/comment reports. Outbound channels, approval alerts, account-security notifications, additional reporting targets, governed moderation policy, configurable retention, large-scale delivery and full accessibility acceptance remain pending.

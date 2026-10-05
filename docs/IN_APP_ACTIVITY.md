# In-app Activity inbox

The local milestone adds `/activity` for approved replies, reviewed public case progress and [private moderation decisions and appeal outcomes](PRIVATE_MODERATION_ACTIVITY.md), plus [reporter-owned content report outcomes](CONTENT_REPORT_OUTCOME_ACTIVITY.md). It uses the existing durable outbox and restricted worker. The [canonical specification](spec/README.md) remains the complete product target; email, push, mentions, account-security alerts, outbound channel/quiet-hour settings and production delivery are pending.

## Resident experience

The header bell and desktop navigation open a private inbox. All, Conversations, Service progress and Moderation filters have independent twenty-item keyset pages. The inbox and unread count refresh every thirty seconds while the app is active, on window focus and through **Refresh activity**. A count above 99 displays `99+` in navigation. The header bell remains available at 320px; the existing mobile navigation remains in place.

A card opens its conversation, current public receipt or exact owner-only moderation decision, appeal or content report record. **Mark as read** and **Mark as unread** persist across reloads. Opening a target does not mark it read. These controls change presentation only: they cannot acknowledge an agency submission, accept work, claim completion or record verification. Reply links open the thread; jumping directly to a comment on an unloaded page is pending.

Loading, signed-out, empty and retry states are explicit. Refresh clears the selected filter's pages and starts again, including after an expired cursor. A failed read command refreshes source visibility. Account changes clear private query caches; block commands also reset activity and counts.

## Delivery and eligibility

| Source                           | Recipient                        | Trigger and duplicate rule                                                                                                                                                       |
| -------------------------------- | -------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| First approved top-level comment | Post author                      | Moderation approval writes `CommentPublished` in the same transaction. One notification per recipient/comment; later approved edits do not notify again.                         |
| First approved nested reply      | Direct parent comment author     | Same approval rule. Self replies do not notify. The parent and thread must still be published.                                                                                   |
| Restriction or removal           | Content author                   | `ModerationDecisionRecorded` targets the immutable decision. One notification per recipient/decision. Approval and report dismissal do not create alerts.                        |
| Final independent appeal outcome | Appellant                        | `AppealOutcomeRecorded` targets the finalized appeal. One notification per recipient/appeal, including reversals that cannot restore content.                                    |
| Final content report outcome     | Original reporter                | `ContentReportOutcomeRecorded` opens the private receipt. One notification per recipient/report, for both dismissal and removal. |
| Reviewed public case progress    | Current public receipt followers | `SafeReceiptPublished` projects from the current published receipt. One notification per recipient/receipt/projection version. Internal case events never deliver public alerts. |

The worker shares source eligibility views with inbox reads and unread counts. Reply eligibility checks current publication, active authors/recipient, public or restricted active community, either-direction blocks, unexpired person/community mutes and the IN_APP channel preference. Existing preference rows are honored; without a row, IN_APP is enabled. [Account controls](ACTIVITY_PREFERENCES_AND_MUTES.md) now manage IN_APP consent and person/community mutes. Case eligibility checks an active recipient, a published receipt, current follow and IN_APP preference. Interpersonal blocks do not hide institutional case progress. Private review alerts, including content report outcomes, require an active owner and IN_APP consent; blocks/mutes do not suppress these owner records. Soft deletion of the source preserves private review notices.

The worker stores the event time, not its processing time. Publication events and case follows record statement time after command lock waits; transaction-start times could misorder a late subscription. A case follow created after publication cannot receive a delayed older event. Unfollowing hides earlier case alerts and prevents queued delivery; refollowing starts a new subscription. A receipt event with an obsolete projection version is skipped. Republishing or correcting the same operational version does not create another alert.

Blocked, muted, inactive, deleted, hidden or inaccessible reply sources disappear from later reads, read commands and counts. A deleted post suppresses its alerts even if the thread still retains comment tombstones. A notification already delivered can reappear after a reversible block/mute is removed if its source remains eligible. A notification skipped at delivery is not backfilled. Changes in another session appear on the next refresh; the app does not promise instantaneous removal from a previously rendered browser page.

The worker inserts notifications, records event deduplication and acknowledges the fenced lease in one transaction. Failed projections roll back together and use the existing retry/dead-letter behavior. Uniqueness constraints protect both event redelivery and equivalent publication events.

## HTTP contract

The executable contract is [OpenAPI 0.15.0](../contracts/openapi/core.yaml). These local routes follow the existing `/me` convention; the full specification's generalized Activity API remains a future compatibility target.

| Method and route through the BFF                                      | Contract                                                                                                              |
| --------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------- |
| `GET /api/me/activity?filter=ALL\|SOCIAL\|CASES\|MODERATION&cursor=…` | `{items,nextCursor,expiresAt}`; twenty items plus a next cursor when available.                                       |
| `GET /api/me/activity/summary`                                        | `{unreadCount}` using the same current eligibility rules.                                                             |
| `PUT /api/me/activity/{id}/read`                                      | Required `{read:boolean}`; returns `{id,readAt}`. Repeating true preserves the first read timestamp; false clears it. |

An item contains only `id,kind,createdAt,readAt,actor,message,target`. Reply actors expose their chosen public ID/name/handle. Messages are fixed safe text. Reply targets contain a public post ID and the generic title “Conversation”; case targets contain an independently identified public receipt ID and its currently reviewed title. No post/comment bodies, private edit candidates, report statements, report aliases, operational case IDs, private principal IDs or provider details enter the DTO or stored preview. Private review targets carry only their decision/appeal/report ID and a fixed title; their actor is null and shared reasons appear only when opening the owner record. There is no stored text preview.

Sessions are required; mutations retain CSRF/origin checks. Unknown, hidden and other-owner IDs return the same 404. There is no owner selector. Cursors bind the viewer, filter and five-minute deadline, use a `(created_at,id)` keyset and have no total-row cap. The existing process-local signing key means an API restart invalidates old cursors. New arrivals require refreshing the first page.

## Database and upgrade

[Migration 00013](../db/migrations/00013_in_app_activity.sql) extends the unused foundation notification table without deleting existing records. Legacy notification rows remain excluded from this inbox. The migration adds source shape constraints, source uniqueness, an owner page index, eligibility views and forced row policies. Notification history rollback requires a reviewed forward migration.

The social runtime can select only its authenticated recipient rows and update only `read_at`. It cannot insert alerts or change recipients, source IDs, channel or delivery state. The worker can read and insert notifications, reads only the comment columns needed for eligibility, and retains no operational report or vault access. The owner scope helper derives the current profile from a live authenticated session; identity-table privileges remain restricted. Preference and mute reads are also owner-scoped for the social runtime.

Upgrade the worker before the API and web, preserving the application and media volumes:

```bash
docker compose build migrate api worker web
docker compose run --rm --no-deps migrate
docker compose up -d --no-deps --wait worker
docker compose up -d --no-deps --wait api web
```

Pending older reply events do not create activity because only the new approval event triggers it. Existing pending public receipt events may notify only followers who were already subscribed when they were published and remain eligible. Historical processed events are not replayed. For private review alerts, apply [migration 00020](../db/migrations/00020_private_moderation_activity.sql) and follow the [worker-first upgrade sequence](PRIVATE_MODERATION_ACTIVITY.md#database-and-upgrade).

## Verification and remaining work

Go integration checks cover approval gating, chosen metadata, no private previews, direct-parent recipients, self-reply suppression, read/unread persistence and idempotence, foreign-owner API/RLS denial, limited worker privileges, approved edits, live blocks/mutes/deletion, cursor ownership/filter/deadline, pagination, event redelivery, publication-version deduplication, late follows, unfollow/refollow and private operational-event isolation.

Browser journeys exercise real OIDC accounts, moderation, worker-delivered reply alerts, read/unread persistence, filters, thread navigation, block/deletion suppression, mobile dark reflow, reviewed case updates, receipt navigation and unfollow suppression. These checks complement the existing account/social/media/civic workflow suite.

This milestone is IN_APP only. Outbound channels, quiet-hour scheduling, alert retention/deletion policy, mentions, account-security notices, realtime streaming, scalable fan-out, accessibility evaluation and production notification acceptance gates remain pending. Existing broad local command serialization and process-local cursors retain their documented capacity limits.

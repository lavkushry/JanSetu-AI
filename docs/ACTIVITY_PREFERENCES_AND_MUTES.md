# Activity preferences and mutes

This local milestone adds account controls for IN_APP notification consent and private person/community mutes. It extends the [Activity inbox](IN_APP_ACTIVITY.md) without enabling email, push or quiet-hour scheduling. The [canonical specification](spec/README.md) remains the complete product target.

## Resident controls

Account contains **Activity preferences** and **Muted people and communities**. The IN_APP switch requires an explicit save and also covers [private moderation notices](PRIVATE_MODERATION_ACTIVITY.md) and [content report outcomes](CONTENT_REPORT_OUTCOME_ACTIVITY.md). Pausing notifications does not prevent opening your private decision or appeal history. Turning it off hides eligible existing inbox entries and unread counts, and suppresses new worker delivery. Turning it back on restores previously delivered entries whose sources remain eligible. Events processed while consent was off are skipped; they are not backfilled.

Mute a person from their public profile or a post's options menu. Mute a community from its detail page. Choose one hour, 24 hours, seven days or **Until I unmute**. Account lists active, expired and unavailable targets with a removal action and twenty-item pages. A mute on an inactive or blocked target can still be removed; unavailable metadata is replaced with a placeholder.

| Surface                                          | Mute behavior                                                                                                                                        |
| ------------------------------------------------ | ---------------------------------------------------------------------------------------------------------------------------------------------------- |
| Home, Following, Nearby and community post feeds | Active person/community mutes remove matching posts. Existing feed snapshots recheck mutes during hydration.                                         |
| Post search                                      | Matching posts are excluded at selection and hydration. People and community directory results remain available.                                     |
| Reply Activity                                   | Existing and queued reply alerts recheck active person/community mutes, through the shared delivery/read eligibility views.                          |
| Explicit public profile, thread or bookmark      | Remains readable under ordinary publication and block rules. Muting does not confer access to otherwise unavailable content.                         |
| Follows and memberships                          | Remain intact. Unmuting does not require following or joining again.                                                                                 |
| Public service receipts                          | Remain available; interpersonal and community mutes do not suppress institutional case progress. IN_APP consent still applies to all inbox delivery. |
| Deleted post                                     | Author and body remain hidden. Its mute flag is false so the tombstone cannot reveal a former author indirectly.                                     |

Private moderation decisions, appeal outcomes and content report outcomes remain eligible despite person/community mutes or blocks; they are the owner’s institutional records.

Mute expiry restores eligibility on the next read or refresh. A delivered alert can reappear after unmuting if its source is still available; a skipped alert is not replayed. Notification preference changes and mutes clear affected caches before fresh content is displayed. Changes from another session are reflected on the next query refresh; existing rendered browser content is not a realtime stream.

## Executable API contract

[OpenAPI 0.9.0](../contracts/openapi/core.yaml) and generated web types describe the local endpoints:

| BFF route                                | Behavior                                                                                                                              |
| ---------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------- |
| `GET /api/me/notification-preferences`   | Owner-only `{inApp,version}`. No stored row means `true,1`.                                                                           |
| `PATCH /api/me/notification-preferences` | Required `{inApp:boolean}` and If-Match. Saves only IN_APP membership, preserving other stored channel choices and preference fields. |
| `GET /api/me/mutes?cursor=…`             | Owner-only `{items,nextCursor,expiresAt}`, including expired/unavailable rows.                                                        |
| `PUT /api/me/mutes`                      | `{targetType:PROFILE\|COMMUNITY,targetId,active,expiresAt?:null\|date-time}` desired state.                                           |

Preference writes reject a stale version with 412 and missing If-Match with 428. A no-op using the current version retains that version. An unsaved checkbox change retains the version on which it was based, even when a background refresh arrives. **Reload preferences** explicitly discards that edit and loads the latest state. Only the IN_APP choice is exposed; this does not grant consent to other channels.

Active mute targets must be available, and a person cannot mute themselves. A supplied expiry must be future and within one year; omitting it means indefinite. Removal needs no current target availability. Retrying an identical mute preserves its row ID and timestamp; changing expiry reorders it as a new choice. Cursor ownership, target-independent endpoint identity and a fixed five-minute deadline are signed. An expired cursor restarts through **Refresh mute list**. The existing signing key is process-local.

Public profile viewer state, community projections and post viewer state include the viewer's own active mute flag. These flags do not disclose another person's mute list. Mute-list metadata contains chosen public labels/handles only, with placeholders for inactive/blocked people and private/inactive communities. There are no report statements, principal IDs, provider identifiers or protected-case signals in these DTOs.

## Database and upgrade

[Migration 00014](../db/migrations/00014_activity_preferences.sql) adds a mute creation timestamp/index and owner INSERT/UPDATE/DELETE policies. Existing rows and application/media volumes remain intact. Existing mute timestamps are initialized at migration time; older activity eligibility keeps its current behavior. Rollback requires a reviewed forward migration.

Forced recipient policies from migration 00013 remain in place. The social runtime can insert/update its own preference row and read only the exposed preference columns; it can update only notification channels, policy version and version. It cannot change language/interests or another owner's preference through these grants. Mute writes enforce authenticated owner scope and allow only expiry/timestamp updates; owner/target IDs cannot be reassigned. The worker retains read access needed to honor consent/mutes, with no new write grants.

Commands use the existing fresh-session and profile checks plus shared local command lock. Preference materialization, version checking and update occur in one transaction. IN_APP consent changes take effect through existing worker/read eligibility views. Feed snapshots also recheck mutes rather than trusting an earlier candidate selection.

```bash
docker compose build migrate api web
docker compose run --rm --no-deps migrate
docker compose up -d --no-deps --wait api web
```

The existing worker can continue using the unchanged Activity projection queries; no event replay or volume reset is required. Keep the web/API network bindings configured for this project.

## Verification and boundaries

Go checks cover default consent, persistence, stale/no-op versions, preservation of unrelated preference fields/channels, current worker delivery, no backfill, RLS read/write/insert denial, mute retry identity, expiry, unavailable-target removal, private/blocked metadata placeholders, pagination/deadline/owner binding, current mute checks on old feed snapshots, post search, direct access/bookmarks, unchanged follows, community flags and deleted-author privacy.

Browser checks use real OIDC accounts and API/worker data for two-session stale edits, save/reload, paused Activity, skipped and resumed reply delivery, person/community mute dialogs, duration, profile/post flags, search suppression, explicit bookmarks/threads, Account removal and 320px reflow. Post-menu coverage verifies that the confirmation remains usable after the menu closes, including cancellation, confirmation and unmuting.

Email/push, quiet-hour delivery, digest scheduling, topic/keyword mutes, account-security notifications, production consent/audit retention and full accessibility/scale acceptance remain pending. Appearance still uses the existing device-local controls; this milestone does not implement the full preference schema or all canonical settings requirements.

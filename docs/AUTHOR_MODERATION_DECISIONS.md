# Private author decisions and corrected initial submissions

Authors can open **Account → Moderation decisions** to see private decisions about their posts and replies. Each record identifies the target, reviewed revision, action, policy version and decision date. Approval notices use a fixed message. Restrictions and removals use a separately written author-facing reason. The history has newest-first twenty-item pages, signed author/endpoint-bound cursors and a five-minute cursor expiry; Refresh restarts pagination.

The history contains no source preview, reporter identity, complaint grounds, internal review note or moderator principal ID. Dismissed complaints do not appear as publication approvals. Legacy restrictions/removals without an author-facing reason show an explicit fallback; internal notes are never backfilled into those notices. Soft deletion retains the author's private decision record. Current-thread links apply the thread's normal visibility rules and may lead to unavailable content.

## Review commands and privacy

`POST /v1/moderation/{id}/decisions` requires `authorReason` for RESTRICT. `POST /v1/moderation/content-reports/{id}/decisions` requires it for REMOVE. Both require 5–1,000 trimmed Unicode code points. The existing `reason` remains an internal publication-review note or the private reporter's outcome explanation. Staff write the separate shared field and must exclude reporter identities and private complaint details. Allowing publication or dismissing a report does not store the optional shared field.

`GET /v1/me/moderation-decisions` and `GET /v1/me/moderation-decisions/{id}` require an active authenticated author session. Foreign IDs return 404, including for a platform moderator. OpenAPI 0.13.0 documents these reads and the conditional shared-reason inputs.

Migration `00018_author_moderation_decisions.sql` adds nullable `author_reason` and the security-barrier view `social.author_moderation_decision`. The view deliberately uses the migration owner's privileges to project only safe fields from canonical cases/decisions; `authz.current_profile()` derives ownership from the live session. Runtime grants permit SELECT on that projection and INSERT of the new reason column. They do not broaden case/decision read policies or permit decision updates/deletes. Revoked sessions produce no author rows through the view. Run the migration/privilege installer with the existing migration administrator, never with the application role.

## Corrected initial submission

A hidden post or comment can be edited and resubmitted only when it has **never had a published revision** and its current revision is REJECTED. The normal versioned PATCH command creates a new PENDING revision and moderation case atomically. The earlier rejection remains recorded. Only fresh approval of the new revision publishes it; an owner sees the pending candidate while other readers do not.

A post resubmission rechecks current community posting policy. A comment resubmission also requires a currently published, accessible thread, current posting membership and an available/replyable parent comment when replying to another comment. Stale versions and foreign edits are rejected. Ordinary edits of published descendant replies retain their existing independent visibility rules.

Published content that was removed retains its published pointer and cannot use this path, including when it already had a pending edit. Existing obsolete-review checks also reject late approval of that edit. Resubmission is a correction workflow, not an appeal or a restoration decision.

## Verification and remaining work

Integration tests exercise separate shared/internal reasons, owner-only reads, runtime case isolation, revoked-session denial, legacy fallback, equal-timestamp pagination/cursor binding, deleted-source records, initial post/comment correction, current membership/parent rules, and removal non-revival with pending candidates. A browser journey covers staff restriction, private author history, light/dark 320px layouts, editing/resubmitting both content types and fresh approval. Existing content-report browser journeys use the new shared reason for removals.

[Independent platform appeals](INDEPENDENT_MODERATION_APPEALS.md), distinct-reviewer adjudication and conservative exact-revision restoration are implemented locally. Author notification delivery, community-scoped moderation, claim reassignment and production moderation operations remain pending.

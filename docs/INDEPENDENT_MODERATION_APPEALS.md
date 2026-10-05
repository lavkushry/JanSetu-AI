# Independent moderation appeals

Authors can request independent review from **Account → Moderation decisions → Appeal decision** for a restriction or removal. Grounds must contain 5–1,000 trimmed Unicode code points. There is one appeal per decision for life; identical grounds return the existing receipt, different grounds conflict, and ten new appeals per hour are available in the local pilot. A stable principal-bound submission key makes acknowledgement-loss retries safe for 72 hours. Keys never cache source text or original internal notes.

**Account → My appeals** shows the private grounds, state and separately written reviewer explanation. Outcomes distinguish a decision being upheld, an exact revision being restored, and an overturned decision whose content cannot currently be restored. Owner reads expose no source preview, original internal notes, reviewer/reporter identities or private complaint details. Foreign IDs return 404, including for staff. Soft deletion retains the receipt.

## Reviewer independence and lifecycle

**Studio → Appeals** lists eligible unassigned appeals and the signed-in moderator's claims, oldest first. A reviewer must hold a current PLATFORM_MODERATOR grant and differ from the appellant, original decision maker and original content reporter. Taking a review assigns it to that reviewer. Another moderator cannot inspect or take an assigned claim. Closed appeals cannot be reopened.

The staff context includes the exact original revision when it is available under current rules, the original internal review note, policy version and a current restoration explanation. Staff provide a separate reason shared with the author. Confirmation previews the expected publication effect; the server checks it again when saving. A source-version conflict requires refreshed context and a new deliberate decision. Closing the claim, inserting the immutable outcome, restoring eligible content and writing projection/activity events happen in one transaction.

| Transition | Preconditions | Publication effect |
| --- | --- | --- |
| OPEN → REVIEWING | Fresh independent moderator; current appeal version | None |
| REVIEWING → UPHELD | Assigned reviewer; original target revision; current appeal/source versions; shared reason | Unchanged |
| REVIEWING → REVERSED | Same assignment/version/reason checks | Exact revision restored only if currently eligible; otherwise a typed non-restoration result |

Neha Sen's existing fictional account has an additional local platform-moderation grant to exercise a second reviewer. Its operational verifier grant remains separate. This fixture is not a production staffing model or staff provisioning system. The identity container and saved accounts do not need to be replaced.

## Restoration boundaries

The current revision must equal the original decision's reviewed revision. A later edit or resubmission produces TARGET_CHANGED and stays unapproved by the appeal. The author must remain active, the community must be active and public/restricted, and current posting membership/contributor policy must permit publication. Inactive/private communities, deleted content, unavailable threads and another removal cannot be bypassed.

For RESTRICT, the exact revision must still be REJECTED. An initially hidden target must have no published pointer. A rejected edit on a still-published target can restore that exact edit; an intervening removal prevents it. For REMOVE, the target must remain hidden with its published pointer on the reviewed APPROVED revision. Appeals never use publication to promote a later pending candidate.

Comment restoration also requires a currently published thread with an active author. A never-published reply must still have a replyable, available parent under the source author's block rules. Previously published descendant replies retain their existing independent visibility semantics. First publication emits the existing CommentPublished activity event; restoring a previously published reply does not duplicate that notification. The worker rebuilds public counts from current canonical state.

## HTTP and database contracts

OpenAPI **0.13.0** describes these endpoints:

| Endpoint | Purpose |
| --- | --- |
| POST /v1/moderation/decisions/{id}/appeals | Author submission with CSRF and Idempotency-Key |
| GET /v1/me/appeals | Private newest-first twenty-item pages |
| GET /v1/me/appeals/{id} | Owner-only receipt |
| GET /v1/moderation/appeals | Independent oldest-first twenty-item queue |
| GET /v1/moderation/appeals/{id} | Staff review context |
| POST /v1/moderation/appeals/{id}/claim | Versioned assignment with CSRF and If-Match |
| POST /v1/moderation/appeals/{id}/decisions | Assigned outcome with CSRF, If-Match, targetRevision and targetVersion |

Cursors are signed, bound to the viewer and endpoint, and expire after five minutes. Expiry returns 410; tampered or cross-scope cursors return 422. Stale appeal versions return 412, changed source versions return 409, and ineligible/closed mutation targets return 404. Grounds/outcomes are private account data; source previews are staff-only and are never part of creation receipts. Reviewer explanations are explicitly shared with the author.

Migration [00019](../db/migrations/00019_moderation_appeals.sql) adds forced row policies to the existing appeal table, bounded grounds, page indexes, append-only appeal outcomes, an independence/lifecycle trigger and a deferred constraint that prevents a result committing without the matching closed claim. Runtime INSERT/UPDATE column grants cannot alter appeal identity, grounds or creation age, nor update/delete outcomes. Existing canonical moderation decisions remain immutable and original case read policies remain unchanged. Current sessions/grants constrain row reads; commands recheck authority within the pilot mutation transaction before row locks.

Apply the migration and privilege installer with the migration administrator. The local seed adds the second synthetic grant with ON CONFLICT DO NOTHING. A volume-preserving upgrade rebuilds migration/API/web images, runs the migration service with --no-deps, and recreates only API/web. It does not reset the application or photo volumes or identity provider.

## Validation and remaining work

Integration checks exercise ownership, Unicode bounds, stable deduplication, reviewer conflicts, live grant revocation, quota, signed pagination, append-only outcomes, atomic closure, stale source versions, exact post/comment restoration, rejected published edits, deleted sources, later candidates, independent removals, current membership and blocked parent rules. Browser journeys exercise cancellation, lost-acknowledgement retry, separate staff/owner views, 320px light/dark layouts, successful restoration and recovery of changed-source context without publishing a later edit.

Production moderation still requires governed staff scope/provisioning, funded independent review capacity, evidence/retention policy, response targets and reviewed operating procedures. Claim release/reassignment, withdrawal, notifications for appeal outcomes, community-scoped moderation and appeals for other action types are not implemented. Global pilot command serialization remains a throughput limit. These checks do not establish full production trust-and-safety readiness.

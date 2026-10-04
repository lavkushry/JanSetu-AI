# Paginated conversations

Conversations load twenty visible comments at a time instead of silently stopping at two hundred. Residents can load later comments, collapse loaded reply branches, follow a reply's parent link and keep a short excerpt beside the composer. The [implementation status](IMPLEMENTATION_STATUS.md) describes the synthetic local application's wider boundaries.

## Try the flow

Open a published post. **Load more comments** appears when another page exists. The conversation shows how many comments are loaded, rather than claiming a total count. The UI groups loaded children below their parents and caps visual indentation at three levels. The server retains the maximum reply depth of twenty.

**Hide replies** collapses loaded descendants. **Show replies** restores them, including nested collapse choices. The count describes loaded replies, not private or blocked activity. Native buttons expose expanded state and controls references for keyboard users.

A reply's context link scrolls to its loaded parent. A missing or blocked parent has a neutral **Reply to an unavailable comment** label. A deleted parent remains a body-free placeholder with accessible descendants. Selecting **Reply** focuses the composer and shows the chosen parent's published text, clipped to three lines. Cancel reply retains unsent text. Deleting the selected parent clears its excerpt.

**Refresh conversation** starts from the first page and retains unsent composer text. It recovers from an expired cursor or API restart. Successful submission shows an acknowledgement even when the new pending comment lies beyond the loaded pages.

## Contract and privacy

The [OpenAPI 0.7.0 contract](../contracts/openapi/core.yaml) defines `GET /v1/posts/{id}/comments?cursor=…` as a `CommentPage` with `items`, nullable `nextCursor` and `expiresAt`. Each SQL query selects at most twenty-one rows and returns twenty; there is no total-row cap.

Pages use `(created_at, id)` ascending. HMAC-SHA-256 cursors bind the conversation, viewer, endpoint kind and original five-minute deadline. Cross-viewer, cross-post, cross-endpoint, oversized and tampered cursors return 422; expired cursors return 410. Loading more preserves the deadline. Signing keys remain process-local.

Post, community, author and block visibility are rechecked on every page. The comment query checks post access in the same SQL statement as its bodies, covering changes after the initial lookup. Pending comments and current revision candidates are owner-only. Other readers see the approved published body. Deleted comments return null body, author and candidate, including for their owner.

New replies check the parent's publication, active author and either-direction block state inside the command transaction. Knowing an ID does not permit replying to a blocked, inactive, deleted, cross-post or over-depth target. Existing CSRF/origin, live-session and command deduplication checks still apply. Deleted post context retains eligible comments, as specified by FR-15.

Web query keys include the viewer. Account changes clear caches, and the existing [block controls](PUBLIC_PROFILES.md) cancel and clear social snapshots. The UI does not broadcast revocations across already open devices. Loading a page or refreshing rechecks authorization. An earlier creation-time comment newly approved behind a cursor appears on refresh. These are live pages, not a frozen export.

The loaded forest is indexed and walked in linear time. Rendering still grows with loaded comments. Separate branch requests, virtualization, ranking, direct links to unloaded comments, shared cursor keys, capacity evaluation and a full accessibility audit remain pending. Collapsing a branch changes its presentation; it does not fetch unloaded replies.

## Upgrade and verification

[Migration 00012](../db/migrations/00012_comment_pagination.sql) adds `(post_id, created_at, id)` for whole-conversation pages. The foundation index includes `parent_id` for branch lookups and is retained. Existing records, privileges and volumes are preserved.

```bash
docker compose build migrate api web
docker compose run --rm --no-deps migrate
docker compose up -d --no-deps --wait api web
```

Verification commands:

```bash
make check
JANSETU_VISION_BINARY="$PWD/services/vision/run" make test-integration
make generate
npm run test:e2e
```

Go tests use isolated temporary databases for 220 equal-timestamp comments across eleven pages, cursor scope/tamper/expiry checks, changed blocks and inactive authors between pages, known-ID reply denial, later-page pending/edit privacy, deleted-parent descendants and the reply depth boundary.

The browser journey uses real API-created and reviewed fixtures to load twenty-five comments, navigate reply context, use collapse controls by keyboard, preserve nested collapse choices, reload, delete a parent and check the 320 px layout. It injects an expired-page error at the browser boundary; actual expired-cursor rejection is checked in Go. Existing social, account, profile, civic report, OCR/vision and resumable-upload journeys remain in the suite.

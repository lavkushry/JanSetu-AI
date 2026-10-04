# Public profiles and block management

Residents can open an author's profile from a post or comment, discover people by name or handle, follow/unfollow them, and manage their own blocks in Account. The profile route uses the public profile UUID, so changing a handle does not break existing links. This remains the synthetic local application described in [implementation status](IMPLEMENTATION_STATUS.md).

## Try the flow

Sign in as Rohan and open Ananya's author link on a published post. The profile shows her chosen name, handle, bio, join date and published posts. Follow her to include eligible posts in Following; unfollowing removes that relationship. Search for `Ananya` or `@ananya` to open the same profile from the People results.

Choose **Block person**, review the confirmation, and confirm. The application opens **Account → Blocked people**. Her profile and social content become unavailable to Rohan, and Rohan's profile is also unavailable to Ananya. Search excludes the blocked profile. Account lists only blocks created by the current resident. **Unblock** requires confirmation; it removes that resident's block without restoring earlier follows. A remaining block in the other direction still prevents access.

The account page links to **View public profile**. Public profiles show only published content even to their author. Choosing Edit on a published post explicitly fetches its private owner view, preserving the latest pending revision in the composer instead of overwriting it with the published text.

## Contracts and privacy

The [OpenAPI 0.6.0 contract](../contracts/openapi/core.yaml) generates the web DTOs.

| Endpoint | Behavior |
| --- | --- |
| `GET /v1/profiles/{id}` | Active public profile with exactly `id`, `handle`, `displayName`, `bio`, `joinedAt`; viewer-specific `self` and `following` flags. Unknown, inactive and either-direction blocked profiles share a 404 response. |
| `GET /v1/profiles/{id}/posts` | Twenty approved published posts per keyset page. Current profile, block, community and publication eligibility are rechecked. Private edit candidates are always null, including on the author's own timeline. |
| `GET /v1/search?q=…` | Existing posts, communities and reviewed public progress plus up to twenty active, eligible people matched by chosen name/handle. A leading `@` is supported for the people portion. |
| `GET /v1/me/blocks` | Twenty of the current resident's own blocks per keyset page. No caller-supplied owner selector, incoming-block list or total counts. Inactive targets retain an opaque ID for removal and have a null public profile. |
| `PUT /v1/me/following/{id}` | Existing explicit `{enabled:true/false}` desired state. Adding a follow requires an active target with no block in either direction; removal works for inactive or absent targets. |
| `PUT /v1/me/blocks/{id}` | Existing explicit desired state. Adding a block requires an active other profile and removes follows in both directions. Removing an owned block works for an inactive or absent target and is safe to retry. |

Public profile DTOs contain no authentication principal, provider subject/email/picture, session metadata, staff grants, private reports, report aliases, evidence or OCR results. The chosen public biography is plain text. Profile discovery does not label a person as a verified authority based on their account grants. Search matches names and handles, without inferring interests or identities from private activity.

All relationship commands require the existing live-session, CSRF/origin and transaction checks. Public reads use the restricted social database pool. The private block list derives its owner from the authenticated actor and exposes only that owner's rows. This adds no identity, operational, vault or media database grants. Broader social-table RLS and production service separation remain part of the existing [database privacy gates](DATABASE_VAULT_ISOLATION.md).

Block changes clear visible and inactive feed/search/thread/profile/block-list query data before refetch, preventing a previously cached profile from being shown during the next permission check. The UI confirmation and account controls work with keyboard focus through the existing dialog. Current authorization is checked on each API read; this milestone does not add real-time revocation broadcasts across already open devices.

## Pagination and upgrade

Profile posts are ordered by original publication time and UUID, newest first. Blocks use block creation time and target UUID. Queries select at most twenty-one rows, return twenty, and sign the next keyset position with HMAC-SHA-256. The cursor binds its endpoint kind, viewer, target and five-minute deadline. Cross-owner, cross-profile, cross-endpoint, oversized, tampered and expired cursors are rejected. The deadline is preserved across pages; it is not extended by loading more.

Keyset pages have no two-hundred-row total cap. Newly published posts ahead of an existing page position appear on refresh, while edits retain the original publication order. Later pages apply current visibility and can omit removed/blocked content. These are live pages, not a frozen historical export. Cursor keys remain process-local, so API restart requires refreshing the first page. Persistent shared keys, search relevance/localization evaluation, capacity tests, follower directories/counts and public comment/activity histories remain pending.

[Migration 00011](../db/migrations/00011_profile_discovery.sql) adds author/publication and owner/block-time indexes. It changes no existing data, ownership mapping or privileges. A targeted upgrade is:

```bash
docker compose build migrate api web
docker compose run --rm --no-deps migrate
docker compose up -d --no-deps --wait api web
```

Both application and media volumes remain intact. No seed resets or schema rewrites are required.

## Verification

```bash
make check
JANSETU_VISION_BINARY="$PWD/services/vision/run" make test-integration
make generate
npm run test:e2e
```

Go checks cover the public field allowlist, pending post/edit exclusion for owners and visitors, bidirectional block denial, people-search suppression, desired-state follow/unfollow, removal of both follows, owner-only block lists, inactive-account removal, cursor integrity/deadline/scope, duplicate-free post/block pagination and publication removal between pages. Pagination fixtures live only in isolated temporary test databases.

Browser journeys use real Keycloak, API and PostgreSQL. They open author links, search for a handle, follow/unfollow and reload, cancel/confirm a block, verify the private settings and inverse denial, unblock without restored follows, and check 320 px layouts. A separate journey verifies publication review, absent pending content in owner/anonymous profiles, loading the latest private revision only for editing, and removal of a deleted post from the timeline. Existing account, social, service-report, OCR/vision and resumable-upload journeys still run. These checks do not establish production capacity or a complete accessibility audit.

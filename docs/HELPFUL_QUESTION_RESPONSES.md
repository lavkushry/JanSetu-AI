# Helpful responses to questions

Question authors can mark one approved comment as a **Helpful response**, replace it, or clear the choice. Active `MODERATOR` and `OWNER` members of that question's community have the same scoped ability. A platform moderation grant by itself does not confer this power. The label expresses usefulness, as specified by AC-45 in the [acceptance contracts](spec/TRACEABILITY_AND_DELIVERY.md); it does not verify the answer's factual accuracy or civic service outcome.

The thread presents a summary above the conversation and a badge beside the selected comment. The summary remains available when that comment is on a later page or within a collapsed branch. Feed cards indicate that a visible helpful response exists. Readers see who wrote the response and whether the choice came from the question author or a community moderator; the selecting moderator's identity is not included in the public DTO.

## API and revision contract

`PUT /v1/posts/{id}/selected-response` requires a current authenticated session, `X-JanSetu-CSRF: 1`, and `If-Match` on the **post version**. Send `{ "commentId": "<uuid>", "commentRevision": 1 }` to choose the displayed published reply revision, or `{ "commentId": null }` to clear. Omitting commentId or a non-null choice's positive commentRevision is rejected. The response is `{commentId, version}`, with the same version in `ETag`.

The target must be a published `QUESTION` in an active public/restricted community. Selection requires an approved, published comment in that same question, active question/comment authors, and current visibility to the selector. Either-direction blocks between the question and response authors prevent eligibility. Either-direction viewer blocks suppress that viewer's summary. Self-authored replies are permitted; the label records usefulness rather than independent fact verification.

A choice binds to the current **published question revision and published comment revision**. Pending edits preserve the approved helpful text. Approval of changed question or answer text suppresses the old choice until an authorized person explicitly selects a response again. The retained row never silently endorses a later revision. Deleted, hidden, inactive-author, blocked, private-community and frozen-community sources cannot produce a public helpful summary. Temporary block/profile-state restrictions can suppress a retained selection and restore it when visibility returns, provided both approved revision IDs still match. Deleted/hidden sources cannot be selected again.

The post read adds nullable `selectedResponse: {commentId, postRevision, commentRevision, body, author, selectedBy}` and `viewer.canSelectResponse`. The summary body comes from eligible published text at read time; there is no copied pending candidate or cached source snapshot. `selectedBy` is `AUTHOR` or `COMMUNITY_MODERATOR`. Existing comment pagination and chronology remain unchanged.

Selection, version increment and a body-free `HelpfulResponseChanged` outbox event commit together. An identical choice at the current version, or clearing an already-empty choice, is a no-op and emits no event. Replaying a stale post version or reply revision returns 412 even if the desired comment ID is unchanged. Comment approval changes the comment aggregate without changing the post version, so both preconditions are necessary to avoid selecting text the author has not seen. The UI resets/refetches the post and conversation after a conflict and requires another deliberate choice. Changed selections do not alter votes, comment counts, review state or case verification.

Error cases include 401 for an invalid session, 403 for missing community/author authority or CSRF failure, 404 for an inaccessible question, 412 for a stale post version or reply revision, 422 for missing/invalid input or an ineligible response, and 428 for a missing precondition. UUIDs belonging to another question, unpublished comments and removed comments are rejected.

## Persistence and upgrade

[Migration 00016](../db/migrations/00016_question_responses.sql) extends the canonical `social.selected_response` table with approved revision foreign keys. A security-invoker candidate view joins reviewed sources, active profiles and community visibility, and checks blocks between the two authors. A trigger rejects invalid source/revision combinations and stamps selection time. The original composite foreign key continues to enforce that a comment belongs to its question.

Forced row-level security permits eligible public reads and reserves writes for the question author or active moderator/owner of the same community. A write must identify the current selector and pass current eligibility/block checks. Authorized selectors can replace or clear retained rows after their old sources stop being eligible. Public DTO queries independently join current eligible revisions, including for the author. Restricted runtime grants prevent changing the post ID or forging selection timestamps. Session, profile and membership checks run inside the existing serialized pilot command boundary.

[Migration 00017](../db/migrations/00017_community_membership_scope.sql) protects the underlying community authority. Runtime membership inserts must belong to the current profile and use `MEMBER`; updates can change only that profile's active/left state and version. Provisioned moderator/owner roles survive join/leave. Runtime credentials cannot change roles, mutate another profile's membership, delete a membership/ban, or rejoin a banned member. Membership reads remain available for counts and existing community access checks. Privileged role provisioning remains outside the resident runtime.

Upgrade without resetting volumes:

```bash
docker compose build migrate api web
docker compose run --rm --no-deps migrate
docker compose up -d --no-deps api web
docker compose ps
```

Existing installations have no application-created choices before this release. If scaffold rows were manually created, the migration binds their existing published revision pointers; a row without published pointers stops the upgrade for investigation rather than deleting retained data. Reverting retained selections requires a reviewed forward migration. Existing worker contracts remain compatible and do not generate new notifications for selections.

## Verification and remaining boundaries

[Real PostgreSQL integration tests](../services/backend/internal/app/question_responses_integration_test.go) cover concurrent selection, stale/no-op requests, clearing, same-thread/publication checks, author and scoped moderator permissions, revoked community membership, runtime RLS, foreign-comment rejection, timestamp grants, pending versus approved edits, either-direction blocks, inactive authors, deletion, reasoned removal, feed hydration and deleted questions. [Browser coverage](../tests/e2e/core.spec.ts) exercises selection/replacement/clearing, nested branch collapse, refresh persistence, reader controls, edit review, concurrent-tab conflict recovery, retained descendant replies and 320-pixel light/dark layouts.

Community creation and role management, community-scoped publication/abuse review, appeals/restoration, selection notifications and public selection history remain pending. This feature does not enable private communities, production-scale locking, multilingual UI or the full accessibility acceptance gate. Community roles are administered through existing controlled fixture/provisioning processes until role management is implemented.

-- name: SessionActor :one
SELECT ip.id AS principal_id, ip.profile_id, ip.authorization_version, s.id AS session_id
FROM identity.session s JOIN identity.principal ip ON ip.id = s.principal_id
JOIN social.profile p ON p.id = ip.profile_id
WHERE s.token_hash = $1 AND s.auth_method = $2 AND s.revoked_at IS NULL AND s.expires_at > now()
  AND s.last_seen_at > now() - interval '30 minutes'
  AND (s.auth_method='demo' OR (s.provider=sqlc.arg(oidc_issuer) AND EXISTS(SELECT 1 FROM identity.account_binding b WHERE b.provider=s.provider AND b.provider_subject=s.provider_subject AND b.principal_id=ip.id AND b.state='ACTIVE')))
  AND ip.state = 'ACTIVE' AND p.state = 'ACTIVE';

-- name: Profile :one
SELECT id, handle::text AS handle, display_name, bio, state, version FROM social.profile WHERE id = $1;

-- name: PublicProfile :one
SELECT p.id,p.handle::text AS handle,p.display_name,p.bio,p.created_at,
 EXISTS(SELECT FROM social.profile_follow f WHERE f.follower_id=sqlc.arg(viewer_id) AND f.followed_id=p.id) AS following,
 EXISTS(SELECT FROM social.mute m WHERE m.profile_id=sqlc.arg(viewer_id) AND m.muted_profile_id=p.id AND (m.expires_at IS NULL OR m.expires_at>statement_timestamp())) AS muted
FROM social.profile p WHERE p.id=sqlc.arg(profile_id) AND p.state='ACTIVE'
 AND NOT EXISTS(SELECT FROM social.profile_block b WHERE
 (b.blocker_id=sqlc.arg(viewer_id) AND b.blocked_id=p.id) OR (b.blocked_id=sqlc.arg(viewer_id) AND b.blocker_id=p.id));

-- name: SearchProfiles :many
SELECT p.id FROM social.profile p WHERE p.state='ACTIVE'
 AND strpos(lower(p.handle::text||' '||p.display_name),lower(sqlc.arg(search_text)))>0
 AND NOT EXISTS(SELECT FROM social.profile_block b WHERE
 (b.blocker_id=sqlc.arg(viewer_id) AND b.blocked_id=p.id) OR (b.blocked_id=sqlc.arg(viewer_id) AND b.blocker_id=p.id))
ORDER BY p.handle,p.id LIMIT 20;

-- name: ProfilePostPage :many
SELECT p.id,p.published_at FROM social.post p
JOIN social.profile author ON author.id=p.author_id AND author.state='ACTIVE'
JOIN social.post_revision pub ON pub.post_id=p.id AND pub.revision=p.published_revision AND pub.review_state='APPROVED'
LEFT JOIN social.community c ON c.id=p.community_id
WHERE p.author_id=sqlc.arg(profile_id) AND p.state='PUBLISHED'
 AND (c.id IS NULL OR (c.state='ACTIVE' AND c.visibility IN ('PUBLIC','RESTRICTED')))
 AND (NOT sqlc.arg(has_cursor)::boolean OR (p.published_at,p.id)<(sqlc.arg(before_time)::timestamptz,sqlc.arg(before_id)::uuid))
 AND NOT EXISTS(SELECT FROM social.profile_block b WHERE
 (b.blocker_id=sqlc.arg(viewer_id) AND b.blocked_id=p.author_id) OR (b.blocked_id=sqlc.arg(viewer_id) AND b.blocker_id=p.author_id))
ORDER BY p.published_at DESC,p.id DESC LIMIT 21;

-- name: BlockedPeoplePage :many
SELECT b.blocked_id,b.created_at,p.state='ACTIVE' AS available,
 CASE WHEN p.state='ACTIVE' THEN p.handle::text ELSE '' END AS handle,
 CASE WHEN p.state='ACTIVE' THEN p.display_name ELSE '' END AS display_name
FROM social.profile_block b JOIN social.profile p ON p.id=b.blocked_id
WHERE b.blocker_id=sqlc.arg(viewer_id)
 AND (NOT sqlc.arg(has_cursor)::boolean OR (b.created_at,b.blocked_id)<(sqlc.arg(before_time)::timestamptz,sqlc.arg(before_id)::uuid))
ORDER BY b.created_at DESC,b.blocked_id DESC LIMIT 21;

-- name: PlatformRoles :many
SELECT role FROM identity.platform_grant WHERE principal_id = $1 AND revoked_at IS NULL AND valid_to > now();

-- name: AgencyGrants :many
SELECT g.agency_id, g.role, a.name FROM identity.organization_grant g JOIN ops.agency a ON a.id = g.agency_id
WHERE g.principal_id = $1 AND g.revoked_at IS NULL AND g.valid_from <= now() AND g.valid_to > now() AND a.state = 'ACTIVE';

-- name: LockPrincipal :one
SELECT id,state FROM identity.principal WHERE id = $1 FOR UPDATE;

-- name: LockProfiles :many
SELECT id,state FROM social.profile WHERE id = ANY($1::uuid[]) ORDER BY id FOR UPDATE;

-- name: Communities :many
SELECT c.id,c.slug::text AS slug,c.title,c.description,c.language_tag,c.visibility,c.state,c.rules_body,c.rules_revision,c.version,
  EXISTS(SELECT 1 FROM social.community_follow cf WHERE cf.community_id = c.id AND cf.profile_id = sqlc.arg(viewer_id)) AS following,
  COALESCE((SELECT cm.state FROM social.community_member cm WHERE cm.community_id = c.id AND cm.profile_id = sqlc.arg(viewer_id)),'LEFT') AS membership_state,
  EXISTS(SELECT FROM social.mute m WHERE m.profile_id=sqlc.arg(viewer_id) AND m.muted_community_id=c.id AND (m.expires_at IS NULL OR m.expires_at>statement_timestamp())) AS muted,
  (SELECT count(*) FROM social.community_member cm WHERE cm.community_id = c.id AND cm.state = 'ACTIVE') AS members
FROM social.community c WHERE c.visibility IN ('PUBLIC','RESTRICTED') ORDER BY c.created_at,c.id;

-- name: LockCommunity :one
SELECT id,state,visibility,rules_revision FROM social.community WHERE id = $1 FOR UPDATE;

-- name: Membership :one
SELECT role,state FROM social.community_member WHERE community_id = $1 AND profile_id = $2;

-- name: SetMembership :exec
INSERT INTO social.community_member(community_id,profile_id,role,state) VALUES ($1,$2,$3,$4)
ON CONFLICT(community_id,profile_id) DO UPDATE SET state = EXCLUDED.state, version = social.community_member.version + 1;

-- name: SetCommunityFollow :exec
INSERT INTO social.community_follow(profile_id,community_id) VALUES ($1,$2) ON CONFLICT DO NOTHING;
-- name: DeleteCommunityFollow :exec
DELETE FROM social.community_follow WHERE profile_id=$1 AND community_id=$2;

-- name: GetPost :one
SELECT jsonb_build_object(
  'id',p.id,'kind',p.kind,'state',p.state,'version',p.version,'currentRevision',p.current_revision,'publishedRevision',p.published_revision,
  'title',CASE WHEN p.state='DELETED' THEN NULL WHEN pub.post_id IS NOT NULL THEN pub.title ELSE cur.title END,
  'body',CASE WHEN p.state='DELETED' THEN NULL ELSE COALESCE(pub.body,'') END,
  'languageTag',COALESCE(pub.language_tag,cur.language_tag),'createdAt',p.created_at,'publishedAt',p.published_at,
  'author',CASE WHEN p.author_id IS NULL OR p.state='DELETED' THEN NULL ELSE jsonb_build_object('id',a.id,'handle',a.handle,'displayName',a.display_name) END,
  'community',CASE WHEN c.id IS NULL THEN NULL ELSE jsonb_build_object('id',c.id,'slug',c.slug,'title',c.title) END,
  'media','[]'::jsonb,
  'selectedResponse',(SELECT jsonb_build_object('commentId',e.comment_id,'postRevision',e.post_revision,'commentRevision',e.comment_revision,
    'body',e.body,'author',jsonb_build_object('id',e.author_id,'handle',e.handle,'displayName',e.display_name),
    'selectedBy',CASE WHEN s.selected_by=p.author_id THEN 'AUTHOR' ELSE 'COMMUNITY_MODERATOR' END)
    FROM social.selected_response s JOIN social.eligible_question_response e ON e.post_id=s.post_id AND e.comment_id=s.comment_id
    AND e.post_revision=s.post_revision AND e.comment_revision=s.comment_revision WHERE s.post_id=p.id
    AND NOT EXISTS(SELECT FROM social.profile_block b WHERE (b.blocker_id=sqlc.arg(viewer_id) AND b.blocked_id=e.author_id) OR (b.blocked_id=sqlc.arg(viewer_id) AND b.blocker_id=e.author_id))),
  'stats',jsonb_build_object('score',COALESCE(st.up_count-st.down_count,0),'comments',COALESCE(st.comment_count,0),'reposts',COALESCE(st.repost_count,0),'asOf',COALESCE(st.as_of,p.created_at)),
  'viewer',jsonb_build_object('vote',COALESCE((SELECT value FROM social.post_vote v WHERE v.profile_id=sqlc.arg(viewer_id) AND v.post_id=p.id),0),
    'bookmarked',EXISTS(SELECT 1 FROM social.bookmark b WHERE b.profile_id=sqlc.arg(viewer_id) AND b.post_id=p.id),
    'reposted',EXISTS(SELECT 1 FROM social.repost r WHERE r.profile_id=sqlc.arg(viewer_id) AND r.post_id=p.id),
    'canEdit',p.author_id=sqlc.arg(viewer_id) AND (p.state IN ('PENDING','PUBLISHED') OR (p.state='HIDDEN' AND p.published_revision IS NULL AND cur.review_state='REJECTED')),
    'canDelete',p.author_id=sqlc.arg(viewer_id) AND p.state NOT IN ('DELETED'),
    'canReply',p.state='PUBLISHED' AND sqlc.arg(viewer_id)::uuid <> '00000000-0000-0000-0000-000000000000'::uuid,
    'canSelectResponse',COALESCE(p.kind='QUESTION' AND p.state='PUBLISHED' AND (p.author_id=sqlc.arg(viewer_id)
      OR EXISTS(SELECT FROM social.community_member m WHERE m.community_id=p.community_id AND m.profile_id=sqlc.arg(viewer_id) AND m.state='ACTIVE' AND m.role IN ('MODERATOR','OWNER'))),false),
    'mutedAuthor',CASE WHEN p.state='DELETED' THEN false ELSE EXISTS(SELECT FROM social.mute m WHERE m.profile_id=sqlc.arg(viewer_id) AND m.muted_profile_id=p.author_id AND (m.expires_at IS NULL OR m.expires_at>statement_timestamp())) END),
  'candidate',CASE WHEN p.author_id=sqlc.arg(viewer_id) OR sqlc.arg(review_access)::boolean THEN
    jsonb_build_object('title',cur.title,'body',cur.body,'revision',cur.revision,'reviewState',cur.review_state) ELSE NULL END
) AS data
FROM social.post p JOIN social.post_revision cur ON cur.post_id=p.id AND cur.revision=p.current_revision
LEFT JOIN social.post_revision pub ON pub.post_id=p.id AND pub.revision=p.published_revision
LEFT JOIN social.profile a ON a.id=p.author_id LEFT JOIN social.community c ON c.id=p.community_id
LEFT JOIN social.post_stats st ON st.post_id=p.id
WHERE p.id=sqlc.arg(post_id) AND (c.id IS NULL OR c.visibility IN ('PUBLIC','RESTRICTED'))
AND (c.id IS NULL OR c.state='ACTIVE') AND (p.state='DELETED' OR p.author_id IS NULL OR a.state='ACTIVE')
AND (p.state IN ('PUBLISHED','DELETED') OR p.author_id=sqlc.arg(viewer_id) OR sqlc.arg(review_access)::boolean)
AND (sqlc.arg(review_access)::boolean OR NOT EXISTS(SELECT 1 FROM social.profile_block b WHERE
  (b.blocker_id=sqlc.arg(viewer_id) AND b.blocked_id=p.author_id) OR (b.blocked_id=sqlc.arg(viewer_id) AND b.blocker_id=p.author_id)));

-- name: FeedPostIDs :many
SELECT p.id FROM social.post p LEFT JOIN social.community c ON c.id=p.community_id
LEFT JOIN social.post_stats s ON s.post_id=p.id
WHERE p.state='PUBLISHED' AND (c.id IS NULL OR c.visibility IN ('PUBLIC','RESTRICTED'))
  AND (c.id IS NULL OR c.state='ACTIVE') AND (p.author_id IS NULL OR EXISTS(SELECT 1 FROM social.profile author WHERE author.id=p.author_id AND author.state='ACTIVE'))
  AND (sqlc.arg(community_id)::uuid='00000000-0000-0000-0000-000000000000'::uuid OR p.community_id=sqlc.arg(community_id))
  AND (NOT sqlc.arg(saved_only)::boolean OR EXISTS(SELECT 1 FROM social.bookmark bm WHERE bm.profile_id=sqlc.arg(viewer_id) AND bm.post_id=p.id))
  AND (sqlc.arg(saved_only)::boolean OR NOT EXISTS(SELECT FROM social.mute m WHERE m.profile_id=sqlc.arg(viewer_id) AND (m.muted_profile_id=p.author_id OR m.muted_community_id=p.community_id) AND (m.expires_at IS NULL OR m.expires_at>statement_timestamp())))
  AND (NOT sqlc.arg(following_only)::boolean OR EXISTS(SELECT 1 FROM social.community_follow cf WHERE cf.profile_id=sqlc.arg(viewer_id) AND cf.community_id=p.community_id)
    OR EXISTS(SELECT 1 FROM social.profile_follow pf WHERE pf.follower_id=sqlc.arg(viewer_id) AND pf.followed_id=p.author_id))
  AND (sqlc.arg(search_text)::text='' OR strpos(lower(COALESCE((SELECT body||' '||COALESCE(title,'') FROM social.post_revision WHERE post_id=p.id AND revision=p.published_revision),'')),lower(sqlc.arg(search_text)))>0)
  AND NOT EXISTS(SELECT 1 FROM social.profile_block b WHERE (b.blocker_id=sqlc.arg(viewer_id) AND b.blocked_id=p.author_id) OR (b.blocked_id=sqlc.arg(viewer_id) AND b.blocker_id=p.author_id))
ORDER BY CASE WHEN sqlc.arg(sort_top)::boolean THEN COALESCE(s.up_count-s.down_count,0) ELSE 0 END DESC,p.published_at DESC,p.id DESC LIMIT 200;

-- name: LockPost :one
SELECT * FROM social.post WHERE id=$1 FOR UPDATE;

-- name: GetSelectedResponse :one
SELECT * FROM social.selected_response WHERE post_id=$1;
-- name: QuestionResponseCandidate :one
SELECT e.post_id,e.post_revision,e.comment_id,e.comment_revision FROM social.eligible_question_response e
WHERE e.post_id=sqlc.arg(post_id) AND e.comment_id=sqlc.arg(comment_id)
AND NOT EXISTS(SELECT FROM social.profile_block b WHERE
(b.blocker_id=sqlc.arg(viewer_id) AND b.blocked_id IN(e.author_id,e.post_author_id)) OR (b.blocked_id=sqlc.arg(viewer_id) AND b.blocker_id IN(e.author_id,e.post_author_id)));
-- name: SetSelectedResponse :exec
INSERT INTO social.selected_response(post_id,comment_id,selected_by,post_revision,comment_revision) VALUES($1,$2,$3,$4,$5)
ON CONFLICT(post_id) DO UPDATE SET comment_id=EXCLUDED.comment_id,selected_by=EXCLUDED.selected_by,post_revision=EXCLUDED.post_revision,comment_revision=EXCLUDED.comment_revision;
-- name: ClearSelectedResponse :exec
DELETE FROM social.selected_response WHERE post_id=$1;
-- name: TouchQuestionResponse :exec
UPDATE social.post SET version=version+1,updated_at=now() WHERE id=$1;

-- name: InsertPost :exec
INSERT INTO social.post(id,author_id,community_id,kind,state) VALUES ($1,$2,$3,$4,$5);
-- name: InsertPostRevision :exec
INSERT INTO social.post_revision(post_id,revision,title,body,language_tag,review_state,editor_id) VALUES ($1,$2,$3,$4,$5,'PENDING',$6);
-- name: EditPost :one
UPDATE social.post SET current_revision=current_revision+1,version=version+1,updated_at=now(),state=CASE WHEN state='HIDDEN' THEN 'PENDING' ELSE state END
WHERE id=$1 AND version=$2 AND (state IN ('PENDING','PUBLISHED') OR (state='HIDDEN' AND published_revision IS NULL
 AND EXISTS(SELECT FROM social.post_revision r WHERE r.post_id=post.id AND r.revision=post.current_revision AND r.review_state='REJECTED'))) RETURNING current_revision,version;
-- name: DeletePost :exec
UPDATE social.post SET state='DELETED',version=version+1,updated_at=now() WHERE id=$1;
-- name: ApprovePostRevision :exec
UPDATE social.post_revision SET review_state='APPROVED' WHERE post_id=$1 AND revision=$2;
-- name: RejectPostRevision :exec
UPDATE social.post_revision SET review_state='REJECTED' WHERE post_id=$1 AND revision=$2;
-- name: PublishPost :exec
UPDATE social.post SET published_revision=current_revision,state='PUBLISHED',published_at=COALESCE(published_at,now()),updated_at=now(),version=version+1 WHERE id=$1;
-- name: HideUnpublishedPost :exec
UPDATE social.post SET state=CASE WHEN published_revision IS NULL THEN 'HIDDEN' ELSE state END,version=version+1 WHERE id=$1;

-- name: CommentPage :many
-- Recheck the thread's access in the same statement as its comment bodies,
-- including changes after the handler's initial post lookup.
WITH accessible_post AS (
 SELECT post.id FROM social.post post LEFT JOIN social.profile author ON author.id=post.author_id
 LEFT JOIN social.community community ON community.id=post.community_id
 WHERE post.id=sqlc.arg(post_id)
 AND (community.id IS NULL OR (community.state='ACTIVE' AND community.visibility IN ('PUBLIC','RESTRICTED')))
 AND (post.state='DELETED' OR post.author_id IS NULL OR author.state='ACTIVE')
 AND (post.state IN ('PUBLISHED','DELETED') OR post.author_id=sqlc.arg(viewer_id))
 AND NOT EXISTS(SELECT FROM social.profile_block b WHERE
 (b.blocker_id=sqlc.arg(viewer_id) AND b.blocked_id=post.author_id) OR (b.blocked_id=sqlc.arg(viewer_id) AND b.blocker_id=post.author_id))
)
SELECT c.id,c.created_at,jsonb_build_object('id',c.id,'postId',c.post_id,'parentId',c.parent_id,'depth',c.depth,'state',c.state,'version',c.version,
  'currentRevision',c.current_revision,'publishedVersion',c.published_version,'createdAt',c.created_at,
  'body',CASE WHEN c.state='PUBLISHED' THEN c.body ELSE NULL END,
  'author',CASE WHEN c.state='DELETED' THEN NULL ELSE jsonb_build_object('id',p.id,'handle',p.handle,'displayName',p.display_name) END,
  'viewer',jsonb_build_object('canEdit',c.author_id=sqlc.arg(viewer_id) AND (c.state IN ('PENDING','PUBLISHED') OR (c.state='HIDDEN' AND c.published_version IS NULL AND r.review_state='REJECTED')),'canDelete',c.author_id=sqlc.arg(viewer_id) AND c.state<>'DELETED'),
  'candidate',CASE WHEN c.author_id=sqlc.arg(viewer_id) AND c.state<>'DELETED' THEN jsonb_build_object('body',r.body,'reviewState',r.review_state) ELSE NULL END) AS data
FROM social.comment c JOIN accessible_post thread ON thread.id=c.post_id JOIN social.profile p ON p.id=c.author_id
JOIN social.comment_revision r ON r.comment_id=c.id AND r.version=c.current_revision
WHERE c.post_id=sqlc.arg(post_id) AND (c.state IN ('PUBLISHED','DELETED') OR c.author_id=sqlc.arg(viewer_id))
  AND (NOT sqlc.arg(has_cursor)::boolean OR (c.created_at,c.id)>(sqlc.arg(after_time)::timestamptz,sqlc.arg(after_id)::uuid))
  AND (c.state='DELETED' OR p.state='ACTIVE')
  AND NOT EXISTS(SELECT 1 FROM social.profile_block b WHERE (b.blocker_id=sqlc.arg(viewer_id) AND b.blocked_id=c.author_id) OR (b.blocked_id=sqlc.arg(viewer_id) AND b.blocker_id=c.author_id))
ORDER BY c.created_at,c.id LIMIT 21;
-- name: CommentReplyable :one
SELECT EXISTS(SELECT FROM social.comment c JOIN social.profile p ON p.id=c.author_id
 WHERE c.id=sqlc.arg(comment_id) AND c.post_id=sqlc.arg(post_id) AND c.state='PUBLISHED' AND p.state='ACTIVE'
 AND NOT EXISTS(SELECT FROM social.profile_block b WHERE
 (b.blocker_id=sqlc.arg(viewer_id) AND b.blocked_id=c.author_id) OR (b.blocked_id=sqlc.arg(viewer_id) AND b.blocker_id=c.author_id))) AS allowed;
-- name: LockComment :one
SELECT * FROM social.comment WHERE id=$1 FOR UPDATE;
-- name: InsertComment :exec
INSERT INTO social.comment(id,post_id,parent_id,author_id,body,depth,state) VALUES ($1,$2,$3,$4,$5,$6,'PENDING');
-- name: InsertCommentRevision :exec
INSERT INTO social.comment_revision(comment_id,version,body,language_tag,review_state) VALUES ($1,$2,$3,$4,'PENDING');
-- name: EditComment :one
UPDATE social.comment SET version=version+1,current_revision=current_revision+1,updated_at=now(),state=CASE WHEN state='HIDDEN' THEN 'PENDING' ELSE state END
WHERE comment.id=$1 AND comment.version=$2 AND (comment.state IN ('PENDING','PUBLISHED') OR (comment.state='HIDDEN' AND comment.published_version IS NULL
 AND EXISTS(SELECT FROM social.comment_revision r WHERE r.comment_id=comment.id AND r.version=comment.current_revision AND r.review_state='REJECTED'))) RETURNING version,current_revision;
-- name: DeleteComment :exec
UPDATE social.comment SET state='DELETED',version=version+1,updated_at=now() WHERE id=$1;
-- name: ApproveComment :exec
UPDATE social.comment_revision SET review_state='APPROVED' WHERE comment_id=$1 AND version=$2;
-- name: PublishComment :exec
UPDATE social.comment SET state='PUBLISHED',body=$2,published_version=current_revision,version=version+1,updated_at=now() WHERE id=$1;
-- name: RejectCommentRevision :exec
UPDATE social.comment_revision SET review_state='REJECTED' WHERE comment_id=$1 AND version=$2;
-- name: HideUnpublishedComment :exec
UPDATE social.comment SET state=CASE WHEN published_version IS NULL THEN 'HIDDEN' ELSE state END,version=version+1 WHERE id=$1;
-- name: CandidateComment :one
SELECT body FROM social.comment_revision WHERE comment_id=$1 AND version=$2;

-- name: SetVote :exec
INSERT INTO social.post_vote(profile_id,post_id,value) VALUES ($1,$2,$3) ON CONFLICT(profile_id,post_id) DO UPDATE SET value=EXCLUDED.value,updated_at=now();
-- name: DeleteVote :exec
DELETE FROM social.post_vote WHERE profile_id=$1 AND post_id=$2;
-- name: SetBookmark :exec
INSERT INTO social.bookmark(profile_id,post_id) VALUES ($1,$2) ON CONFLICT DO NOTHING;
-- name: DeleteBookmark :exec
DELETE FROM social.bookmark WHERE profile_id=$1 AND post_id=$2;
-- name: SetRepost :exec
INSERT INTO social.repost(profile_id,post_id) VALUES ($1,$2) ON CONFLICT DO NOTHING;
-- name: DeleteRepost :exec
DELETE FROM social.repost WHERE profile_id=$1 AND post_id=$2;
-- name: SetBlock :exec
INSERT INTO social.profile_block(blocker_id,blocked_id) VALUES ($1,$2) ON CONFLICT DO NOTHING;
-- name: DeleteBlock :exec
DELETE FROM social.profile_block WHERE blocker_id=$1 AND blocked_id=$2;
-- name: RemoveConflictingFollows :exec
DELETE FROM social.profile_follow WHERE (follower_id=$1 AND followed_id=$2) OR (follower_id=$2 AND followed_id=$1);
-- name: SetProfileFollow :exec
INSERT INTO social.profile_follow(follower_id,followed_id) VALUES ($1,$2) ON CONFLICT DO NOTHING;
-- name: DeleteProfileFollow :exec
DELETE FROM social.profile_follow WHERE follower_id=$1 AND followed_id=$2;
-- name: HasBlock :one
SELECT EXISTS(SELECT 1 FROM social.profile_block WHERE (blocker_id=$1 AND blocked_id=$2) OR (blocker_id=$2 AND blocked_id=$1));

-- name: InsertModeration :exec
INSERT INTO social.moderation_case(id,post_id,comment_id,target_version,reason_code,grounds,state) VALUES ($1,$2,$3,$4,'PUBLICATION_REVIEW','Review the submitted revision','OPEN');
-- name: ModerationQueue :many
SELECT m.* FROM social.moderation_case m WHERE m.state IN ('OPEN','REVIEWING') AND m.reason_code='PUBLICATION_REVIEW' AND m.reporter_ref IS NULL
AND (EXISTS(SELECT 1 FROM social.post p WHERE p.id=m.post_id AND p.state IN ('PENDING','PUBLISHED') AND p.current_revision=m.target_version)
OR EXISTS(SELECT 1 FROM social.comment c JOIN social.post p ON p.id=c.post_id WHERE c.id=m.comment_id
AND c.state IN ('PENDING','PUBLISHED') AND c.current_revision=m.target_version AND p.state='PUBLISHED'))
ORDER BY m.created_at,m.id LIMIT 100;
-- name: LockModeration :one
SELECT * FROM social.moderation_case WHERE id=$1 FOR UPDATE;
-- name: FinishModeration :exec
UPDATE social.moderation_case SET state='DECIDED',version=version+1 WHERE id=$1;
-- name: InsertModerationDecision :exec
INSERT INTO social.moderation_decision(id,moderation_case_id,sequence,action,rule_version,actor_ref,reason,author_reason) VALUES ($1,$2,1,$3,'local-community-v1',$4,$5,$6);

-- name: LockIdempotency :exec
SELECT pg_advisory_xact_lock($1::bigint);
-- name: GetIdempotency :one
SELECT * FROM infra.idempotency_record WHERE principal_ref=$1 AND operation=$2 AND idempotency_key=$3 AND expires_at>now();
-- name: InsertIdempotency :exec
INSERT INTO infra.idempotency_record(principal_ref,operation,idempotency_key,request_hash,result_resource_id,response_code,response_body,expires_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,now()+interval '72 hours') ON CONFLICT(principal_ref,operation,idempotency_key)
DO UPDATE SET request_hash=EXCLUDED.request_hash,result_resource_id=EXCLUDED.result_resource_id,response_code=EXCLUDED.response_code,response_body=EXCLUDED.response_body,expires_at=EXCLUDED.expires_at;
-- name: AddEvent :exec
INSERT INTO infra.outbox(id,aggregate_type,aggregate_id,aggregate_version,event_type,payload_version,payload) VALUES ($1,$2,$3,$4,$5,1,$6);

-- name: Receipts :many
SELECT * FROM social.case_receipt WHERE publication_state='PUBLISHED'
AND (sqlc.arg(search_text)::text='' OR strpos(lower(title||' '||safe_summary),lower(sqlc.arg(search_text)))>0)
ORDER BY urgency_tier DESC,first_reported_at,id LIMIT 200;
-- name: Receipt :one
SELECT * FROM social.case_receipt WHERE id=$1 AND publication_state='PUBLISHED';
-- name: ReceiptEvents :many
SELECT * FROM social.case_receipt_event WHERE receipt_id=$1 ORDER BY sequence;
-- name: CaseFollowing :one
SELECT EXISTS(SELECT 1 FROM social.case_follow WHERE profile_id=$1 AND receipt_id=$2);
-- name: SetCaseFollow :exec
INSERT INTO social.case_follow(profile_id,receipt_id) VALUES ($1,$2) ON CONFLICT DO NOTHING;
-- name: DeleteCaseFollow :exec
DELETE FROM social.case_follow WHERE profile_id=$1 AND receipt_id=$2;

-- name: OwnReportProgress :many
SELECT r.id,r.received_at,r.statement,r.language_tag,ir.state AS linkage,ir.case_id,pb.receipt_id
FROM ops.report r JOIN ops.intake_review ir ON ir.report_id=r.id
LEFT JOIN ops.publication_binding pb ON pb.case_id=ir.case_id
WHERE r.reporter_ref=ANY(sqlc.arg(alias_ids)::uuid[])
AND (sqlc.arg(report_id)::uuid='00000000-0000-0000-0000-000000000000'::uuid OR r.id=sqlc.arg(report_id))
ORDER BY r.received_at DESC LIMIT 100;
-- name: ReportBySubmission :one
SELECT * FROM ops.report WHERE client_submission_id=$1;
-- name: InsertReport :exec
INSERT INTO ops.report(id,client_submission_id,reporter_ref,source_channel,language_tag,statement,observed_at,classification,publication_preference,intake_metadata,retention_policy_id,request_hash)
VALUES ($1,$2,$3,'WEB',$4,$5,$6,'PUBLIC_SERVICE',$7,$8,'local-demo-v1',$9);
-- name: InsertIntakeReview :exec
INSERT INTO ops.intake_review(report_id) VALUES ($1);
-- name: IntakeQueue :many
SELECT r.id,r.statement,r.language_tag,r.publication_preference,r.intake_metadata,r.received_at,ir.version
FROM ops.report r JOIN ops.intake_review ir ON ir.report_id=r.id WHERE ir.state='PENDING' ORDER BY r.received_at LIMIT 100;
-- name: LockReport :one
SELECT * FROM ops.report WHERE id=$1 FOR UPDATE;
-- name: LockIntake :one
SELECT * FROM ops.intake_review WHERE report_id=$1 FOR UPDATE;
-- name: LinkIntake :exec
UPDATE ops.intake_review SET case_id=$2,state='LINKED',version=version+1,reviewed_by=$3,reviewed_at=now() WHERE report_id=$1;
-- name: AgencyDirectory :many
SELECT id,name FROM ops.agency WHERE state='ACTIVE' ORDER BY name;
-- name: InsertCase :exec
INSERT INTO ops.case_record(id,category_code,state,first_valid_report_at,urgency_tier) VALUES ($1,$2,'OPEN',$3,$4);
-- name: InsertObservation :exec
INSERT INTO ops.case_observation(case_id,report_id,relation) VALUES ($1,$2,'INITIAL');
-- name: InsertObligation :exec
INSERT INTO ops.obligation(id,case_id,agency_id,obligation_type,state,authority_basis_ref) VALUES ($1,$2,$3,'RESTORATION','PROPOSED','synthetic-local-mandate-v1');
-- name: AssignCoordinator :exec
INSERT INTO ops.coordinator_assignment(case_id,principal_id,roster_version,assigned_at) VALUES ($1,$2,'local-roster-v1',now());
-- name: Cases :many
SELECT c.*,COALESCE((SELECT title FROM social.case_receipt r JOIN ops.publication_binding b ON b.receipt_id=r.id WHERE b.case_id=c.id),'Service issue awaiting publication') AS title,
 (SELECT min(o.agency_id::text) FROM ops.obligation o WHERE o.case_id=c.id) AS agency_id
FROM ops.case_record c WHERE sqlc.arg(all_cases)::boolean OR EXISTS(SELECT 1 FROM ops.obligation o WHERE o.case_id=c.id AND o.agency_id=ANY(sqlc.arg(agency_ids)::uuid[]))
ORDER BY c.urgency_tier DESC,c.first_valid_report_at LIMIT 100;
-- name: LockCase :one
SELECT * FROM ops.case_record WHERE id=$1 FOR UPDATE;
-- name: CaseObligations :many
SELECT o.*,a.name AS agency_name FROM ops.obligation o LEFT JOIN ops.agency a ON a.id=o.agency_id WHERE o.case_id=$1 ORDER BY o.id;
-- name: LockObligation :one
SELECT * FROM ops.obligation WHERE id=$1 FOR UPDATE;
-- name: ChangeObligation :exec
UPDATE ops.obligation SET state=$2,version=version+1,work_summary=$3,
 accepted_at=CASE WHEN $2='ACCEPTED' THEN now() ELSE accepted_at END,
 completed_at=CASE WHEN $2='COMPLETION_CLAIMED' THEN now() ELSE completed_at END,
 completion_actor_ref=CASE WHEN $2='COMPLETION_CLAIMED' THEN $4 ELSE completion_actor_ref END WHERE id=$1;
-- name: ChangeCase :exec
UPDATE ops.case_record SET state=$2,version=version+1,updated_at=now() WHERE id=$1;
-- name: InsertCaseEvent :exec
INSERT INTO ops.case_event(id,case_id,sequence,event_type,actor_ref,actor_role,payload,occurred_at)
VALUES ($1,$2,COALESCE((SELECT max(sequence)+1 FROM ops.case_event WHERE case_id=$2),1),$3,$4,$5,$6,now());
-- name: CaseEvents :many
SELECT * FROM ops.case_event WHERE case_id=$1 ORDER BY sequence;
-- name: CaseCanResolve :one
SELECT NOT EXISTS(SELECT 1 FROM ops.obligation WHERE case_id=$1 AND required_for_restoration AND state NOT IN ('VERIFIED','CANCELLED'));
-- name: InsertVerification :exec
INSERT INTO ops.verification_decision(id,case_id,obligation_id,reviewer_ref,result,reason,decided_at) VALUES ($1,$2,$3,$4,$5,$6,now());
-- name: PublicationBinding :one
SELECT * FROM ops.publication_binding WHERE case_id=$1;
-- name: PublicationAllowed :one
SELECT NOT EXISTS(SELECT 1 FROM ops.case_observation o JOIN ops.report r ON r.id=o.report_id WHERE o.case_id=$1 AND r.publication_preference='PRIVATE');
-- name: SavePublicationBinding :exec
INSERT INTO ops.publication_binding(case_id,receipt_id,approved_case_version,reviewer_ref,decision_ref,approved_at) VALUES ($1,$2,$3,$4,$5,now())
ON CONFLICT(case_id) DO UPDATE SET approved_case_version=EXCLUDED.approved_case_version,reviewer_ref=EXCLUDED.reviewer_ref,decision_ref=EXCLUDED.decision_ref,approved_at=now();
-- name: SaveReceipt :exec
INSERT INTO social.case_receipt(id,title,safe_summary,area_label,public_state,urgency_tier,first_reported_at,responsibilities,projection_version,publication_state,published_at,updated_at,policy_version)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,'PUBLISHED',now(),now(),'local-publication-v1')
ON CONFLICT(id) DO UPDATE SET title=EXCLUDED.title,safe_summary=EXCLUDED.safe_summary,area_label=EXCLUDED.area_label,public_state=EXCLUDED.public_state,urgency_tier=EXCLUDED.urgency_tier,responsibilities=EXCLUDED.responsibilities,projection_version=EXCLUDED.projection_version,publication_state='PUBLISHED',updated_at=now();
-- name: DeleteReceiptEvents :exec
DELETE FROM social.case_receipt_event WHERE receipt_id=$1;
-- name: InsertReceiptEvent :exec
INSERT INTO social.case_receipt_event(receipt_id,sequence,type,safe_text,actor_type,occurred_at,projection_version) VALUES ($1,$2,$3,$4,$5,$6,$7);
-- name: InsertPublicationDecision :exec
INSERT INTO ops.publication_decision(id,case_id,case_version,action,safe_payload,reviewer_ref,policy_version,decided_at) VALUES ($1,$2,$3,'PUBLISH',$4,$5,'local-publication-v1',now());

-- name: ClaimEvent :one
WITH candidate AS (
 SELECT id FROM infra.outbox WHERE delivered_at IS NULL AND dead_lettered_at IS NULL AND available_at<=now()
 AND (lease_until IS NULL OR lease_until<now()) ORDER BY created_at,id FOR UPDATE SKIP LOCKED LIMIT 1
)
UPDATE infra.outbox SET lease_owner=$1,lease_token=$2,lease_until=now()+interval '60 seconds',attempts=attempts+1
WHERE id IN (SELECT id FROM candidate) RETURNING *;
-- name: LockClaim :one
SELECT * FROM infra.outbox WHERE id=$1 AND lease_token=$2 AND lease_owner=$3 AND lease_until>now() FOR UPDATE;
-- name: EventProcessed :one
INSERT INTO infra.processed_event(consumer_name,event_id) VALUES ('core-projector',$1) ON CONFLICT DO NOTHING RETURNING event_id;
-- name: CompleteEvent :exec
UPDATE infra.outbox SET delivered_at=now(),lease_until=NULL WHERE id=$1 AND lease_token=$2 AND lease_owner=$3;
-- name: RetryEvent :exec
UPDATE infra.outbox SET lease_until=NULL,available_at=now()+interval '5 seconds',last_error_code='PROJECTION_FAILED',dead_lettered_at=CASE WHEN attempts>=8 THEN now() ELSE NULL END WHERE id=$1 AND lease_token=$2;
-- name: RebuildPostStats :exec
INSERT INTO social.post_stats(post_id,up_count,down_count,comment_count,repost_count,as_of)
SELECT $1,(SELECT count(*) FROM social.post_vote WHERE post_id=$1 AND value=1),(SELECT count(*) FROM social.post_vote WHERE post_id=$1 AND value=-1),
(SELECT count(*) FROM social.comment WHERE post_id=$1 AND state='PUBLISHED'),(SELECT count(*) FROM social.repost WHERE post_id=$1),now()
ON CONFLICT(post_id) DO UPDATE SET up_count=EXCLUDED.up_count,down_count=EXCLUDED.down_count,comment_count=EXCLUDED.comment_count,repost_count=EXCLUDED.repost_count,as_of=now();

-- name: AttachReportMedia :exec
INSERT INTO ops.report_media(report_id,media_id) VALUES($1,$2);

-- name: ReportMediaIDs :one
SELECT authz.report_media_ids($1)::uuid[] AS media_ids;

-- name: ReportOCRRegion :one
SELECT coalesce(authz.report_ocr_region($1,$2,$3),'')::text AS original_text;

-- name: ReportMediaAttachable :one
SELECT authz.media_attachable($1,$2)::boolean AS allowed;

-- name: DeliverReplyActivity :exec
INSERT INTO social.notification(id,recipient_id,event_id,channel,post_id,comment_id,kind,source_version,state,created_at)
SELECT gen_random_uuid(),recipient_id,sqlc.arg(event_id),'IN_APP',post_id,comment_id,'REPLY',sqlc.arg(source_version),'SENT',sqlc.arg(event_time)
FROM social.activity_reply_target target WHERE target.comment_id=sqlc.arg(comment_id) AND target.source_version>=sqlc.arg(source_version)
ON CONFLICT DO NOTHING;

-- name: DeliverCaseActivity :exec
INSERT INTO social.notification(id,recipient_id,event_id,channel,receipt_id,kind,source_version,state,created_at)
SELECT gen_random_uuid(),recipient_id,sqlc.arg(event_id),'IN_APP',receipt_id,'CASE_PROGRESS',sqlc.arg(source_version),'SENT',sqlc.arg(event_time)
FROM social.activity_case_target target WHERE target.receipt_id=sqlc.arg(receipt_id) AND target.projection_version=sqlc.arg(source_version) AND target.followed_at<=sqlc.arg(event_time)
ON CONFLICT DO NOTHING;

-- name: ActivityPage :many
SELECT * FROM social.activity_visible WHERE recipient_id=sqlc.arg(viewer_id)
AND (sqlc.arg(filter)::text='ALL' OR (sqlc.arg(filter)='SOCIAL' AND kind='REPLY') OR (sqlc.arg(filter)='CASES' AND kind='CASE_PROGRESS'))
AND (NOT sqlc.arg(has_cursor)::boolean OR (created_at,id)<(sqlc.arg(before_time)::timestamptz,sqlc.arg(before_id)::uuid))
ORDER BY created_at DESC,id DESC LIMIT 21;

-- name: ActivityUnread :one
SELECT count(*) FROM social.activity_visible WHERE recipient_id=sqlc.arg(viewer_id) AND read_at IS NULL;

-- name: SetActivityRead :one
UPDATE social.notification n SET read_at=CASE WHEN sqlc.arg(read)::boolean THEN COALESCE(n.read_at,now()) ELSE NULL END
WHERE n.id=sqlc.arg(id) AND n.recipient_id=sqlc.arg(viewer_id)
AND EXISTS(SELECT FROM social.activity_visible visible WHERE visible.id=n.id AND visible.recipient_id=n.recipient_id)
RETURNING n.id,n.read_at;

-- name: NotificationPreference :one
SELECT COALESCE('IN_APP'=ANY(pref.notification_channels),true)::boolean AS in_app,COALESCE(pref.version,1)::bigint AS version
FROM (SELECT sqlc.arg(viewer_id)::uuid AS id) viewer LEFT JOIN social.feed_preference pref ON pref.profile_id=viewer.id;
-- name: EnsureNotificationPreference :exec
INSERT INTO social.feed_preference(profile_id,policy_version) VALUES($1,'local-notifications-v1') ON CONFLICT DO NOTHING;
-- name: LockNotificationPreference :one
SELECT 'IN_APP'=ANY(notification_channels) AS in_app,version FROM social.feed_preference WHERE profile_id=$1 FOR UPDATE;
-- name: SaveNotificationPreference :one
UPDATE social.feed_preference SET notification_channels=CASE WHEN sqlc.arg(in_app)::boolean THEN array_append(array_remove(notification_channels,'IN_APP'),'IN_APP') ELSE array_remove(notification_channels,'IN_APP') END,
 policy_version='local-notifications-v1',version=version+1 WHERE profile_id=sqlc.arg(viewer_id)
RETURNING 'IN_APP'=ANY(notification_channels) AS in_app,version;

-- name: SetProfileMute :exec
INSERT INTO social.mute(id,profile_id,muted_profile_id,expires_at) VALUES($1,$2,$3,$4)
ON CONFLICT(profile_id,muted_profile_id) DO UPDATE SET expires_at=EXCLUDED.expires_at,
 created_at=CASE WHEN social.mute.expires_at IS NOT DISTINCT FROM EXCLUDED.expires_at THEN social.mute.created_at ELSE statement_timestamp() END;
-- name: SetCommunityMute :exec
INSERT INTO social.mute(id,profile_id,muted_community_id,expires_at) VALUES($1,$2,$3,$4)
ON CONFLICT(profile_id,muted_community_id) DO UPDATE SET expires_at=EXCLUDED.expires_at,
 created_at=CASE WHEN social.mute.expires_at IS NOT DISTINCT FROM EXCLUDED.expires_at THEN social.mute.created_at ELSE statement_timestamp() END;
-- name: DeleteMute :exec
DELETE FROM social.mute WHERE profile_id=sqlc.arg(viewer_id) AND
 ((sqlc.arg(target_type)::text='PROFILE' AND muted_profile_id=sqlc.arg(target_id)::uuid) OR
 (sqlc.arg(target_type)='COMMUNITY' AND muted_community_id=sqlc.arg(target_id)));
-- name: PostMuted :one
SELECT EXISTS(SELECT FROM social.post p JOIN social.mute m ON m.profile_id=sqlc.arg(viewer_id)
 AND (m.muted_profile_id=p.author_id OR m.muted_community_id=p.community_id)
 WHERE p.id=sqlc.arg(post_id) AND (m.expires_at IS NULL OR m.expires_at>statement_timestamp()));
-- name: MutePage :many
SELECT m.id,m.muted_profile_id,m.muted_community_id,m.created_at,m.expires_at,
 (m.expires_at IS NULL OR m.expires_at>statement_timestamp()) AS active,
 CASE WHEN p.state='ACTIVE' AND NOT EXISTS(SELECT FROM social.profile_block b WHERE
 (b.blocker_id=m.profile_id AND b.blocked_id=p.id) OR (b.blocked_id=m.profile_id AND b.blocker_id=p.id)) THEN p.display_name ELSE '' END::text AS display_name,
 CASE WHEN p.state='ACTIVE' AND NOT EXISTS(SELECT FROM social.profile_block b WHERE
 (b.blocker_id=m.profile_id AND b.blocked_id=p.id) OR (b.blocked_id=m.profile_id AND b.blocker_id=p.id)) THEN p.handle::text ELSE '' END::text AS handle,
 CASE WHEN c.state='ACTIVE' AND c.visibility IN ('PUBLIC','RESTRICTED') THEN c.title ELSE '' END::text AS community_title,
 CASE WHEN c.state='ACTIVE' AND c.visibility IN ('PUBLIC','RESTRICTED') THEN c.slug::text ELSE '' END::text AS community_slug
FROM social.mute m LEFT JOIN social.profile p ON p.id=m.muted_profile_id LEFT JOIN social.community c ON c.id=m.muted_community_id
WHERE m.profile_id=sqlc.arg(viewer_id)
 AND (NOT sqlc.arg(has_cursor)::boolean OR (m.created_at,m.id)<(sqlc.arg(before_time)::timestamptz,sqlc.arg(before_id)::uuid))
ORDER BY m.created_at DESC,m.id DESC LIMIT 21;

-- name: ContentReportTarget :one
SELECT target.* FROM social.content_report_target target
WHERE target.target_type=sqlc.arg(target_type) AND target.target_id=sqlc.arg(target_id)
AND (sqlc.arg(review_access)::boolean OR NOT EXISTS(SELECT FROM social.profile_block b WHERE
 (b.blocker_id=sqlc.arg(viewer_id) AND b.blocked_id IN (target.author_id,target.post_author_id)) OR
 (b.blocked_id=sqlc.arg(viewer_id) AND b.blocker_id IN (target.author_id,target.post_author_id))));
-- name: ExistingContentReport :one
SELECT * FROM social.moderation_case WHERE reporter_ref=sqlc.arg(reporter_ref)
AND (post_id=sqlc.narg(post_id)::uuid OR comment_id=sqlc.narg(comment_id)::uuid) AND target_version=sqlc.arg(target_version);
-- name: ContentReportCount :one
SELECT count(*) FROM social.moderation_case WHERE reporter_ref=$1 AND created_at>statement_timestamp()-interval '1 hour';
-- name: InsertContentReport :one
INSERT INTO social.moderation_case(id,post_id,comment_id,target_version,reporter_ref,reason_code,grounds,state)
VALUES($1,$2,$3,$4,$5,$6,$7,'OPEN') RETURNING *;
-- name: OwnedContentReport :one
SELECT * FROM social.moderation_case WHERE id=$1 AND reporter_ref=$2;
-- name: OwnContentReportPage :many
SELECT * FROM social.moderation_case WHERE reporter_ref=sqlc.arg(reporter_ref)
AND (NOT sqlc.arg(has_cursor)::boolean OR (created_at,id)<(sqlc.arg(before_time)::timestamptz,sqlc.arg(before_id)::uuid))
ORDER BY created_at DESC,id DESC LIMIT 21;
-- name: ContentReportQueue :many
SELECT m.* FROM social.moderation_case m LEFT JOIN social.post p ON p.id=m.post_id LEFT JOIN social.comment c ON c.id=m.comment_id
WHERE m.reporter_ref IS NOT NULL AND m.state='OPEN' AND COALESCE(c.author_id,p.author_id) IS DISTINCT FROM sqlc.arg(viewer_id)
AND m.reporter_ref<>sqlc.arg(reviewer_principal_id)::uuid
AND (NOT sqlc.arg(has_cursor)::boolean OR (m.created_at,m.id)>(sqlc.arg(after_time)::timestamptz,sqlc.arg(after_id)::uuid))
ORDER BY m.created_at,m.id LIMIT 21;
-- name: ContentReportDecision :one
SELECT action,reason,decided_at FROM social.moderation_decision WHERE moderation_case_id=$1 AND rule_version='local-content-report-v1' ORDER BY sequence DESC LIMIT 1;
-- name: InsertContentReportDecision :exec
INSERT INTO social.moderation_decision(id,moderation_case_id,sequence,action,rule_version,actor_ref,reason,author_reason)
VALUES($1,$2,1,$3,'local-content-report-v1',$4,$5,$6);
-- name: RemoveReportedPost :exec
UPDATE social.post SET state='HIDDEN',version=version+1,updated_at=now() WHERE id=$1;
-- name: RemoveReportedComment :exec
UPDATE social.comment SET state='HIDDEN',version=version+1,updated_at=now() WHERE id=$1;

-- name: AuthorModerationDecision :one
SELECT * FROM social.author_moderation_decision WHERE id=$1;
-- name: AuthorModerationDecisionPage :many
SELECT * FROM social.author_moderation_decision
WHERE NOT sqlc.arg(has_cursor)::boolean OR (decided_at,id)<(sqlc.arg(before_time)::timestamptz,sqlc.arg(before_id)::uuid)
ORDER BY decided_at DESC,id DESC LIMIT 21;

-- name: InsertAppeal :one
INSERT INTO social.appeal(id,decision_id,appellant_ref,grounds,state) VALUES($1,$2,$3,$4,'OPEN') RETURNING *;
-- name: OwnedAppeal :one
SELECT * FROM social.appeal WHERE id=$1 AND appellant_ref=$2;
-- name: OwnedAppealForDecision :one
SELECT * FROM social.appeal WHERE decision_id=$1 AND appellant_ref=$2;
-- name: OwnAppealPage :many
SELECT * FROM social.appeal WHERE appellant_ref=sqlc.arg(appellant_ref)
AND (NOT sqlc.arg(has_cursor)::boolean OR (created_at,id)<(sqlc.arg(before_time)::timestamptz,sqlc.arg(before_id)::uuid))
ORDER BY created_at DESC,id DESC LIMIT 21;
-- name: AppealCount :one
SELECT count(*) FROM social.appeal WHERE appellant_ref=$1 AND created_at>statement_timestamp()-interval '1 hour';
-- name: AppealQueue :many
SELECT * FROM social.appeal WHERE state IN ('OPEN','REVIEWING') AND appellant_ref<>authz.principal()
AND (reviewer_ref IS NULL OR reviewer_ref=authz.principal())
AND (NOT sqlc.arg(has_cursor)::boolean OR (created_at,id)>(sqlc.arg(after_time)::timestamptz,sqlc.arg(after_id)::uuid))
ORDER BY created_at,id LIMIT 21;
-- name: ReviewerAppeal :one
SELECT * FROM social.appeal WHERE id=$1 AND appellant_ref<>authz.principal()
AND (reviewer_ref IS NULL OR reviewer_ref=authz.principal());
-- name: LockAppeal :one
SELECT * FROM social.appeal WHERE id=$1 FOR UPDATE;
-- name: ClaimAppeal :exec
UPDATE social.appeal SET state='REVIEWING',reviewer_ref=$2,version=version+1 WHERE id=$1;
-- name: FinishAppeal :exec
UPDATE social.appeal SET state=$2,version=version+1 WHERE id=$1;
-- name: AppealDecision :one
SELECT * FROM social.appeal_decision WHERE appeal_id=$1;
-- name: InsertAppealDecision :exec
INSERT INTO social.appeal_decision(id,appeal_id,result,author_reason,restoration_state,restoration_reason,reviewer_ref) VALUES($1,$2,$3,$4,$5,$6,$7);
-- name: AppealOriginal :one
SELECT d.id,d.action,d.rule_version,d.actor_ref,d.reason AS internal_reason,m.post_id,m.comment_id,m.target_version,m.reporter_ref
FROM social.moderation_decision d JOIN social.moderation_case m ON m.id=d.moderation_case_id
JOIN social.appeal appeal ON appeal.decision_id=d.id
WHERE d.id=$1 AND authz.has_role('PLATFORM_MODERATOR') AND appeal.appellant_ref<>authz.principal()
AND (appeal.reviewer_ref IS NULL OR appeal.reviewer_ref=authz.principal());
-- name: AppealSourceStatus :one
SELECT p.id AS target_id,p.id AS post_id,p.version,p.current_revision::bigint AS current_revision,coalesce(p.published_revision,0)::bigint AS published_revision,
p.state,p.author_id,p.community_id,NULL::uuid AS parent_id,r.review_state
FROM social.post p JOIN social.post_revision r ON r.post_id=p.id AND r.revision=sqlc.arg(target_revision)::bigint
WHERE p.id=sqlc.arg(post_id)::uuid
UNION ALL
SELECT c.id,c.post_id,c.version,c.current_revision,coalesce(c.published_version,0)::bigint,c.state,c.author_id,p.community_id,c.parent_id,r.review_state
FROM social.comment c JOIN social.post p ON p.id=c.post_id JOIN social.comment_revision r ON r.comment_id=c.id AND r.version=sqlc.arg(target_revision)::bigint
WHERE c.id=sqlc.arg(comment_id)::uuid;
-- name: AppealReviewPreview :one
WITH eligible AS (SELECT FROM social.appeal WHERE decision_id=sqlc.arg(decision_id)::uuid
AND authz.has_role('PLATFORM_MODERATOR') AND appellant_ref<>authz.principal()
AND (reviewer_ref IS NULL OR reviewer_ref=authz.principal()))
SELECT p.id AS post_id,r.title,r.body FROM social.post p JOIN social.profile a ON a.id=p.author_id AND a.state='ACTIVE'
JOIN social.post_revision r ON r.post_id=p.id AND r.revision=sqlc.arg(target_revision)::bigint
LEFT JOIN social.community community ON community.id=p.community_id
WHERE p.id=sqlc.arg(post_id)::uuid AND EXISTS(SELECT FROM eligible) AND p.state IN ('HIDDEN','PUBLISHED')
AND (community.id IS NULL OR (community.state='ACTIVE' AND community.visibility IN ('PUBLIC','RESTRICTED')))
UNION ALL
SELECT p.id,NULL::text,candidate.body FROM social.comment c JOIN social.post p ON p.id=c.post_id AND p.state='PUBLISHED'
JOIN social.profile a ON a.id=c.author_id AND a.state='ACTIVE'
JOIN social.profile pa ON pa.id=p.author_id AND pa.state='ACTIVE'
JOIN social.comment_revision candidate ON candidate.comment_id=c.id AND candidate.version=sqlc.arg(target_revision)::bigint
LEFT JOIN social.community community ON community.id=p.community_id
WHERE c.id=sqlc.arg(comment_id)::uuid AND EXISTS(SELECT FROM eligible) AND c.state IN ('HIDDEN','PUBLISHED')
AND (community.id IS NULL OR (community.state='ACTIVE' AND community.visibility IN ('PUBLIC','RESTRICTED')));
-- name: ThreadReplyable :one
SELECT EXISTS(SELECT FROM social.post p JOIN social.profile author ON author.id=p.author_id AND author.state='ACTIVE'
WHERE p.id=sqlc.arg(post_id)::uuid AND p.state='PUBLISHED'
AND NOT EXISTS(SELECT FROM social.profile_block b WHERE
(b.blocker_id=sqlc.arg(viewer_id)::uuid AND b.blocked_id=p.author_id) OR
(b.blocked_id=sqlc.arg(viewer_id)::uuid AND b.blocker_id=p.author_id))) AS allowed;

-- +goose Up
-- Only currently public content is eligible; private edit candidates are never copied.
CREATE VIEW social.content_report_target WITH (security_invoker=true) AS
 SELECT 'POST'::text AS target_type,p.id AS target_id,p.id AS post_id,p.author_id,p.author_id AS post_author_id,
 p.published_revision::bigint AS revision,r.title,r.body,a.display_name
 FROM social.post p JOIN social.post_revision r ON r.post_id=p.id AND r.revision=p.published_revision
 JOIN social.profile a ON a.id=p.author_id AND a.state='ACTIVE'
 LEFT JOIN social.community community ON community.id=p.community_id
 WHERE p.state='PUBLISHED' AND (community.id IS NULL OR (community.state='ACTIVE' AND community.visibility IN ('PUBLIC','RESTRICTED')))
 UNION ALL
 SELECT 'COMMENT',c.id,p.id,c.author_id,p.author_id,c.published_version,
 CASE WHEN p.state='DELETED' THEN NULL ELSE r.title END,c.body,a.display_name
 FROM social.comment c JOIN social.post p ON p.id=c.post_id
 JOIN social.profile a ON a.id=c.author_id AND a.state='ACTIVE'
 LEFT JOIN social.profile pa ON pa.id=p.author_id
 LEFT JOIN social.post_revision r ON r.post_id=p.id AND r.revision=p.published_revision
 LEFT JOIN social.community community ON community.id=p.community_id
 WHERE c.state='PUBLISHED' AND p.state IN ('PUBLISHED','DELETED')
 AND (p.state='DELETED' OR p.author_id IS NULL OR pa.state='ACTIVE')
 AND (community.id IS NULL OR (community.state='ACTIVE' AND community.visibility IN ('PUBLIC','RESTRICTED')));

CREATE UNIQUE INDEX content_report_post_once ON social.moderation_case(reporter_ref,post_id,target_version) WHERE reporter_ref IS NOT NULL AND post_id IS NOT NULL;
CREATE UNIQUE INDEX content_report_comment_once ON social.moderation_case(reporter_ref,comment_id,target_version) WHERE reporter_ref IS NOT NULL AND comment_id IS NOT NULL;
CREATE INDEX content_report_owner_page ON social.moderation_case(reporter_ref,created_at DESC,id DESC) WHERE reporter_ref IS NOT NULL;
CREATE INDEX content_report_queue ON social.moderation_case(created_at,id) WHERE reporter_ref IS NOT NULL AND state='OPEN';

ALTER TABLE social.moderation_case ENABLE ROW LEVEL SECURITY;
ALTER TABLE social.moderation_case FORCE ROW LEVEL SECURITY;
CREATE POLICY moderation_case_read ON social.moderation_case FOR SELECT TO js_social USING(authz.has_role('PLATFORM_MODERATOR') OR reporter_ref=authz.principal());
CREATE POLICY moderation_case_review_insert ON social.moderation_case FOR INSERT TO js_social WITH CHECK(
 reporter_ref IS NULL AND reason_code='PUBLICATION_REVIEW' AND state='OPEN' AND
 (EXISTS(SELECT FROM social.post p WHERE p.id=moderation_case.post_id AND p.author_id=authz.current_profile() AND p.current_revision=moderation_case.target_version)
 OR EXISTS(SELECT FROM social.comment c WHERE c.id=moderation_case.comment_id AND c.author_id=authz.current_profile() AND c.current_revision=moderation_case.target_version)));
CREATE POLICY moderation_case_report_insert ON social.moderation_case FOR INSERT TO js_social WITH CHECK(
 reporter_ref=authz.principal() AND state='OPEN' AND reason_code IN ('SPAM','HARASSMENT','HATE','THREATS','PRIVACY','MISINFORMATION','OTHER')
 AND EXISTS(SELECT FROM social.content_report_target target WHERE target.target_id=COALESCE(moderation_case.post_id,moderation_case.comment_id)
 AND target.target_type=CASE WHEN moderation_case.post_id IS NOT NULL THEN 'POST' ELSE 'COMMENT' END
 AND target.revision=moderation_case.target_version AND target.author_id<>authz.current_profile()
 AND NOT EXISTS(SELECT FROM social.profile_block b WHERE
 (b.blocker_id=authz.current_profile() AND b.blocked_id IN (target.author_id,target.post_author_id)) OR
 (b.blocked_id=authz.current_profile() AND b.blocker_id IN (target.author_id,target.post_author_id)))));
CREATE POLICY moderation_case_update ON social.moderation_case FOR UPDATE TO js_social USING(authz.has_role('PLATFORM_MODERATOR')) WITH CHECK(authz.has_role('PLATFORM_MODERATOR'));

ALTER TABLE social.moderation_decision ENABLE ROW LEVEL SECURITY;
ALTER TABLE social.moderation_decision FORCE ROW LEVEL SECURITY;
CREATE POLICY moderation_decision_read ON social.moderation_decision FOR SELECT TO js_social USING(
 EXISTS(SELECT FROM social.moderation_case m WHERE m.id=moderation_case_id));
CREATE POLICY moderation_decision_insert ON social.moderation_decision FOR INSERT TO js_social WITH CHECK(
 authz.has_role('PLATFORM_MODERATOR') AND actor_ref=authz.principal()
 AND EXISTS(SELECT FROM social.moderation_case m WHERE m.id=moderation_case_id AND m.state IN ('OPEN','REVIEWING')));

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Retained moderation reports require a reviewed forward migration'; END $$;
-- +goose StatementEnd

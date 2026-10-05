-- +goose Up
-- Choices apply to reviewed text, never to an author's future edit.
ALTER TABLE social.selected_response ADD COLUMN post_revision integer;
ALTER TABLE social.selected_response ADD COLUMN comment_revision bigint;
UPDATE social.selected_response s SET post_revision=p.published_revision,comment_revision=c.published_version
 FROM social.post p,social.comment c WHERE p.id=s.post_id AND c.id=s.comment_id;
ALTER TABLE social.selected_response ALTER COLUMN post_revision SET NOT NULL;
ALTER TABLE social.selected_response ALTER COLUMN comment_revision SET NOT NULL;
ALTER TABLE social.selected_response ADD FOREIGN KEY(post_id,post_revision) REFERENCES social.post_revision(post_id,revision);
ALTER TABLE social.selected_response ADD FOREIGN KEY(comment_id,comment_revision) REFERENCES social.comment_revision(comment_id,version);

CREATE VIEW social.eligible_question_response WITH (security_invoker=true) AS
 SELECT p.id AS post_id,p.author_id AS post_author_id,p.community_id,p.published_revision AS post_revision,
 c.id AS comment_id,c.author_id,c.published_version AS comment_revision,c.body,a.handle,a.display_name
 FROM social.post p JOIN social.profile pa ON pa.id=p.author_id AND pa.state='ACTIVE'
 JOIN social.post_revision pr ON pr.post_id=p.id AND pr.revision=p.published_revision AND pr.review_state='APPROVED'
 JOIN social.community community ON community.id=p.community_id AND community.state='ACTIVE' AND community.visibility IN ('PUBLIC','RESTRICTED')
 JOIN social.comment c ON c.post_id=p.id AND c.state='PUBLISHED'
 JOIN social.comment_revision cr ON cr.comment_id=c.id AND cr.version=c.published_version AND cr.review_state='APPROVED' AND cr.body=c.body
 JOIN social.profile a ON a.id=c.author_id AND a.state='ACTIVE'
 WHERE p.kind='QUESTION' AND p.state='PUBLISHED'
 AND NOT EXISTS(SELECT FROM social.profile_block b WHERE
 (b.blocker_id=p.author_id AND b.blocked_id=c.author_id) OR (b.blocked_id=p.author_id AND b.blocker_id=c.author_id));

-- +goose StatementBegin
CREATE FUNCTION social.check_selected_response() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog,pg_temp AS $$
BEGIN
 IF NOT EXISTS(SELECT FROM social.eligible_question_response e WHERE e.post_id=NEW.post_id AND e.comment_id=NEW.comment_id
 AND e.post_revision=NEW.post_revision AND e.comment_revision=NEW.comment_revision) THEN
  RAISE EXCEPTION 'helpful response needs a visible reviewed question and reply' USING ERRCODE='23514';
 END IF;
 NEW.selected_at=statement_timestamp();
 RETURN NEW;
END $$;
CREATE TRIGGER selected_response_check BEFORE INSERT OR UPDATE ON social.selected_response FOR EACH ROW EXECUTE FUNCTION social.check_selected_response();
-- +goose StatementEnd

ALTER TABLE social.selected_response ENABLE ROW LEVEL SECURITY;
ALTER TABLE social.selected_response FORCE ROW LEVEL SECURITY;
-- Owners need to replace/clear retained choices after a source revision changes.
CREATE POLICY response_owner ON social.selected_response FOR ALL TO js_social USING(
 EXISTS(SELECT FROM social.post p WHERE p.id=post_id AND (p.author_id=authz.current_profile()
 OR EXISTS(SELECT FROM social.community_member m WHERE m.community_id=p.community_id AND m.profile_id=authz.current_profile() AND m.state='ACTIVE' AND m.role IN ('MODERATOR','OWNER')))))
 WITH CHECK(selected_by=authz.current_profile() AND
 EXISTS(SELECT FROM social.eligible_question_response e WHERE e.post_id=selected_response.post_id AND e.comment_id=selected_response.comment_id
 AND e.post_revision=selected_response.post_revision AND e.comment_revision=selected_response.comment_revision
 AND (e.post_author_id=authz.current_profile() OR EXISTS(SELECT FROM social.community_member m WHERE m.community_id=e.community_id AND m.profile_id=authz.current_profile() AND m.state='ACTIVE' AND m.role IN ('MODERATOR','OWNER')))
 AND NOT EXISTS(SELECT FROM social.profile_block b WHERE
 (b.blocker_id=authz.current_profile() AND b.blocked_id IN(e.author_id,e.post_author_id)) OR (b.blocked_id=authz.current_profile() AND b.blocker_id IN(e.author_id,e.post_author_id)))));
CREATE POLICY response_public ON social.selected_response FOR SELECT TO js_social USING(
 EXISTS(SELECT FROM social.eligible_question_response e WHERE e.post_id=selected_response.post_id AND e.comment_id=selected_response.comment_id
 AND e.post_revision=selected_response.post_revision AND e.comment_revision=selected_response.comment_revision
 AND NOT EXISTS(SELECT FROM social.profile_block b WHERE
 (b.blocker_id=authz.current_profile() AND b.blocked_id IN(e.author_id,e.post_author_id)) OR (b.blocked_id=authz.current_profile() AND b.blocker_id IN(e.author_id,e.post_author_id)))));

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Helpful response history requires a reviewed forward migration'; END $$;
-- +goose StatementEnd

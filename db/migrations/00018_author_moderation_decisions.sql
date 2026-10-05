-- +goose Up
-- Internal review notes stay private; authors receive a separately written reason.
ALTER TABLE social.moderation_decision ADD COLUMN author_reason text
 CHECK(author_reason IS NULL OR char_length(btrim(author_reason)) BETWEEN 5 AND 1000);

-- A narrow owner-scoped projection uses the migration owner's privileges to read
-- decisions without granting authors access to complaint cases or internal notes.
-- The authenticated session, never a supplied profile ID, determines ownership.
CREATE VIEW social.author_moderation_decision WITH (security_barrier=true) AS
 SELECT d.id,CASE WHEN m.comment_id IS NULL THEN 'POST' ELSE 'COMMENT' END AS target_type,
 COALESCE(m.comment_id,m.post_id) AS target_id,COALESCE(c.post_id,m.post_id) AS post_id,
 m.target_version AS target_revision,d.action,d.rule_version,
 CASE WHEN d.action='ALLOW' THEN 'This revision was approved for publication.'
 ELSE COALESCE(d.author_reason,CASE WHEN d.action='REMOVE'
 THEN 'A moderator removed this content. No author-facing reason was recorded.'
 ELSE 'A moderator restricted this revision. No author-facing reason was recorded.' END) END::text AS reason,
 d.decided_at
 FROM social.moderation_decision d JOIN social.moderation_case m ON m.id=d.moderation_case_id
 LEFT JOIN social.post p ON p.id=m.post_id LEFT JOIN social.comment c ON c.id=m.comment_id
 WHERE COALESCE(c.author_id,p.author_id)=authz.current_profile()
 AND ((m.reporter_ref IS NULL AND m.reason_code='PUBLICATION_REVIEW' AND d.action IN ('ALLOW','RESTRICT'))
 OR (m.reporter_ref IS NOT NULL AND d.action='REMOVE'));
REVOKE ALL ON social.author_moderation_decision FROM PUBLIC;

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Retained author decision history requires a reviewed forward migration'; END $$;
-- +goose StatementEnd

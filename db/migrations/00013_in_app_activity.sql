-- +goose Up
-- Record mutation time after command lock waits, rather than transaction start.
-- Otherwise a late follow can appear to precede a publication, or vice versa.
ALTER TABLE social.case_follow ALTER COLUMN created_at SET DEFAULT statement_timestamp();
ALTER TABLE infra.outbox ALTER COLUMN created_at SET DEFAULT statement_timestamp();
ALTER TABLE social.notification ADD COLUMN kind text NOT NULL DEFAULT 'LEGACY'
 CHECK (kind IN ('LEGACY','REPLY','CASE_PROGRESS'));
ALTER TABLE social.notification ADD COLUMN comment_id uuid REFERENCES social.comment(id);
ALTER TABLE social.notification ADD COLUMN source_version bigint CHECK (source_version > 0);
ALTER TABLE social.notification ADD CONSTRAINT activity_source_shape CHECK (
 kind='LEGACY' OR (channel='IN_APP' AND source_version IS NOT NULL AND
 ((kind='REPLY' AND post_id IS NOT NULL AND comment_id IS NOT NULL AND receipt_id IS NULL)
 OR (kind='CASE_PROGRESS' AND receipt_id IS NOT NULL AND post_id IS NULL AND comment_id IS NULL))));
CREATE UNIQUE INDEX activity_reply_once ON social.notification(recipient_id,comment_id) WHERE kind='REPLY';
CREATE UNIQUE INDEX activity_receipt_version_once ON social.notification(recipient_id,receipt_id,source_version) WHERE kind='CASE_PROGRESS';
CREATE INDEX activity_owner_page ON social.notification(recipient_id,created_at DESC,id DESC) WHERE channel='IN_APP' AND state='SENT';

-- +goose StatementBegin
CREATE FUNCTION authz.current_profile() RETURNS uuid LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $$
 SELECT profile_id FROM authz.authenticate(decode(nullif(current_setting('jansetu.session_hash',true),''),'hex'),current_setting('jansetu.auth_mode',true),current_setting('jansetu.issuer',true))
$$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION authz.current_profile() FROM PUBLIC;
ALTER TABLE social.notification ENABLE ROW LEVEL SECURITY;
ALTER TABLE social.notification FORCE ROW LEVEL SECURITY;
CREATE POLICY activity_owner_read ON social.notification FOR SELECT TO js_social USING(recipient_id=authz.current_profile());
CREATE POLICY activity_owner_update ON social.notification FOR UPDATE TO js_social USING(recipient_id=authz.current_profile()) WITH CHECK(recipient_id=authz.current_profile());
CREATE POLICY activity_worker_read ON social.notification FOR SELECT TO js_worker USING(true);
CREATE POLICY activity_worker_insert ON social.notification FOR INSERT TO js_worker WITH CHECK(true);
ALTER TABLE social.feed_preference ENABLE ROW LEVEL SECURITY;
ALTER TABLE social.feed_preference FORCE ROW LEVEL SECURITY;
CREATE POLICY preference_owner_read ON social.feed_preference FOR SELECT TO js_social USING(profile_id=authz.current_profile());
CREATE POLICY preference_worker_read ON social.feed_preference FOR SELECT TO js_worker USING(true);
ALTER TABLE social.mute ENABLE ROW LEVEL SECURITY;
ALTER TABLE social.mute FORCE ROW LEVEL SECURITY;
CREATE POLICY mute_owner_read ON social.mute FOR SELECT TO js_social USING(profile_id=authz.current_profile());
CREATE POLICY mute_worker_read ON social.mute FOR SELECT TO js_worker USING(true);

-- Shared eligibility rules for delivery and every subsequent inbox read.
-- These views contain identifiers and chosen public metadata, never bodies.
CREATE VIEW social.activity_reply_target WITH (security_invoker=true) AS
 SELECT c.id AS comment_id,c.post_id,c.published_version AS source_version,
 recipient.id AS recipient_id,author.id AS actor_id,author.display_name,author.handle
 FROM social.comment c JOIN social.post p ON p.id=c.post_id
 JOIN social.profile author ON author.id=c.author_id AND author.state='ACTIVE'
 LEFT JOIN social.comment parent ON parent.id=c.parent_id
 JOIN social.profile recipient ON recipient.id=CASE WHEN c.parent_id IS NULL THEN p.author_id ELSE parent.author_id END AND recipient.state='ACTIVE'
 LEFT JOIN social.profile post_author ON post_author.id=p.author_id
 LEFT JOIN social.community community ON community.id=p.community_id
 WHERE c.state='PUBLISHED' AND c.published_version IS NOT NULL AND p.state='PUBLISHED'
 AND (c.parent_id IS NULL OR parent.state='PUBLISHED') AND recipient.id<>author.id
 AND (p.author_id IS NULL OR post_author.state='ACTIVE')
 AND (community.id IS NULL OR (community.state='ACTIVE' AND community.visibility IN ('PUBLIC','RESTRICTED')))
 AND NOT EXISTS(SELECT FROM social.profile_block b WHERE
 (b.blocker_id=recipient.id AND b.blocked_id IN (author.id,p.author_id)) OR
 (b.blocked_id=recipient.id AND b.blocker_id IN (author.id,p.author_id)))
 AND NOT EXISTS(SELECT FROM social.mute m WHERE m.profile_id=recipient.id AND (m.expires_at IS NULL OR m.expires_at>statement_timestamp())
 AND (m.muted_profile_id IN (author.id,p.author_id) OR m.muted_community_id=p.community_id))
 AND NOT EXISTS(SELECT FROM social.feed_preference f WHERE f.profile_id=recipient.id AND NOT ('IN_APP'=ANY(f.notification_channels)));

CREATE VIEW social.activity_case_target WITH (security_invoker=true) AS
 SELECT r.id AS receipt_id,r.title,r.projection_version,f.profile_id AS recipient_id,f.created_at AS followed_at
 FROM social.case_receipt r JOIN social.case_follow f ON f.receipt_id=r.id
 JOIN social.profile recipient ON recipient.id=f.profile_id AND recipient.state='ACTIVE'
 WHERE r.publication_state='PUBLISHED'
 AND NOT EXISTS(SELECT FROM social.feed_preference pref WHERE pref.profile_id=f.profile_id AND NOT ('IN_APP'=ANY(pref.notification_channels)));

CREATE VIEW social.activity_visible WITH (security_invoker=true) AS
 SELECT n.id,n.recipient_id,n.kind,n.post_id,n.receipt_id,n.created_at,n.read_at,
 reply.actor_id,reply.display_name,reply.handle,receipt.title
 FROM social.notification n
 LEFT JOIN social.activity_reply_target reply ON reply.comment_id=n.comment_id AND reply.recipient_id=n.recipient_id AND reply.source_version>=n.source_version
 LEFT JOIN social.activity_case_target receipt ON receipt.receipt_id=n.receipt_id AND receipt.recipient_id=n.recipient_id AND receipt.followed_at<=n.created_at
 WHERE n.channel='IN_APP' AND n.state='SENT'
 AND ((n.kind='REPLY' AND reply.comment_id IS NOT NULL AND reply.post_id=n.post_id)
 OR (n.kind='CASE_PROGRESS' AND receipt.receipt_id IS NOT NULL));

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Activity history rollback requires a reviewed forward migration'; END $$;
-- +goose StatementEnd

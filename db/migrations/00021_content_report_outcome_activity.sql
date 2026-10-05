-- +goose Up
ALTER TABLE social.notification DROP CONSTRAINT notification_kind_check;
ALTER TABLE social.notification ADD CONSTRAINT notification_kind_check CHECK(kind IN ('LEGACY','REPLY','CASE_PROGRESS','MODERATION_DECISION','APPEAL_OUTCOME','CONTENT_REPORT_OUTCOME'));
ALTER TABLE social.notification ADD COLUMN content_report_id uuid REFERENCES social.moderation_case(id);
ALTER TABLE social.notification DROP CONSTRAINT activity_source_shape;
ALTER TABLE social.notification ADD CONSTRAINT activity_source_shape CHECK(
 (kind='LEGACY' AND moderation_decision_id IS NULL AND appeal_id IS NULL AND content_report_id IS NULL)
 OR (channel='IN_APP' AND source_version IS NOT NULL AND (
 (kind='REPLY' AND post_id IS NOT NULL AND comment_id IS NOT NULL AND receipt_id IS NULL AND moderation_decision_id IS NULL AND appeal_id IS NULL AND content_report_id IS NULL)
 OR (kind='CASE_PROGRESS' AND receipt_id IS NOT NULL AND post_id IS NULL AND comment_id IS NULL AND moderation_decision_id IS NULL AND appeal_id IS NULL AND content_report_id IS NULL)
 OR (kind='MODERATION_DECISION' AND moderation_decision_id IS NOT NULL AND appeal_id IS NULL AND content_report_id IS NULL AND post_id IS NULL AND comment_id IS NULL AND receipt_id IS NULL)
 OR (kind='APPEAL_OUTCOME' AND appeal_id IS NOT NULL AND moderation_decision_id IS NULL AND content_report_id IS NULL AND post_id IS NULL AND comment_id IS NULL AND receipt_id IS NULL)
 OR (kind='CONTENT_REPORT_OUTCOME' AND content_report_id IS NOT NULL AND moderation_decision_id IS NULL AND appeal_id IS NULL AND post_id IS NULL AND comment_id IS NULL AND receipt_id IS NULL))));
CREATE UNIQUE INDEX activity_content_report_once ON social.notification(recipient_id,content_report_id) WHERE kind='CONTENT_REPORT_OUTCOME';

-- Preserve the narrow four-column projection. Report owners are resolved without
-- exposing complaint text, outcome reasons, reported targets or principal IDs.
CREATE OR REPLACE VIEW social.activity_review_source WITH (security_barrier=true) AS
 SELECT 'MODERATION_DECISION'::text AS kind,d.id AS source_id,recipient.id AS recipient_id,1::bigint AS source_version
 FROM social.moderation_decision d JOIN social.moderation_case m ON m.id=d.moderation_case_id
 LEFT JOIN social.post p ON p.id=m.post_id LEFT JOIN social.comment c ON c.id=m.comment_id
 JOIN social.profile recipient ON recipient.id=COALESCE(c.author_id,p.author_id) AND recipient.state='ACTIVE'
 WHERE ((m.reporter_ref IS NULL AND m.reason_code='PUBLICATION_REVIEW' AND d.action='RESTRICT') OR (m.reporter_ref IS NOT NULL AND d.action='REMOVE'))
 AND NOT EXISTS(SELECT FROM social.feed_preference f WHERE f.profile_id=recipient.id AND NOT('IN_APP'=ANY(f.notification_channels)))
 UNION ALL
 SELECT 'APPEAL_OUTCOME'::text,a.id,recipient.id,a.version
 FROM social.appeal a JOIN social.appeal_decision d ON d.appeal_id=a.id AND d.result=a.state
 JOIN identity.principal owner ON owner.id=a.appellant_ref AND owner.state='ACTIVE'
 JOIN social.profile recipient ON recipient.id=owner.profile_id AND recipient.state='ACTIVE'
 WHERE a.state IN ('UPHELD','REVERSED')
 AND NOT EXISTS(SELECT FROM social.feed_preference f WHERE f.profile_id=recipient.id AND NOT('IN_APP'=ANY(f.notification_channels)))
 UNION ALL
 SELECT 'CONTENT_REPORT_OUTCOME'::text,m.id,recipient.id,m.version
 FROM social.moderation_case m JOIN social.moderation_decision d ON d.moderation_case_id=m.id AND d.action IN ('ALLOW','REMOVE')
 JOIN identity.principal reporter ON reporter.id=m.reporter_ref AND reporter.state='ACTIVE'
 JOIN social.profile recipient ON recipient.id=reporter.profile_id AND recipient.state='ACTIVE'
 WHERE m.reporter_ref IS NOT NULL AND m.state='DECIDED' AND num_nonnulls(m.post_id,m.comment_id)=1
 AND NOT EXISTS(SELECT FROM social.feed_preference f WHERE f.profile_id=recipient.id AND NOT('IN_APP'=ANY(f.notification_channels)));

ALTER POLICY activity_worker_insert ON social.notification WITH CHECK(
 kind NOT IN ('MODERATION_DECISION','APPEAL_OUTCOME','CONTENT_REPORT_OUTCOME') OR EXISTS(SELECT FROM social.activity_review_source source
 WHERE source.kind=notification.kind AND source.source_id=COALESCE(notification.moderation_decision_id,notification.appeal_id,notification.content_report_id)
 AND source.recipient_id=notification.recipient_id AND source.source_version=notification.source_version));

CREATE OR REPLACE VIEW social.activity_visible WITH (security_invoker=true) AS
 SELECT n.id,n.recipient_id,n.kind,n.post_id,n.receipt_id,n.created_at,n.read_at,
 reply.actor_id,reply.display_name,reply.handle,receipt.title,n.moderation_decision_id,n.appeal_id,n.content_report_id
 FROM social.notification n
 LEFT JOIN social.activity_reply_target reply ON reply.comment_id=n.comment_id AND reply.recipient_id=n.recipient_id AND reply.source_version>=n.source_version
 LEFT JOIN social.activity_case_target receipt ON receipt.receipt_id=n.receipt_id AND receipt.recipient_id=n.recipient_id AND receipt.followed_at<=n.created_at
 LEFT JOIN social.activity_review_target review ON review.kind=n.kind AND review.source_id=COALESCE(n.moderation_decision_id,n.appeal_id,n.content_report_id)
 AND review.recipient_id=n.recipient_id AND review.source_version=n.source_version
 WHERE n.channel='IN_APP' AND n.state='SENT'
 AND ((n.kind='REPLY' AND reply.comment_id IS NOT NULL AND reply.post_id=n.post_id)
 OR (n.kind='CASE_PROGRESS' AND receipt.receipt_id IS NOT NULL)
 OR (n.kind IN ('MODERATION_DECISION','APPEAL_OUTCOME','CONTENT_REPORT_OUTCOME') AND review.source_id IS NOT NULL));

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Private content report activity requires a reviewed forward migration'; END $$;
-- +goose StatementEnd

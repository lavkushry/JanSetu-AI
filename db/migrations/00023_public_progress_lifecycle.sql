-- +goose Up
-- Publication has its own revision; operational case age/version do not change.
ALTER TABLE social.case_receipt ADD COLUMN publication_version bigint NOT NULL DEFAULT 1 CHECK(publication_version>0);
ALTER TABLE social.case_receipt ADD COLUMN last_withdrawn_version bigint NOT NULL DEFAULT 0 CHECK(last_withdrawn_version>=0 AND last_withdrawn_version<=publication_version);
UPDATE social.case_receipt r SET publication_version=GREATEST(r.projection_version,COALESCE((SELECT max(n.source_version) FROM social.notification n WHERE n.receipt_id=r.id AND n.kind='CASE_PROGRESS'),0));
UPDATE social.case_receipt SET last_withdrawn_version=publication_version WHERE publication_state='WITHDRAWN';
ALTER TABLE ops.publication_decision ADD COLUMN publication_version bigint CHECK(publication_version>0);
ALTER TABLE ops.publication_decision ADD COLUMN internal_reason text;
CREATE UNIQUE INDEX publication_revision_once ON ops.publication_decision(case_id,publication_version) WHERE publication_version IS NOT NULL;

-- Historical decisions retain unknown publication revisions. Do not invent a
-- mapping from repeated legacy case versions to newly independent revisions.
CREATE VIEW ops.publication_status WITH (security_barrier=true) AS
 SELECT b.case_id,r.id AS receipt_id,r.publication_state,r.publication_version,r.projection_version AS case_version,r.title,r.safe_summary,r.area_label
 FROM ops.publication_binding b JOIN social.case_receipt r ON r.id=b.receipt_id
 WHERE authz.has_role('PUBLISHER') AND authz.case_access(b.case_id);
CREATE VIEW ops.publication_history WITH (security_barrier=true) AS
 SELECT d.id,d.case_id,d.case_version,d.publication_version,d.action,d.internal_reason,d.decided_at
 FROM ops.publication_decision d WHERE authz.has_role('PUBLISHER') AND authz.case_access(d.case_id);
REVOKE ALL ON ops.publication_status,ops.publication_history FROM PUBLIC;

CREATE OR REPLACE VIEW social.activity_case_target WITH (security_invoker=true) AS
 SELECT r.id AS receipt_id,r.title,r.publication_version AS projection_version,f.profile_id AS recipient_id,f.created_at AS followed_at,r.last_withdrawn_version
 FROM social.case_receipt r JOIN social.case_follow f ON f.receipt_id=r.id
 JOIN social.profile recipient ON recipient.id=f.profile_id AND recipient.state='ACTIVE'
 WHERE r.publication_state='PUBLISHED'
 AND NOT EXISTS(SELECT FROM social.feed_preference pref WHERE pref.profile_id=f.profile_id AND NOT ('IN_APP'=ANY(pref.notification_channels)));

ALTER POLICY activity_worker_insert ON social.notification WITH CHECK(
 (kind='CASE_PROGRESS' AND EXISTS(SELECT FROM social.activity_case_target source WHERE source.receipt_id=notification.receipt_id
 AND source.recipient_id=notification.recipient_id AND source.projection_version=notification.source_version
 AND notification.source_version>source.last_withdrawn_version AND source.followed_at<=notification.created_at))
 OR kind NOT IN ('CASE_PROGRESS','MODERATION_DECISION','PUBLICATION_APPROVAL','APPEAL_OUTCOME','CONTENT_REPORT_OUTCOME')
 OR EXISTS(SELECT FROM social.activity_review_source source WHERE source.kind=notification.kind
 AND source.source_id=COALESCE(notification.moderation_decision_id,notification.appeal_id,notification.content_report_id)
 AND source.recipient_id=notification.recipient_id AND source.source_version=notification.source_version));

CREATE OR REPLACE VIEW social.activity_visible WITH (security_invoker=true) AS
 SELECT n.id,n.recipient_id,n.kind,n.post_id,n.receipt_id,n.created_at,n.read_at,
 reply.actor_id,reply.display_name,reply.handle,receipt.title,n.moderation_decision_id,n.appeal_id,n.content_report_id
 FROM social.notification n
 LEFT JOIN social.activity_reply_target reply ON reply.comment_id=n.comment_id AND reply.recipient_id=n.recipient_id AND reply.source_version>=n.source_version
 LEFT JOIN social.activity_case_target receipt ON receipt.receipt_id=n.receipt_id AND receipt.recipient_id=n.recipient_id AND receipt.followed_at<=n.created_at AND n.source_version>receipt.last_withdrawn_version
 LEFT JOIN social.activity_review_target review ON review.kind=n.kind AND review.source_id=COALESCE(n.moderation_decision_id,n.appeal_id,n.content_report_id)
 AND review.recipient_id=n.recipient_id AND review.source_version=n.source_version
 WHERE n.channel='IN_APP' AND n.state='SENT'
 AND ((n.kind='REPLY' AND reply.comment_id IS NOT NULL AND reply.post_id=n.post_id)
 OR (n.kind='CASE_PROGRESS' AND receipt.receipt_id IS NOT NULL)
 OR (n.kind IN ('MODERATION_DECISION','PUBLICATION_APPROVAL','APPEAL_OUTCOME','CONTENT_REPORT_OUTCOME') AND review.source_id IS NOT NULL));

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Retained public progress decisions require a reviewed forward migration'; END $$;
-- +goose StatementEnd

-- +goose Up
ALTER TABLE social.media_asset ADD COLUMN rejection_code text;
ALTER TABLE social.media_asset ADD COLUMN processing_token uuid;
ALTER TABLE social.media_asset ADD COLUMN processing_until timestamptz;
ALTER TABLE infra.upload_session ADD COLUMN token_hash bytea CHECK(token_hash IS NULL OR octet_length(token_hash)=32);
ALTER TABLE infra.upload_session ADD COLUMN part_etag text;
ALTER TABLE infra.analysis_job ADD COLUMN session_hash bytea CHECK(octet_length(session_hash)=32);
ALTER TABLE infra.analysis_job ADD COLUMN auth_mode text;
ALTER TABLE infra.analysis_job ADD COLUMN auth_issuer text;
ALTER TABLE infra.analysis_job ADD COLUMN lease_token uuid;
ALTER TABLE infra.analysis_job ADD COLUMN lease_until timestamptz;
CREATE INDEX media_quarantine_queue ON social.media_asset(created_at) WHERE state='QUARANTINED';
CREATE INDEX analysis_queue ON infra.analysis_job(created_at) WHERE state IN ('QUEUED','RUNNING');
CREATE TABLE ops.report_media (
 report_id uuid NOT NULL REFERENCES ops.report(id),
 media_id uuid NOT NULL UNIQUE REFERENCES social.media_asset(id),
 PRIMARY KEY(report_id,media_id)
);

-- +goose StatementBegin
CREATE FUNCTION authz.media_read(mid uuid) RETURNS boolean LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $$
 SELECT EXISTS(SELECT FROM social.media_asset m WHERE m.id=mid AND (authz.owns_alias(m.report_alias_ref) OR
 (authz.has_role('COORDINATOR') AND EXISTS(SELECT FROM ops.report_media rm WHERE rm.media_id=m.id))))
$$;
CREATE FUNCTION authz.media_owner(mid uuid) RETURNS boolean LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $$
 SELECT EXISTS(SELECT FROM social.media_asset WHERE id=mid AND authz.owns_alias(report_alias_ref))
$$;
CREATE FUNCTION authz.analysis_owner(jid uuid) RETURNS boolean LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $$
 SELECT EXISTS(SELECT FROM infra.analysis_job j JOIN social.media_asset m ON m.id=j.media_id WHERE j.id=jid AND authz.owns_alias(j.report_alias_ref) AND m.state='APPROVED' AND m.sha256=j.source_sha256 AND m.authorization_version=j.authorization_version AND j.expires_at>statement_timestamp())
$$;
CREATE FUNCTION authz.media_retained(mid uuid) RETURNS boolean LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $$
 SELECT EXISTS(SELECT FROM ops.report_media WHERE media_id=mid)
$$;
CREATE FUNCTION authz.media_job_live(jid uuid) RETURNS boolean LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $$
 SELECT EXISTS(SELECT FROM infra.analysis_job j JOIN social.media_asset m ON m.id=j.media_id WHERE j.id=jid AND j.state='RUNNING' AND j.expires_at>statement_timestamp() AND m.state='APPROVED' AND m.sha256=j.source_sha256 AND m.authorization_version=j.authorization_version AND EXISTS(SELECT FROM authz.authenticate(j.session_hash,j.auth_mode,j.auth_issuer)))
$$;
CREATE FUNCTION authz.media_attached(mid uuid) RETURNS boolean LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $$
 SELECT EXISTS(SELECT FROM ops.report_media WHERE media_id=mid) AND authz.media_read(mid)
$$;
CREATE FUNCTION authz.media_attachable(mid uuid,rid uuid) RETURNS boolean LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $$
DECLARE alias uuid; media_state text;
BEGIN
 SELECT report_alias_ref,state INTO alias,media_state FROM social.media_asset WHERE id=mid FOR UPDATE;
 RETURN media_state='APPROVED' AND authz.owns_alias(alias) AND EXISTS(SELECT FROM ops.report WHERE id=rid AND reporter_ref=alias);
END $$;
CREATE FUNCTION authz.report_media_ids(rid uuid) RETURNS uuid[] LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $$
 SELECT coalesce(array_agg(media_id ORDER BY media_id),'{}'::uuid[]) FROM ops.report_media WHERE report_id=rid AND (authz.owns_report(rid) OR authz.has_role('COORDINATOR'))
$$;
CREATE FUNCTION authz.report_ocr_region(rid uuid,tid uuid,region text) RETURNS text LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $$
 SELECT v->>'text' FROM infra.analysis_task t JOIN infra.analysis_job j ON j.id=t.job_id JOIN ops.report_media rm ON rm.media_id=j.media_id JOIN social.media_asset m ON m.id=j.media_id CROSS JOIN LATERAL jsonb_array_elements(t.result->'regions') v
 WHERE rm.report_id=rid AND authz.owns_report(rid) AND t.id=tid AND t.task_kind='OCR' AND t.state='SUCCEEDED' AND j.state IN ('SUCCEEDED','PARTIAL') AND j.expires_at>statement_timestamp() AND m.state='APPROVED' AND m.sha256=j.source_sha256 AND m.authorization_version=j.authorization_version AND v->>'id'=region
$$;
-- +goose StatementEnd

ALTER TABLE social.media_asset ENABLE ROW LEVEL SECURITY;
ALTER TABLE social.media_asset FORCE ROW LEVEL SECURITY;
CREATE POLICY media_read ON social.media_asset FOR SELECT TO js_media USING(authz.media_read(id));
CREATE POLICY media_insert ON social.media_asset FOR INSERT TO js_media WITH CHECK(uploader_id IS NULL AND authz.owns_alias(report_alias_ref) AND state='UPLOADING');
CREATE POLICY media_update ON social.media_asset FOR UPDATE TO js_media USING(authz.media_read(id)) WITH CHECK(authz.owns_alias(report_alias_ref));
CREATE POLICY media_process ON social.media_asset TO js_media_worker USING(uploader_id IS NULL AND report_alias_ref IS NOT NULL) WITH CHECK(uploader_id IS NULL AND report_alias_ref IS NOT NULL);
ALTER TABLE infra.upload_session ENABLE ROW LEVEL SECURITY;
ALTER TABLE infra.upload_session FORCE ROW LEVEL SECURITY;
CREATE POLICY upload_owner ON infra.upload_session TO js_media USING(authz.media_owner(media_id)) WITH CHECK(authz.media_owner(media_id));
CREATE POLICY upload_cleanup ON infra.upload_session TO js_media_worker USING(true) WITH CHECK(true);
ALTER TABLE infra.media_derivative ENABLE ROW LEVEL SECURITY;
ALTER TABLE infra.media_derivative FORCE ROW LEVEL SECURITY;
CREATE POLICY derivative_read ON infra.media_derivative FOR SELECT TO js_media USING(authz.media_read(media_id));
CREATE POLICY derivative_process ON infra.media_derivative TO js_media_worker USING(true) WITH CHECK(true);
ALTER TABLE infra.analysis_job ENABLE ROW LEVEL SECURITY;
ALTER TABLE infra.analysis_job FORCE ROW LEVEL SECURITY;
CREATE POLICY analysis_owner ON infra.analysis_job TO js_media USING(authz.analysis_owner(id)) WITH CHECK(authz.media_owner(media_id) AND authz.owns_alias(report_alias_ref));
CREATE POLICY analysis_process ON infra.analysis_job TO js_media_worker USING(report_alias_ref IS NOT NULL) WITH CHECK(report_alias_ref IS NOT NULL);
ALTER TABLE infra.analysis_task ENABLE ROW LEVEL SECURITY;
ALTER TABLE infra.analysis_task FORCE ROW LEVEL SECURITY;
CREATE POLICY analysis_task_owner ON infra.analysis_task TO js_media USING(authz.analysis_owner(job_id)) WITH CHECK(authz.analysis_owner(job_id));
CREATE POLICY analysis_task_process ON infra.analysis_task TO js_media_worker USING(true) WITH CHECK(true);
ALTER TABLE ops.report_media ENABLE ROW LEVEL SECURITY;
ALTER TABLE ops.report_media FORCE ROW LEVEL SECURITY;
CREATE POLICY report_media_read ON ops.report_media FOR SELECT TO js_ops USING(authz.owns_report(report_id) OR authz.has_role('COORDINATOR'));
CREATE POLICY report_media_insert ON ops.report_media FOR INSERT TO js_ops WITH CHECK(authz.media_attachable(media_id,report_id));
REVOKE ALL ON ALL FUNCTIONS IN SCHEMA authz FROM PUBLIC;

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Private media rollback requires a reviewed forward migration'; END $$;
-- +goose StatementEnd

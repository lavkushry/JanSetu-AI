-- +goose Up
CREATE EXTENSION IF NOT EXISTS pgcrypto;
CREATE SCHEMA authz;
REVOKE ALL ON SCHEMA authz FROM PUBLIC;
CREATE TABLE authz.vault_key (singleton boolean PRIMARY KEY DEFAULT true CHECK(singleton), signing_key bytea NOT NULL CHECK(octet_length(signing_key)=32));

-- +goose StatementBegin
CREATE FUNCTION authz.authenticate(token bytea, mode text, issuer text)
RETURNS TABLE(principal_id uuid, profile_id uuid, authorization_version bigint, session_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $$
 SELECT ip.id,ip.profile_id,ip.authorization_version,s.id
 FROM identity.session s JOIN identity.principal ip ON ip.id=s.principal_id
 JOIN social.profile p ON p.id=ip.profile_id
 WHERE octet_length(token)=32 AND s.token_hash=token AND s.auth_method=mode
 AND s.revoked_at IS NULL AND s.expires_at>statement_timestamp() AND s.last_seen_at>statement_timestamp()-interval '30 minutes'
 AND ip.state='ACTIVE' AND p.state='ACTIVE'
 AND (s.auth_method='demo' OR (s.provider=issuer AND EXISTS(SELECT FROM identity.account_binding b WHERE b.provider=s.provider AND b.provider_subject=s.provider_subject AND b.principal_id=ip.id AND b.state='ACTIVE')))
$$;
CREATE FUNCTION authz.principal() RETURNS uuid LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $$
 SELECT principal_id FROM authz.authenticate(decode(nullif(current_setting('jansetu.session_hash',true),''),'hex'),current_setting('jansetu.auth_mode',true),current_setting('jansetu.issuer',true))
$$;
CREATE FUNCTION authz.has_role(wanted text) RETURNS boolean LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $$
 SELECT EXISTS(SELECT FROM identity.platform_grant WHERE principal_id=authz.principal() AND role=wanted AND revoked_at IS NULL AND valid_to>statement_timestamp())
$$;
CREATE FUNCTION authz.agency(aid uuid) RETURNS boolean LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $$
 SELECT EXISTS(SELECT FROM identity.organization_grant g JOIN ops.agency a ON a.id=g.agency_id WHERE g.principal_id=authz.principal() AND g.agency_id=aid AND g.revoked_at IS NULL AND g.valid_from<=statement_timestamp() AND g.valid_to>statement_timestamp() AND a.state='ACTIVE')
$$;
CREATE FUNCTION authz.owns_alias(alias uuid) RETURNS boolean LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $$
DECLARE claim text := current_setting('jansetu.vault_claim',true); body jsonb; signature text := current_setting('jansetu.vault_signature',true);
BEGIN
 IF authz.principal() IS NULL OR claim IS NULL OR claim='' OR signature IS NULL THEN RETURN false; END IF;
 IF NOT EXISTS(SELECT FROM authz.vault_key WHERE encode(public.hmac(convert_to(claim,'UTF8'),signing_key,'sha256'),'hex')=signature) THEN RETURN false; END IF;
 body := claim::jsonb;
 RETURN body->>'sessionHash'=current_setting('jansetu.session_hash',true) AND (body->>'expiresAt')::bigint>extract(epoch FROM statement_timestamp()) AND body->'aliases' ? alias::text;
EXCEPTION WHEN invalid_text_representation OR numeric_value_out_of_range THEN RETURN false;
END $$;
CREATE FUNCTION authz.owns_report(rid uuid) RETURNS boolean LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $$
 SELECT EXISTS(SELECT FROM ops.report WHERE id=rid AND authz.owns_alias(reporter_ref))
$$;
CREATE FUNCTION authz.owns_case(cid uuid) RETURNS boolean LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $$
 SELECT EXISTS(SELECT FROM ops.intake_review WHERE case_id=cid AND authz.owns_report(report_id))
$$;
CREATE FUNCTION authz.case_access(cid uuid) RETURNS boolean LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $$
 SELECT authz.has_role('COORDINATOR') OR authz.has_role('PUBLISHER') OR EXISTS(SELECT FROM ops.obligation WHERE case_id=cid AND authz.agency(agency_id))
$$;
-- Lock only the account matching the secret session context, even when revoked;
-- the caller rechecks authenticate after waiting for this lock.
CREATE FUNCTION authz.lock_principal(pid uuid) RETURNS text LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $$
DECLARE result text;
BEGIN
 IF NOT EXISTS(SELECT FROM identity.session WHERE principal_id=pid AND token_hash=decode(nullif(current_setting('jansetu.session_hash',true),''),'hex')) THEN RETURN NULL; END IF;
 SELECT state INTO result FROM identity.principal WHERE id=pid FOR UPDATE;
 RETURN result;
END $$;
CREATE FUNCTION authz.lock_profile(pid uuid) RETURNS text LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $$
DECLARE result text;
BEGIN
 IF pid IS DISTINCT FROM authz.principal() THEN RETURN NULL; END IF;
 SELECT p.state INTO result FROM social.profile p JOIN identity.principal ip ON ip.profile_id=p.id WHERE ip.id=pid FOR UPDATE OF p;
 RETURN result;
END $$;
CREATE FUNCTION authz.owner_tasks(rid uuid) RETURNS TABLE(agency_name text,state text) LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $$
 SELECT a.name,o.state FROM ops.intake_review ir JOIN ops.obligation o ON o.case_id=ir.case_id LEFT JOIN ops.agency a ON a.id=o.agency_id WHERE ir.report_id=rid AND authz.owns_report(rid) ORDER BY o.id
$$;
-- +goose StatementEnd

ALTER TABLE ops.report ENABLE ROW LEVEL SECURITY;
ALTER TABLE ops.report FORCE ROW LEVEL SECURITY;
CREATE POLICY report_read ON ops.report FOR SELECT USING(authz.has_role('COORDINATOR') OR authz.has_role('PUBLISHER') OR authz.owns_alias(reporter_ref));
CREATE POLICY report_lock ON ops.report FOR UPDATE USING(authz.has_role('COORDINATOR')) WITH CHECK(false);
CREATE POLICY report_insert ON ops.report FOR INSERT WITH CHECK(authz.owns_alias(reporter_ref));
ALTER TABLE ops.intake_review ENABLE ROW LEVEL SECURITY;
ALTER TABLE ops.intake_review FORCE ROW LEVEL SECURITY;
CREATE POLICY intake_read ON ops.intake_review FOR SELECT USING(authz.has_role('COORDINATOR') OR authz.owns_report(report_id));
CREATE POLICY intake_insert ON ops.intake_review FOR INSERT WITH CHECK(authz.owns_report(report_id));
CREATE POLICY intake_update ON ops.intake_review FOR UPDATE USING(authz.has_role('COORDINATOR')) WITH CHECK(authz.has_role('COORDINATOR'));
ALTER TABLE ops.case_record ENABLE ROW LEVEL SECURITY;
ALTER TABLE ops.case_record FORCE ROW LEVEL SECURITY;
CREATE POLICY case_read ON ops.case_record FOR SELECT USING(authz.case_access(id));
CREATE POLICY case_insert ON ops.case_record FOR INSERT WITH CHECK(authz.has_role('COORDINATOR'));
CREATE POLICY case_update ON ops.case_record FOR UPDATE USING(authz.case_access(id)) WITH CHECK(authz.case_access(id));
ALTER TABLE ops.obligation ENABLE ROW LEVEL SECURITY;
ALTER TABLE ops.obligation FORCE ROW LEVEL SECURITY;
CREATE POLICY task_read ON ops.obligation FOR SELECT USING(authz.case_access(case_id));
CREATE POLICY task_insert ON ops.obligation FOR INSERT WITH CHECK(authz.has_role('COORDINATOR'));
CREATE POLICY task_update ON ops.obligation FOR UPDATE USING(authz.has_role('COORDINATOR') OR authz.agency(agency_id)) WITH CHECK(authz.has_role('COORDINATOR') OR authz.agency(agency_id));
ALTER TABLE ops.case_event ENABLE ROW LEVEL SECURITY;
ALTER TABLE ops.case_event FORCE ROW LEVEL SECURITY;
CREATE POLICY event_read ON ops.case_event FOR SELECT USING(authz.case_access(case_id));
CREATE POLICY event_insert ON ops.case_event FOR INSERT WITH CHECK(authz.case_access(case_id) AND actor_ref=authz.principal());
ALTER TABLE ops.case_observation ENABLE ROW LEVEL SECURITY;
ALTER TABLE ops.case_observation FORCE ROW LEVEL SECURITY;
CREATE POLICY observation_read ON ops.case_observation FOR SELECT USING(authz.case_access(case_id));
CREATE POLICY observation_insert ON ops.case_observation FOR INSERT WITH CHECK(authz.has_role('COORDINATOR'));
ALTER TABLE ops.coordinator_assignment ENABLE ROW LEVEL SECURITY;
ALTER TABLE ops.coordinator_assignment FORCE ROW LEVEL SECURITY;
CREATE POLICY coordinator_insert ON ops.coordinator_assignment FOR INSERT WITH CHECK(authz.has_role('COORDINATOR') AND principal_id=authz.principal());
ALTER TABLE ops.verification_decision ENABLE ROW LEVEL SECURITY;
ALTER TABLE ops.verification_decision FORCE ROW LEVEL SECURITY;
CREATE POLICY verification_insert ON ops.verification_decision FOR INSERT WITH CHECK(authz.case_access(case_id) AND reviewer_ref=authz.principal());
ALTER TABLE ops.publication_binding ENABLE ROW LEVEL SECURITY;
ALTER TABLE ops.publication_binding FORCE ROW LEVEL SECURITY;
CREATE POLICY binding_read ON ops.publication_binding FOR SELECT USING(authz.case_access(case_id) OR authz.owns_case(case_id));
CREATE POLICY binding_write ON ops.publication_binding FOR ALL TO js_publication USING(authz.has_role('PUBLISHER')) WITH CHECK(authz.has_role('PUBLISHER') AND reviewer_ref=authz.principal());
ALTER TABLE ops.publication_decision ENABLE ROW LEVEL SECURITY;
ALTER TABLE ops.publication_decision FORCE ROW LEVEL SECURITY;
CREATE POLICY publication_insert ON ops.publication_decision FOR INSERT TO js_publication WITH CHECK(authz.has_role('PUBLISHER') AND reviewer_ref=authz.principal());
ALTER TABLE social.case_receipt ENABLE ROW LEVEL SECURITY;
ALTER TABLE social.case_receipt FORCE ROW LEVEL SECURITY;
CREATE POLICY receipt_read ON social.case_receipt FOR SELECT USING(publication_state='PUBLISHED');
CREATE POLICY receipt_write ON social.case_receipt FOR ALL TO js_publication USING(authz.has_role('PUBLISHER')) WITH CHECK(authz.has_role('PUBLISHER'));
ALTER TABLE social.case_receipt_event ENABLE ROW LEVEL SECURITY;
ALTER TABLE social.case_receipt_event FORCE ROW LEVEL SECURITY;
CREATE POLICY receipt_event_read ON social.case_receipt_event FOR SELECT USING(EXISTS(SELECT FROM social.case_receipt WHERE id=receipt_id));
CREATE POLICY receipt_event_write ON social.case_receipt_event FOR ALL TO js_publication USING(authz.has_role('PUBLISHER')) WITH CHECK(authz.has_role('PUBLISHER'));
ALTER TABLE infra.idempotency_record ENABLE ROW LEVEL SECURITY;
ALTER TABLE infra.idempotency_record FORCE ROW LEVEL SECURITY;
CREATE POLICY command_owner ON infra.idempotency_record USING(principal_ref=authz.principal()) WITH CHECK(principal_ref=authz.principal());
-- Constraint triggers check private media eligibility without exposing those tables.
ALTER FUNCTION social.check_post_publication() SECURITY DEFINER;
ALTER FUNCTION social.check_post_publication() SET search_path=pg_catalog,pg_temp;
ALTER FUNCTION social.check_comment_publication() SECURITY DEFINER;
ALTER FUNCTION social.check_comment_publication() SET search_path=pg_catalog,pg_temp;
ALTER TABLE social.profile ENABLE ROW LEVEL SECURITY;
ALTER TABLE social.profile FORCE ROW LEVEL SECURITY;
CREATE POLICY profile_read ON social.profile FOR SELECT USING(true);
CREATE POLICY profile_provision ON social.profile FOR INSERT TO js_auth WITH CHECK(true);
CREATE POLICY profile_update ON social.profile FOR UPDATE TO js_auth USING(id=(SELECT profile_id FROM identity.principal WHERE id=authz.principal())) WITH CHECK(id=(SELECT profile_id FROM identity.principal WHERE id=authz.principal()));
CREATE POLICY profile_lock ON social.profile FOR UPDATE TO js_social USING(true) WITH CHECK(false);
REVOKE ALL ON ALL FUNCTIONS IN SCHEMA authz FROM PUBLIC;

-- +goose Down
-- Intentionally fail rather than silently restoring broad runtime access.
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Database isolation rollback requires a reviewed forward migration'; END $$;
-- +goose StatementEnd

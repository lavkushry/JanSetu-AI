-- +goose Up
-- Session and grant expiry use the current statement time, even after a lock
-- wait or inside an already-open transaction.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION authz.authenticate(token bytea, mode text, issuer text)
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
CREATE OR REPLACE FUNCTION authz.has_role(wanted text) RETURNS boolean LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $$
 SELECT EXISTS(SELECT FROM identity.platform_grant WHERE principal_id=authz.principal() AND role=wanted AND revoked_at IS NULL AND valid_to>statement_timestamp())
$$;
CREATE OR REPLACE FUNCTION authz.agency(aid uuid) RETURNS boolean LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $$
 SELECT EXISTS(SELECT FROM identity.organization_grant g JOIN ops.agency a ON a.id=g.agency_id WHERE g.principal_id=authz.principal() AND g.agency_id=aid AND g.revoked_at IS NULL AND g.valid_from<=statement_timestamp() AND g.valid_to>statement_timestamp() AND a.state='ACTIVE')
$$;
-- +goose StatementEnd
-- A column grant for SELECT FOR UPDATE must not permit identifier replacement.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION authz.guard_stable_id() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.id IS DISTINCT FROM OLD.id THEN RAISE EXCEPTION 'Resource identifiers are immutable'; END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION authz.guard_stable_id() FROM PUBLIC;
DROP TRIGGER IF EXISTS post_stable_id ON social.post;
CREATE TRIGGER post_stable_id BEFORE UPDATE OF id ON social.post FOR EACH ROW EXECUTE FUNCTION authz.guard_stable_id();
DROP TRIGGER IF EXISTS community_stable_id ON social.community;
CREATE TRIGGER community_stable_id BEFORE UPDATE OF id ON social.community FOR EACH ROW EXECUTE FUNCTION authz.guard_stable_id();
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Live scope rollback requires a reviewed forward migration'; END $$;
-- +goose StatementEnd

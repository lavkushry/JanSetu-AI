-- +goose Up
-- +goose StatementBegin
CREATE TABLE identity.login_flow (
  state_hash bytea PRIMARY KEY CHECK (octet_length(state_hash) = 32),
  browser_hash bytea NOT NULL CHECK (octet_length(browser_hash) = 32),
  nonce_hash bytea NOT NULL CHECK (octet_length(nonce_hash) = 32),
  pkce_verifier text NOT NULL,
  return_path text NOT NULL,
  expires_at timestamptz NOT NULL
);
CREATE INDEX login_flow_expiry_idx ON identity.login_flow(expires_at);
ALTER TABLE identity.session ADD COLUMN auth_method text NOT NULL DEFAULT 'demo'
  CHECK (auth_method IN ('demo','oidc'));
ALTER TABLE identity.session ADD COLUMN provider text;
ALTER TABLE identity.session ADD COLUMN provider_subject text;
ALTER TABLE identity.session ADD CONSTRAINT session_binding_required CHECK (
  (auth_method='oidc' AND provider IS NOT NULL AND provider_subject IS NOT NULL) OR
  (auth_method='demo' AND provider IS NULL AND provider_subject IS NULL));
ALTER TABLE identity.session ADD COLUMN last_seen_at timestamptz NOT NULL DEFAULT now();
CREATE INDEX session_owner_idx ON identity.session(principal_id, created_at DESC)
  WHERE revoked_at IS NULL;
CREATE INDEX account_binding_principal_idx ON identity.account_binding(principal_id);
-- Audit records contain references and action codes, never tokens or provider claims.
CREATE FUNCTION identity.reject_audit_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION 'audit events are append only';
END;
$$;
CREATE TRIGGER audit_append_only BEFORE UPDATE OR DELETE ON infra.audit_event
  FOR EACH ROW EXECUTE FUNCTION identity.reject_audit_mutation();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER audit_append_only ON infra.audit_event;
DROP FUNCTION identity.reject_audit_mutation();
DROP INDEX identity.account_binding_principal_idx;
DROP INDEX identity.session_owner_idx;
ALTER TABLE identity.session DROP CONSTRAINT session_binding_required;
ALTER TABLE identity.session DROP COLUMN last_seen_at, DROP COLUMN auth_method, DROP COLUMN provider, DROP COLUMN provider_subject;
DROP TABLE identity.login_flow;
-- +goose StatementEnd

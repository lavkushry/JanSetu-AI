-- +goose Up
CREATE TABLE vault.account_locator (
 lookup_hash bytea PRIMARY KEY CHECK(octet_length(lookup_hash)=32),
 subject_id uuid NOT NULL UNIQUE REFERENCES vault.subject(id),
 principal_ciphertext bytea NOT NULL,
 key_version text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE vault.audit_event (
 id uuid PRIMARY KEY,
 purpose_code text NOT NULL CHECK(purpose_code IN ('SELF_REPORT_ALIAS_ISSUE','SELF_REPORT_ALIASES')),
 subject_id uuid REFERENCES vault.subject(id),
 action text NOT NULL,
 result_code text NOT NULL CHECK(result_code IN ('ALLOWED','DENIED')),
 field_allowlist text[] NOT NULL,
 trace_id uuid NOT NULL,
 occurred_at timestamptz NOT NULL DEFAULT now()
);
-- +goose StatementBegin
CREATE FUNCTION vault.reject_audit_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'vault audit is append-only'; END $$;
-- +goose StatementEnd
CREATE TRIGGER vault_audit_append_only BEFORE UPDATE OR DELETE ON vault.audit_event FOR EACH ROW EXECUTE FUNCTION vault.reject_audit_mutation();
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Encrypted locator rollback requires a reviewed forward migration'; END $$;
-- +goose StatementEnd

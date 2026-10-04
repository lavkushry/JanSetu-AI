-- Generated from the approved v3 reference schema; maintained as a versioned migration.
-- +goose Up
-- +goose StatementBegin
CREATE SCHEMA vault;

CREATE TABLE vault.subject (
  id uuid PRIMARY KEY,
  identity_ciphertext bytea,
  contact_ciphertext bytea,
  key_reference text,
  safe_contact_policy jsonb NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  retention_policy_id text NOT NULL,
  CHECK (
    (identity_ciphertext IS NULL AND contact_ciphertext IS NULL)
    OR key_reference IS NOT NULL
  )
);

CREATE TABLE vault.pseudonym_binding (
  alias_id uuid PRIMARY KEY,
  subject_id uuid NOT NULL REFERENCES vault.subject(id),
  scope_kind text NOT NULL CHECK (scope_kind IN ('REPORT','CASE')),
  scope_id uuid NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (subject_id, scope_kind, scope_id)
);

CREATE TABLE vault.principal_subject (
  principal_ref uuid PRIMARY KEY,
  subject_id uuid NOT NULL REFERENCES vault.subject(id),
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE vault.access_grant (
  id uuid PRIMARY KEY,
  principal_ref uuid NOT NULL,
  resource_kind text NOT NULL CHECK (resource_kind IN ('REPORT','CASE','SUBJECT')),
  resource_id uuid NOT NULL,
  purpose_code text NOT NULL,
  field_allowlist text[] NOT NULL,
  approved_by uuid NOT NULL,
  issued_at timestamptz NOT NULL,
  expires_at timestamptz NOT NULL,
  revoked_at timestamptz,
  CHECK (principal_ref <> approved_by),
  CHECK (expires_at > issued_at)
);

CREATE TABLE vault.disclosure_request (
  id uuid PRIMARY KEY,
  request_document_key text NOT NULL,
  requesting_body text NOT NULL,
  claimed_legal_basis text NOT NULL,
  requested_scope jsonb NOT NULL,
  approved_scope jsonb,
  state text NOT NULL CHECK
    (state IN ('RECEIVED','VALIDATING','CHALLENGED','APPROVED',
               'REJECTED','FULFILLED','CLOSED')),
  legal_reviewer_ref uuid,
  independent_reviewer_ref uuid,
  received_at timestamptz NOT NULL,
  fulfilled_at timestamptz,
  CHECK (legal_reviewer_ref IS NULL OR independent_reviewer_ref IS NULL
         OR legal_reviewer_ref <> independent_reviewer_ref)
);

-- +goose StatementEnd

-- +goose Down
SELECT 1;

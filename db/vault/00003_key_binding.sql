-- +goose Up
CREATE TABLE vault.key_configuration (
 singleton boolean PRIMARY KEY DEFAULT true CHECK(singleton),
 key_version text NOT NULL,
 fingerprint bytea NOT NULL CHECK(octet_length(fingerprint)=32)
);
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Vault key configuration rollback requires a reviewed forward migration'; END $$;
-- +goose StatementEnd

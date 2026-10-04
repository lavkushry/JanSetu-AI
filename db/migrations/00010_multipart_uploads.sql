-- +goose Up
-- Existing sessions remain single-part. New fingerprinted allocations use bounded parts.
ALTER TABLE social.media_asset ADD COLUMN client_upload_id uuid;
CREATE UNIQUE INDEX media_client_upload_identity ON social.media_asset(report_alias_ref,client_upload_id) WHERE client_upload_id IS NOT NULL;
ALTER TABLE infra.upload_session ADD COLUMN expected_sha256 bytea CHECK(expected_sha256 IS NULL OR octet_length(expected_sha256)=32);
CREATE TABLE infra.upload_part (
 upload_id uuid NOT NULL REFERENCES infra.upload_session(id),
 part_number integer NOT NULL CHECK(part_number BETWEEN 1 AND 5),
 byte_count bigint NOT NULL CHECK(byte_count BETWEEN 1 AND 2097152),
 token_hash bytea CHECK(token_hash IS NULL OR octet_length(token_hash)=32),
 etag text CHECK(etag IS NULL OR etag ~ '^[a-f0-9]{64}$'),
 PRIMARY KEY(upload_id,part_number)
);
ALTER TABLE infra.upload_part ENABLE ROW LEVEL SECURITY;
ALTER TABLE infra.upload_part FORCE ROW LEVEL SECURITY;
CREATE POLICY upload_part_owner ON infra.upload_part TO js_media
 USING(EXISTS(SELECT FROM infra.upload_session u WHERE u.id=upload_id AND authz.media_owner(u.media_id)))
 WITH CHECK(EXISTS(SELECT FROM infra.upload_session u WHERE u.id=upload_id AND authz.media_owner(u.media_id)));
CREATE POLICY upload_part_worker ON infra.upload_part TO js_media_worker USING(true) WITH CHECK(true);

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Multipart evidence migration requires a reviewed forward migration'; END $$;
-- +goose StatementEnd

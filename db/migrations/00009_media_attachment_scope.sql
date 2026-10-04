-- +goose Up
ALTER TABLE social.media_asset ADD COLUMN storage_cleanup_at timestamptz;
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION authz.media_attachable(mid uuid,rid uuid) RETURNS boolean LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $$
DECLARE alias uuid; media_state text;
BEGIN
 SELECT report_alias_ref,state INTO alias,media_state FROM social.media_asset WHERE id=mid AND authz.owns_alias(report_alias_ref) FOR UPDATE;
 RETURN coalesce(media_state='APPROVED' AND authz.owns_alias(alias) AND EXISTS(SELECT FROM ops.report WHERE id=rid AND reporter_ref=alias),false);
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Private media rollback requires a reviewed forward migration'; END $$;
-- +goose StatementEnd

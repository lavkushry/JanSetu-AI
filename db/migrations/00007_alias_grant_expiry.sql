-- +goose Up
-- The alias grant TTL is checked at every statement, including when a
-- database transaction started before the grant expired.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION authz.owns_alias(alias uuid) RETURNS boolean LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $$
DECLARE claim text := current_setting('jansetu.vault_claim',true); body jsonb; signature text := current_setting('jansetu.vault_signature',true);
BEGIN
 IF authz.principal() IS NULL OR claim IS NULL OR claim='' OR signature IS NULL THEN RETURN false; END IF;
 IF NOT EXISTS(SELECT FROM authz.vault_key WHERE encode(public.hmac(convert_to(claim,'UTF8'),signing_key,'sha256'),'hex')=signature) THEN RETURN false; END IF;
 body := claim::jsonb;
 RETURN body->>'sessionHash'=current_setting('jansetu.session_hash',true) AND (body->>'expiresAt')::bigint>extract(epoch FROM statement_timestamp()) AND body->'aliases' ? alias::text;
EXCEPTION WHEN invalid_text_representation OR numeric_value_out_of_range THEN RETURN false;
END $$;
-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Alias grant expiry rollback requires a reviewed forward migration'; END $$;
-- +goose StatementEnd

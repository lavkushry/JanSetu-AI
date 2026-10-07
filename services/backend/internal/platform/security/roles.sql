-- Cluster bootstrap; run with the migration administrator, never a runtime login.
DO $$
DECLARE r text;
BEGIN
 FOREACH r IN ARRAY ARRAY['js_auth','js_social','js_ops','js_publication','js_worker','js_vault','js_vault_auth','js_media','js_media_worker','js_recommendation_stream'] LOOP
  IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname=r) THEN
   EXECUTE format('CREATE ROLE %I LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS PASSWORD %L',r,r||'-local');
  END IF;
 END LOOP;
END $$;

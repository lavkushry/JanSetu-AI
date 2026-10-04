-- +goose Up
-- Reading publication preference must not give the operations credential the
-- publisher's authority to read original report statements.
DROP POLICY report_read ON ops.report;
CREATE POLICY report_read ON ops.report FOR SELECT TO js_ops USING(authz.has_role('COORDINATOR') OR authz.owns_alias(reporter_ref));
CREATE POLICY report_publication_check ON ops.report FOR SELECT TO js_publication USING(authz.has_role('PUBLISHER'));
ALTER DEFAULT PRIVILEGES IN SCHEMA authz REVOKE EXECUTE ON FUNCTIONS FROM PUBLIC;
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Publication report scope rollback requires a reviewed forward migration'; END $$;
-- +goose StatementEnd

-- +goose Up
-- Existing tasks keep their identity, work history and verification state.
ALTER TABLE ops.obligation ADD COLUMN scope_text text NOT NULL DEFAULT ''
 CHECK (char_length(scope_text) <= 1000);
ALTER TABLE ops.obligation ADD COLUMN client_task_id uuid;
ALTER TABLE ops.obligation ADD COLUMN created_at timestamptz NOT NULL DEFAULT now();
CREATE UNIQUE INDEX obligation_client_task ON ops.obligation(case_id,client_task_id)
 WHERE client_task_id IS NOT NULL;

-- +goose StatementBegin
CREATE FUNCTION ops.guard_task_proposal() RETURNS trigger LANGUAGE plpgsql
 SET search_path=pg_catalog,pg_temp AS $$
BEGIN
 IF OLD.client_task_id IS NOT NULL AND
  (NEW.id IS DISTINCT FROM OLD.id OR NEW.case_id IS DISTINCT FROM OLD.case_id OR
   NEW.agency_id IS DISTINCT FROM OLD.agency_id OR NEW.scope_text IS DISTINCT FROM OLD.scope_text OR
   NEW.client_task_id IS DISTINCT FROM OLD.client_task_id OR
   NEW.required_for_restoration IS DISTINCT FROM OLD.required_for_restoration) THEN
  RAISE EXCEPTION 'Task proposal identity and scope are immutable' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION ops.guard_task_proposal() FROM PUBLIC;
CREATE TRIGGER task_proposal_identity BEFORE UPDATE ON ops.obligation
 FOR EACH ROW EXECUTE FUNCTION ops.guard_task_proposal();

-- Keep the previous two-column function for older API versions.
-- +goose StatementBegin
CREATE FUNCTION authz.owner_restoration_tasks(rid uuid)
 RETURNS TABLE(agency_name text,state text,required_for_restoration boolean)
 LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $$
 SELECT a.name,o.state,o.required_for_restoration FROM ops.intake_review ir
 JOIN ops.obligation o ON o.case_id=ir.case_id LEFT JOIN ops.agency a ON a.id=o.agency_id
 WHERE ir.report_id=rid AND authz.owns_report(rid) ORDER BY o.created_at,o.id
$$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION authz.owner_restoration_tasks(uuid) FROM PUBLIC;

-- +goose Down
-- Do not discard task identities or scope after operators start using them.
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT FROM ops.obligation WHERE client_task_id IS NOT NULL OR scope_text <> '') THEN
  RAISE EXCEPTION 'Restoration task data requires a reviewed forward migration';
 END IF;
END $$;
-- +goose StatementEnd
DROP INDEX ops.obligation_client_task;
DROP FUNCTION authz.owner_restoration_tasks(uuid);
DROP TRIGGER task_proposal_identity ON ops.obligation;
DROP FUNCTION ops.guard_task_proposal();
ALTER TABLE ops.obligation DROP COLUMN scope_text, DROP COLUMN client_task_id, DROP COLUMN created_at;

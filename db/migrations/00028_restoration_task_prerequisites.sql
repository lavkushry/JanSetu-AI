-- +goose Up
-- Existing tasks have no prerequisites and retain their current work/history.
CREATE TABLE ops.task_prerequisite (
 case_id uuid NOT NULL,
 task_id uuid NOT NULL,
 prerequisite_task_id uuid NOT NULL,
 PRIMARY KEY (task_id,prerequisite_task_id),
 CHECK (task_id <> prerequisite_task_id),
 FOREIGN KEY (case_id,task_id) REFERENCES ops.obligation(case_id,id),
 FOREIGN KEY (case_id,prerequisite_task_id) REFERENCES ops.obligation(case_id,id)
);
CREATE INDEX task_prerequisite_case ON ops.task_prerequisite(case_id,task_id);
ALTER TABLE ops.task_prerequisite ENABLE ROW LEVEL SECURITY;
ALTER TABLE ops.task_prerequisite FORCE ROW LEVEL SECURITY;
CREATE POLICY prerequisite_read ON ops.task_prerequisite FOR SELECT USING(authz.case_access(case_id));
CREATE POLICY prerequisite_insert ON ops.task_prerequisite FOR INSERT WITH CHECK(authz.has_role('COORDINATOR'));

-- Serialize graph checks, reject cycles and prevent rewriting the agreed sequence.
-- +goose StatementBegin
CREATE FUNCTION ops.guard_task_prerequisite() RETURNS trigger LANGUAGE plpgsql
 SET search_path=pg_catalog,pg_temp AS $$
BEGIN
 IF TG_OP <> 'INSERT' THEN
  RAISE EXCEPTION 'Task prerequisites are immutable' USING ERRCODE='23514';
 END IF;
 PERFORM 1 FROM ops.case_record WHERE id=NEW.case_id FOR UPDATE;
 IF NOT EXISTS(SELECT FROM ops.obligation WHERE id=NEW.task_id AND case_id=NEW.case_id
   AND state='PROPOSED' AND required_for_restoration AND obligation_type='RESTORATION') OR
    NOT EXISTS(SELECT FROM ops.obligation WHERE id=NEW.prerequisite_task_id AND case_id=NEW.case_id
   AND state <> 'CANCELLED' AND required_for_restoration AND obligation_type='RESTORATION') THEN
  RAISE EXCEPTION 'Prerequisites require proposed restoration work and eligible same-case tasks' USING ERRCODE='23514';
 END IF;
 IF EXISTS (
  WITH RECURSIVE ancestors(id) AS (
   SELECT NEW.prerequisite_task_id
   UNION
   SELECT p.prerequisite_task_id FROM ops.task_prerequisite p JOIN ancestors a ON p.task_id=a.id
   WHERE p.case_id=NEW.case_id
  ) SELECT FROM ancestors WHERE id=NEW.task_id
 ) THEN
  RAISE EXCEPTION 'Task prerequisites cannot form a cycle' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION ops.guard_task_prerequisite() FROM PUBLIC;
CREATE TRIGGER task_prerequisite_identity BEFORE INSERT OR UPDATE OR DELETE ON ops.task_prerequisite
 FOR EACH ROW EXECUTE FUNCTION ops.guard_task_prerequisite();

-- Defense in depth: old binaries/direct SQL cannot bypass the work gate.
-- +goose StatementBegin
CREATE FUNCTION ops.guard_prerequisite_work() RETURNS trigger LANGUAGE plpgsql
 SET search_path=pg_catalog,pg_temp AS $$
BEGIN
 IF NEW.state IN ('IN_PROGRESS','COMPLETION_CLAIMED','VERIFIED') AND EXISTS(
  SELECT FROM ops.task_prerequisite p JOIN ops.obligation o ON o.id=p.prerequisite_task_id
  WHERE p.task_id=NEW.id AND o.state <> 'VERIFIED'
 ) THEN
  RAISE EXCEPTION 'Work requires independent verification of every prerequisite' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION ops.guard_prerequisite_work() FROM PUBLIC;
CREATE TRIGGER prerequisite_work_gate BEFORE UPDATE ON ops.obligation
 FOR EACH ROW EXECUTE FUNCTION ops.guard_prerequisite_work();

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT FROM ops.task_prerequisite) THEN
  RAISE EXCEPTION 'Task prerequisites require a reviewed forward migration';
 END IF;
END $$;
-- +goose StatementEnd
DROP TRIGGER prerequisite_work_gate ON ops.obligation;
DROP FUNCTION ops.guard_prerequisite_work();
DROP TABLE ops.task_prerequisite;
DROP FUNCTION ops.guard_task_prerequisite();

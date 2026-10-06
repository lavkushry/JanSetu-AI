-- +goose Up
-- A split records the original scope and a reviewed, complete replacement pair.
ALTER TABLE ops.obligation ADD CONSTRAINT obligation_parent_same_case
 FOREIGN KEY(case_id,parent_obligation_id) REFERENCES ops.obligation(case_id,id);
CREATE INDEX obligation_parent_idx ON ops.obligation(parent_obligation_id);
CREATE TABLE ops.task_split_request (
 id uuid PRIMARY KEY,
 case_id uuid NOT NULL,
 task_id uuid NOT NULL,
 client_request_id uuid NOT NULL,
 proposer_ref uuid NOT NULL REFERENCES identity.principal(id),
 task_version bigint NOT NULL CHECK(task_version>0),
 accepted_scope text NOT NULL CHECK(char_length(accepted_scope) BETWEEN 10 AND 1000),
 remaining_scope text NOT NULL CHECK(char_length(remaining_scope) BETWEEN 10 AND 1000),
 authority_basis_ref text NOT NULL CHECK(authority_basis_ref='synthetic-local-mandate-v1'),
 reason text NOT NULL CHECK(char_length(reason) BETWEEN 10 AND 2000),
 state text NOT NULL DEFAULT 'PENDING' CHECK(state IN ('PENDING','APPROVED','REJECTED')),
 reviewer_ref uuid REFERENCES identity.principal(id),
 decision_reason text,
 accepted_task_id uuid,
 remaining_task_id uuid,
 remaining_agency_id uuid REFERENCES ops.agency(id),
 created_at timestamptz NOT NULL DEFAULT now(),
 reviewed_at timestamptz,
 UNIQUE(task_id,client_request_id),
 FOREIGN KEY(case_id,task_id) REFERENCES ops.obligation(case_id,id),
 FOREIGN KEY(case_id,accepted_task_id) REFERENCES ops.obligation(case_id,id),
 FOREIGN KEY(case_id,remaining_task_id) REFERENCES ops.obligation(case_id,id),
 CHECK(lower(accepted_scope)<>lower(remaining_scope)),
 CHECK(reviewer_ref IS NULL OR reviewer_ref<>proposer_ref),
 CHECK((state='PENDING' AND reviewer_ref IS NULL AND decision_reason IS NULL AND reviewed_at IS NULL)
  OR (state<>'PENDING' AND reviewer_ref IS NOT NULL AND char_length(decision_reason) BETWEEN 10 AND 2000 AND reviewed_at IS NOT NULL)),
 CHECK((state='APPROVED' AND accepted_task_id IS NOT NULL AND remaining_task_id IS NOT NULL
   AND accepted_task_id<>remaining_task_id AND remaining_agency_id IS NOT NULL)
  OR (state<>'APPROVED' AND accepted_task_id IS NULL AND remaining_task_id IS NULL AND remaining_agency_id IS NULL))
);
CREATE UNIQUE INDEX task_split_pending ON ops.task_split_request(task_id) WHERE state='PENDING';
CREATE UNIQUE INDEX task_split_approved ON ops.task_split_request(task_id) WHERE state='APPROVED';
CREATE INDEX task_split_case_idx ON ops.task_split_request(case_id,created_at,id);
ALTER TABLE ops.task_split_request ENABLE ROW LEVEL SECURITY;
ALTER TABLE ops.task_split_request FORCE ROW LEVEL SECURITY;
CREATE POLICY split_read ON ops.task_split_request FOR SELECT USING(authz.case_access(case_id));
CREATE POLICY split_insert ON ops.task_split_request FOR INSERT WITH CHECK(
 proposer_ref=authz.principal() AND state='PENDING' AND EXISTS(
 SELECT FROM ops.obligation o WHERE o.id=task_id AND authz.agency(o.agency_id)));
CREATE POLICY split_update ON ops.task_split_request FOR UPDATE USING(authz.has_role('COORDINATOR'))
 WITH CHECK(authz.has_role('COORDINATOR') AND reviewer_ref=authz.principal());

-- +goose StatementBegin
CREATE FUNCTION ops.guard_task_split_request() RETURNS trigger LANGUAGE plpgsql
 SET search_path=pg_catalog,pg_temp AS $$
DECLARE child uuid;
BEGIN
 IF TG_OP='INSERT' THEN
  IF NEW.state<>'PENDING' OR NOT EXISTS(SELECT FROM ops.obligation WHERE id=NEW.task_id
   AND case_id=NEW.case_id AND state='PROPOSED' AND required_for_restoration
   AND obligation_type='RESTORATION' AND version=NEW.task_version) THEN
   RAISE EXCEPTION 'Only proposed required work can request a split' USING ERRCODE='23514';
  END IF;
  RETURN NEW;
 END IF;
 IF OLD.state<>'PENDING' OR NEW.state NOT IN ('APPROVED','REJECTED') OR
  (NEW.id,NEW.case_id,NEW.task_id,NEW.client_request_id,NEW.proposer_ref,NEW.task_version,
   NEW.accepted_scope,NEW.remaining_scope,NEW.authority_basis_ref,NEW.reason,NEW.created_at)
  IS DISTINCT FROM
  (OLD.id,OLD.case_id,OLD.task_id,OLD.client_request_id,OLD.proposer_ref,OLD.task_version,
   OLD.accepted_scope,OLD.remaining_scope,OLD.authority_basis_ref,OLD.reason,OLD.created_at) THEN
  RAISE EXCEPTION 'Split proposal and finalized review are immutable' USING ERRCODE='23514';
 END IF;
 IF NOT EXISTS(SELECT FROM ops.obligation WHERE id=OLD.task_id AND state='PROPOSED'
  AND version=OLD.task_version+1) THEN
  RAISE EXCEPTION 'Split review requires the unchanged pending task' USING ERRCODE='23514';
 END IF;
 IF NEW.state='APPROVED' AND (
  NOT EXISTS(SELECT FROM ops.obligation o JOIN ops.obligation parent ON parent.id=OLD.task_id
   WHERE o.id=NEW.accepted_task_id AND o.parent_obligation_id=OLD.task_id AND o.case_id=OLD.case_id
    AND o.scope_text=OLD.accepted_scope AND o.agency_id=parent.agency_id AND o.required_for_restoration
    AND o.obligation_type='RESTORATION' AND o.state='PROPOSED') OR
  NOT EXISTS(SELECT FROM ops.obligation o WHERE o.id=NEW.remaining_task_id
    AND o.parent_obligation_id=OLD.task_id AND o.case_id=OLD.case_id AND o.scope_text=OLD.remaining_scope
    AND o.agency_id=NEW.remaining_agency_id AND o.required_for_restoration
    AND o.obligation_type='RESTORATION' AND o.state='PROPOSED')) THEN
  RAISE EXCEPTION 'Approved split must preserve both required scopes in this case' USING ERRCODE='23514';
 END IF;
 IF NEW.state='APPROVED' THEN
  FOREACH child IN ARRAY ARRAY[NEW.accepted_task_id,NEW.remaining_task_id] LOOP
   IF EXISTS(SELECT prerequisite_task_id FROM ops.task_prerequisite WHERE task_id=OLD.task_id
      EXCEPT SELECT prerequisite_task_id FROM ops.task_prerequisite WHERE task_id=child) OR
      EXISTS(SELECT prerequisite_task_id FROM ops.task_prerequisite WHERE task_id=child
      EXCEPT SELECT prerequisite_task_id FROM ops.task_prerequisite WHERE task_id=OLD.task_id) OR
      NOT EXISTS(SELECT FROM ops.obligation o JOIN ops.obligation parent ON parent.id=OLD.task_id
       WHERE o.id=child AND o.due_at IS NOT DISTINCT FROM parent.due_at) THEN
    RAISE EXCEPTION 'Both split tasks must inherit prerequisites and the original due date' USING ERRCODE='23514';
   END IF;
  END LOOP;
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION ops.guard_task_split_request() FROM PUBLIC;
CREATE TRIGGER task_split_identity BEFORE INSERT OR UPDATE ON ops.task_split_request
 FOR EACH ROW EXECUTE FUNCTION ops.guard_task_split_request();

-- Enforce the complete atomic result after all confirmation writes have run.
-- +goose StatementBegin
CREATE FUNCTION ops.guard_split_accounting() RETURNS trigger LANGUAGE plpgsql
 SET search_path=pg_catalog,pg_temp AS $$
BEGIN
 IF NOT EXISTS(SELECT FROM ops.obligation WHERE id=NEW.task_id AND state='CANCELLED') OR
  NOT EXISTS(SELECT FROM ops.obligation WHERE id=NEW.accepted_task_id AND state='ACCEPTED') THEN
  RAISE EXCEPTION 'Confirmed split must archive original work and accept its defined part atomically' USING ERRCODE='23514';
 END IF;
 RETURN NULL;
END $$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION ops.guard_split_accounting() FROM PUBLIC;
CREATE CONSTRAINT TRIGGER split_scope_accounting AFTER UPDATE ON ops.task_split_request
 DEFERRABLE INITIALLY DEFERRED FOR EACH ROW WHEN(NEW.state='APPROVED')
 EXECUTE FUNCTION ops.guard_split_accounting();

-- Ordinary cancellation never satisfies restoration. Only a reviewed split can
-- replace its historical parent, and every required descendant must be verified.
-- +goose StatementBegin
CREATE FUNCTION ops.task_restored(oid uuid) RETURNS boolean LANGUAGE sql STABLE
 SET search_path=pg_catalog,pg_temp AS $$
 WITH RECURSIVE work AS (
  SELECT id,state,required_for_restoration,obligation_type FROM ops.obligation WHERE id=oid
  UNION
  SELECT o.id,o.state,o.required_for_restoration,o.obligation_type FROM work w
   JOIN ops.task_split_request s ON s.task_id=w.id AND s.state='APPROVED'
   JOIN ops.obligation o ON o.id=s.accepted_task_id OR o.id=s.remaining_task_id
 ), accounted AS (
  SELECT w.*,EXISTS(SELECT FROM ops.task_split_request s WHERE s.task_id=w.id AND s.state='APPROVED') AS replaced FROM work w
 )
 SELECT count(*) FILTER(WHERE NOT replaced)>0 AND
  COALESCE(bool_and(required_for_restoration AND obligation_type='RESTORATION' AND
   CASE WHEN replaced THEN state='CANCELLED' ELSE state='VERIFIED' END),false) FROM accounted
$$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION ops.task_restored(uuid) FROM PUBLIC;

-- Split children retain historical prerequisite IDs after a reviewed replacement.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION ops.guard_task_prerequisite() RETURNS trigger LANGUAGE plpgsql
 SET search_path=pg_catalog,pg_temp AS $$
BEGIN
 IF TG_OP <> 'INSERT' THEN
  RAISE EXCEPTION 'Task prerequisites are immutable' USING ERRCODE='23514';
 END IF;
 PERFORM 1 FROM ops.case_record WHERE id=NEW.case_id FOR UPDATE;
 IF NOT EXISTS(SELECT FROM ops.obligation WHERE id=NEW.task_id AND case_id=NEW.case_id
   AND state='PROPOSED' AND required_for_restoration AND obligation_type='RESTORATION') OR
    NOT EXISTS(SELECT FROM ops.obligation WHERE id=NEW.prerequisite_task_id AND case_id=NEW.case_id
   AND required_for_restoration AND obligation_type='RESTORATION' AND (state <> 'CANCELLED' OR (
    EXISTS(SELECT FROM ops.task_split_request WHERE task_id=NEW.prerequisite_task_id AND state='APPROVED')
    AND EXISTS(SELECT FROM ops.obligation child JOIN ops.task_prerequisite inherited
      ON inherited.task_id=child.parent_obligation_id WHERE child.id=NEW.task_id
       AND inherited.prerequisite_task_id=NEW.prerequisite_task_id)))) THEN
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

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION ops.guard_prerequisite_work() RETURNS trigger LANGUAGE plpgsql
 SET search_path=pg_catalog,pg_temp AS $$
BEGIN
 IF NEW.state IN ('IN_PROGRESS','COMPLETION_CLAIMED','VERIFIED') AND EXISTS(
  SELECT FROM ops.task_prerequisite p WHERE p.task_id=NEW.id AND NOT ops.task_restored(p.prerequisite_task_id)
 ) THEN
  RAISE EXCEPTION 'Work requires independent verification of every prerequisite' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION ops.guard_split_task_work() RETURNS trigger LANGUAGE plpgsql
 SET search_path=pg_catalog,pg_temp AS $$
BEGIN
 IF OLD.parent_obligation_id IS NOT NULL AND
  (NEW.parent_obligation_id,NEW.case_id,NEW.agency_id,NEW.scope_text,NEW.required_for_restoration,NEW.client_task_id)
  IS DISTINCT FROM
  (OLD.parent_obligation_id,OLD.case_id,OLD.agency_id,OLD.scope_text,OLD.required_for_restoration,OLD.client_task_id) THEN
  RAISE EXCEPTION 'Split work identity and required scope are immutable' USING ERRCODE='23514';
 END IF;
 IF OLD.state='CANCELLED' AND EXISTS(SELECT FROM ops.task_split_request WHERE task_id=OLD.id AND state='APPROVED')
  AND NEW IS DISTINCT FROM OLD THEN
  RAISE EXCEPTION 'Replaced original task is retained as immutable history' USING ERRCODE='23514';
 END IF;
 IF NEW.state='ACCEPTED' AND EXISTS(SELECT FROM ops.task_split_request WHERE task_id=NEW.id AND state='PENDING') THEN
  RAISE EXCEPTION 'A partial acceptance awaits coordinator review' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION ops.guard_split_task_work() FROM PUBLIC;
CREATE TRIGGER split_work_identity BEFORE UPDATE ON ops.obligation
 FOR EACH ROW EXECUTE FUNCTION ops.guard_split_task_work();

-- Residents see current required work; original split scope stays in staff history.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION authz.owner_restoration_tasks(rid uuid)
 RETURNS TABLE(agency_name text,state text,required_for_restoration boolean)
 LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $$
 SELECT a.name,o.state,o.required_for_restoration FROM ops.intake_review ir
 JOIN ops.obligation o ON o.case_id=ir.case_id LEFT JOIN ops.agency a ON a.id=o.agency_id
 WHERE ir.report_id=rid AND authz.owns_report(rid)
 AND NOT EXISTS(SELECT FROM ops.task_split_request s WHERE s.task_id=o.id AND s.state='APPROVED')
 ORDER BY o.created_at,o.id
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT FROM ops.task_split_request) THEN
  RAISE EXCEPTION 'Reviewed split history requires a reviewed forward migration';
 END IF;
END $$;
-- +goose StatementEnd
DROP TRIGGER split_work_identity ON ops.obligation;
DROP FUNCTION ops.guard_split_task_work();
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION ops.guard_task_prerequisite() RETURNS trigger LANGUAGE plpgsql
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
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION ops.guard_prerequisite_work() RETURNS trigger LANGUAGE plpgsql
 SET search_path=pg_catalog,pg_temp AS $$
BEGIN
 IF NEW.state IN ('IN_PROGRESS','COMPLETION_CLAIMED','VERIFIED') AND EXISTS(
  SELECT FROM ops.task_prerequisite p JOIN ops.obligation o ON o.id=p.prerequisite_task_id
  WHERE p.task_id=NEW.id AND o.state<>'VERIFIED'
 ) THEN
  RAISE EXCEPTION 'Work requires independent verification of every prerequisite' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION authz.owner_restoration_tasks(rid uuid)
 RETURNS TABLE(agency_name text,state text,required_for_restoration boolean)
 LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $$
 SELECT a.name,o.state,o.required_for_restoration FROM ops.intake_review ir
 JOIN ops.obligation o ON o.case_id=ir.case_id LEFT JOIN ops.agency a ON a.id=o.agency_id
 WHERE ir.report_id=rid AND authz.owns_report(rid) ORDER BY o.created_at,o.id
$$;
-- +goose StatementEnd
DROP FUNCTION ops.task_restored(uuid);
DROP TABLE ops.task_split_request;
DROP FUNCTION ops.guard_task_split_request();
DROP FUNCTION ops.guard_split_accounting();
DROP INDEX ops.obligation_parent_idx;
ALTER TABLE ops.obligation DROP CONSTRAINT obligation_parent_same_case;

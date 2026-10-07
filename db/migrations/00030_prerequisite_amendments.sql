-- +goose Up
-- Additions are attributed and append-only; original proposal edges stay intact.
CREATE TABLE ops.prerequisite_amendment (
 id uuid PRIMARY KEY,
 case_id uuid NOT NULL,
 task_id uuid NOT NULL,
 client_amendment_id uuid NOT NULL,
 actor_ref uuid NOT NULL REFERENCES identity.principal(id),
 task_version bigint NOT NULL CHECK(task_version>0),
 added_task_ids uuid[] NOT NULL CHECK(cardinality(added_task_ids) BETWEEN 1 AND 7 AND array_position(added_task_ids,NULL) IS NULL),
 reason text NOT NULL CHECK(char_length(reason) BETWEEN 10 AND 2000),
 reviewed boolean NOT NULL CHECK(reviewed),
 created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(task_id,client_amendment_id),
 UNIQUE(case_id,task_id,id),
 FOREIGN KEY(case_id,task_id) REFERENCES ops.obligation(case_id,id)
);
CREATE INDEX prerequisite_amendment_case_idx ON ops.prerequisite_amendment(case_id,created_at,id);
ALTER TABLE ops.prerequisite_amendment ENABLE ROW LEVEL SECURITY;
ALTER TABLE ops.prerequisite_amendment FORCE ROW LEVEL SECURITY;
CREATE POLICY amendment_read ON ops.prerequisite_amendment FOR SELECT USING(authz.case_access(case_id));
CREATE POLICY amendment_insert ON ops.prerequisite_amendment FOR INSERT WITH CHECK(authz.has_role('COORDINATOR') AND actor_ref=authz.principal());
ALTER TABLE ops.task_prerequisite ADD COLUMN amendment_id uuid;
ALTER TABLE ops.task_prerequisite ADD CONSTRAINT prerequisite_amendment_same_task
 FOREIGN KEY(case_id,task_id,amendment_id) REFERENCES ops.prerequisite_amendment(case_id,task_id,id);
-- Finalized scope reviews must also have an explicit non-null reason.
ALTER TABLE ops.task_split_request ADD CONSTRAINT split_review_reason_required
 CHECK(state='PENDING' OR decision_reason IS NOT NULL);

-- Creation time anchors initial edges and cannot be rewritten to bypass auditing.
-- +goose StatementBegin
CREATE FUNCTION ops.guard_task_creation_time() RETURNS trigger LANGUAGE plpgsql
 SET search_path=pg_catalog,pg_temp AS $$
BEGIN
 IF NEW.created_at IS DISTINCT FROM OLD.created_at THEN
  RAISE EXCEPTION 'Task creation time is immutable' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION ops.guard_task_creation_time() FROM PUBLIC;
CREATE TRIGGER task_creation_time BEFORE UPDATE ON ops.obligation
 FOR EACH ROW EXECUTE FUNCTION ops.guard_task_creation_time();

-- +goose StatementBegin
CREATE FUNCTION ops.guard_prerequisite_amendment() RETURNS trigger LANGUAGE plpgsql
 SET search_path=pg_catalog,pg_temp AS $$
DECLARE pid uuid;
BEGIN
 IF TG_OP<>'INSERT' THEN
  RAISE EXCEPTION 'Prerequisite amendment history is immutable' USING ERRCODE='23514';
 END IF;
 PERFORM 1 FROM ops.case_record WHERE id=NEW.case_id FOR UPDATE;
 IF NOT EXISTS(SELECT FROM ops.obligation WHERE id=NEW.task_id AND case_id=NEW.case_id
   AND state='PROPOSED' AND required_for_restoration AND obligation_type='RESTORATION' AND version=NEW.task_version)
  OR NOT EXISTS(SELECT FROM ops.case_record WHERE id=NEW.case_id AND state NOT IN ('RESOLVED','WITHDRAWN'))
  OR EXISTS(SELECT FROM ops.task_split_request WHERE task_id=NEW.task_id AND state='PENDING') THEN
  RAISE EXCEPTION 'Only unchanged proposed work without pending split review can receive additions' USING ERRCODE='23514';
 END IF;
 IF cardinality(NEW.added_task_ids)<>(SELECT count(DISTINCT x) FROM unnest(NEW.added_task_ids) x) THEN
  RAISE EXCEPTION 'Prerequisite additions must be distinct' USING ERRCODE='23514';
 END IF;
 FOREACH pid IN ARRAY NEW.added_task_ids LOOP
  IF pid=NEW.task_id OR NOT EXISTS(SELECT FROM ops.obligation WHERE id=pid AND case_id=NEW.case_id
    AND state<>'CANCELLED' AND required_for_restoration AND obligation_type='RESTORATION')
    OR EXISTS(SELECT FROM ops.task_prerequisite WHERE task_id=NEW.task_id AND prerequisite_task_id=pid) THEN
   RAISE EXCEPTION 'Add new eligible same-case prerequisites only' USING ERRCODE='23514';
  END IF;
 END LOOP;
 RETURN NEW;
END $$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION ops.guard_prerequisite_amendment() FROM PUBLIC;
CREATE TRIGGER prerequisite_amendment_identity BEFORE INSERT OR UPDATE OR DELETE ON ops.prerequisite_amendment
 FOR EACH ROW EXECUTE FUNCTION ops.guard_prerequisite_amendment();

-- +goose StatementBegin
CREATE FUNCTION ops.guard_amendment_accounting() RETURNS trigger LANGUAGE plpgsql
 SET search_path=pg_catalog,pg_temp AS $$
BEGIN
 IF (SELECT count(*) FROM ops.task_prerequisite WHERE amendment_id=NEW.id)<>cardinality(NEW.added_task_ids)
   OR NOT EXISTS(SELECT FROM ops.obligation WHERE id=NEW.task_id AND state='PROPOSED' AND version=NEW.task_version+1) THEN
  RAISE EXCEPTION 'Every recorded addition and task version must commit atomically' USING ERRCODE='23514';
 END IF;
 RETURN NULL;
END $$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION ops.guard_amendment_accounting() FROM PUBLIC;
CREATE CONSTRAINT TRIGGER prerequisite_amendment_accounting AFTER INSERT ON ops.prerequisite_amendment
 DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION ops.guard_amendment_accounting();

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
 IF NEW.amendment_id IS NULL THEN
  IF NOT EXISTS(SELECT FROM ops.obligation WHERE id=NEW.task_id AND created_at=transaction_timestamp()) THEN
   RAISE EXCEPTION 'Later prerequisites require an attributed amendment' USING ERRCODE='23514';
  END IF;
 ELSE
  IF NOT EXISTS(SELECT FROM ops.prerequisite_amendment WHERE id=NEW.amendment_id
   AND task_id=NEW.task_id AND case_id=NEW.case_id AND NEW.prerequisite_task_id=ANY(added_task_ids)
   AND created_at=transaction_timestamp()) THEN
   RAISE EXCEPTION 'Prerequisite must match its newly recorded amendment' USING ERRCODE='23514';
  END IF;
 END IF;
 IF EXISTS (
  WITH RECURSIVE edges(source,target) AS (
   SELECT task_id,prerequisite_task_id FROM ops.task_prerequisite WHERE case_id=NEW.case_id
   UNION ALL SELECT task_id,accepted_task_id FROM ops.task_split_request WHERE case_id=NEW.case_id AND state='APPROVED'
   UNION ALL SELECT task_id,remaining_task_id FROM ops.task_split_request WHERE case_id=NEW.case_id AND state='APPROVED'
  ), ancestors(id) AS (
   SELECT NEW.prerequisite_task_id
   UNION
   SELECT e.target FROM edges e JOIN ancestors a ON e.source=a.id
  ) SELECT FROM ancestors WHERE id=NEW.task_id
 ) THEN
  RAISE EXCEPTION 'Task prerequisites cannot form a cycle' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT FROM ops.prerequisite_amendment) THEN
  RAISE EXCEPTION 'Prerequisite amendment history requires a reviewed forward migration';
 END IF;
END $$;
-- +goose StatementEnd
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
DROP TRIGGER task_creation_time ON ops.obligation;
DROP FUNCTION ops.guard_task_creation_time();
ALTER TABLE ops.task_prerequisite DROP CONSTRAINT prerequisite_amendment_same_task;
ALTER TABLE ops.task_prerequisite DROP COLUMN amendment_id;
DROP TABLE ops.prerequisite_amendment;
DROP FUNCTION ops.guard_prerequisite_amendment();
DROP FUNCTION ops.guard_amendment_accounting();
ALTER TABLE ops.task_split_request DROP CONSTRAINT split_review_reason_required;

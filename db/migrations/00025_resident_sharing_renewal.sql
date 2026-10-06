-- +goose Up
CREATE TABLE ops.publication_sharing_renewal (
 id uuid PRIMARY KEY,
 request_id uuid NOT NULL REFERENCES ops.publication_withdrawal_request(id),
 report_id uuid NOT NULL REFERENCES ops.report(id),
 client_request_id uuid NOT NULL,
 publication_version bigint NOT NULL CHECK(publication_version>0),
 state text NOT NULL DEFAULT 'ACTIVE' CHECK(state IN ('ACTIVE','CANCELLED')),
 version bigint NOT NULL DEFAULT 1 CHECK(version>0),
 created_at timestamptz NOT NULL DEFAULT statement_timestamp(),
 cancelled_at timestamptz,
 UNIQUE(report_id,client_request_id),
 CHECK((state='ACTIVE' AND cancelled_at IS NULL) OR (state='CANCELLED' AND cancelled_at IS NOT NULL))
);
CREATE UNIQUE INDEX sharing_renewal_one_active ON ops.publication_sharing_renewal(request_id) WHERE state='ACTIVE';
CREATE INDEX sharing_renewal_history ON ops.publication_sharing_renewal(request_id,created_at DESC,id DESC);
-- Permission refers to one approved withdrawal. A later approved withdrawal
-- requires its own fresh owner permission; older permissions become history.
-- +goose StatementBegin
CREATE FUNCTION authz.sharing_renewal_target(rid uuid,report uuid,revision bigint) RETURNS boolean
 LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $$
 SELECT authz.owns_report(report) AND EXISTS(
 SELECT FROM ops.publication_withdrawal_request w JOIN ops.report s ON s.id=w.report_id
 JOIN ops.intake_review i ON i.report_id=s.id JOIN ops.publication_binding b ON b.case_id=i.case_id
 JOIN social.case_receipt p ON p.id=b.receipt_id
 WHERE w.id=rid AND w.report_id=report AND w.state='APPROVED' AND s.publication_preference='SANITIZED_RECEIPT'
 AND w.case_id=i.case_id AND w.receipt_id=p.id AND p.publication_state='WITHDRAWN' AND p.publication_version=revision
 AND NOT EXISTS(SELECT FROM ops.publication_withdrawal_request newer WHERE newer.report_id=w.report_id AND newer.state='APPROVED' AND ROW(newer.created_at,newer.id)>ROW(w.created_at,w.id)))
$$;
CREATE FUNCTION authz.sharing_renewal_allowed(rid uuid,report uuid,revision bigint) RETURNS boolean
 LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $$
 SELECT authz.sharing_renewal_target(rid,report,revision)
 AND NOT EXISTS(SELECT FROM ops.publication_sharing_renewal n WHERE n.request_id=rid AND n.state='ACTIVE')
$$;
CREATE FUNCTION authz.guard_sharing_renewal() RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $$
DECLARE receipt uuid; current_state text; current_version bigint;
BEGIN
 SELECT w.receipt_id INTO receipt FROM ops.publication_withdrawal_request w WHERE w.id=NEW.request_id FOR UPDATE;
 SELECT p.publication_state,p.publication_version INTO current_state,current_version FROM social.case_receipt p WHERE p.id=receipt FOR UPDATE;
 IF TG_OP='INSERT' THEN
  IF current_state IS DISTINCT FROM 'WITHDRAWN' OR current_version IS DISTINCT FROM NEW.publication_version
  OR NOT authz.sharing_renewal_allowed(NEW.request_id,NEW.report_id,NEW.publication_version)
  THEN RAISE EXCEPTION 'Sharing renewal requires current owner permission' USING ERRCODE='23514'; END IF;
 ELSE
  IF ROW(NEW.id,NEW.request_id,NEW.report_id,NEW.client_request_id,NEW.publication_version,NEW.created_at)
  IS DISTINCT FROM ROW(OLD.id,OLD.request_id,OLD.report_id,OLD.client_request_id,OLD.publication_version,OLD.created_at)
  OR OLD.state<>'ACTIVE' OR NEW.state<>'CANCELLED' OR NEW.version<>OLD.version+1
  OR current_state IS DISTINCT FROM 'WITHDRAWN'
  OR NOT authz.sharing_renewal_target(OLD.request_id,OLD.report_id,current_version)
  THEN RAISE EXCEPTION 'Immutable sharing renewal or invalid cancellation' USING ERRCODE='23514'; END IF;
  NEW.cancelled_at=statement_timestamp();
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER sharing_renewal_guard BEFORE INSERT OR UPDATE ON ops.publication_sharing_renewal FOR EACH ROW EXECUTE FUNCTION authz.guard_sharing_renewal();
-- Keep the original PRIVATE preference as a permanent independent veto.
CREATE OR REPLACE FUNCTION authz.withdrawal_request_allowed(rid uuid,cid uuid,receipt uuid,revision bigint) RETURNS boolean
 LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $$
 SELECT authz.owns_report(rid)
 AND EXISTS(SELECT FROM ops.report r JOIN ops.intake_review i ON i.report_id=r.id
 JOIN ops.publication_binding b ON b.case_id=i.case_id JOIN social.case_receipt p ON p.id=b.receipt_id
 WHERE r.id=rid AND r.publication_preference='SANITIZED_RECEIPT' AND i.case_id=cid AND p.id=receipt
 AND p.publication_state='PUBLISHED' AND p.publication_version=revision)
 AND NOT EXISTS(SELECT FROM ops.publication_withdrawal_request w WHERE w.report_id=rid
 AND (w.state='REQUESTED' OR (w.state='APPROVED' AND NOT EXISTS(SELECT FROM ops.publication_withdrawal_request newer WHERE newer.report_id=w.report_id AND newer.case_id=w.case_id AND newer.state='APPROVED' AND ROW(newer.created_at,newer.id)>ROW(w.created_at,w.id)) AND NOT EXISTS(SELECT FROM ops.publication_sharing_renewal n WHERE n.request_id=w.id AND n.state='ACTIVE'))))
$$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION authz.sharing_renewal_target(uuid,uuid,bigint),authz.sharing_renewal_allowed(uuid,uuid,bigint),authz.guard_sharing_renewal() FROM PUBLIC;
ALTER TABLE ops.publication_sharing_renewal ENABLE ROW LEVEL SECURITY;
ALTER TABLE ops.publication_sharing_renewal FORCE ROW LEVEL SECURITY;
CREATE POLICY sharing_renewal_owner_read ON ops.publication_sharing_renewal FOR SELECT TO js_ops USING(authz.owns_report(report_id));
CREATE POLICY sharing_renewal_owner_insert ON ops.publication_sharing_renewal FOR INSERT TO js_ops WITH CHECK(
 state='ACTIVE' AND version=1 AND cancelled_at IS NULL AND authz.sharing_renewal_target(request_id,report_id,publication_version));
CREATE POLICY sharing_renewal_owner_cancel ON ops.publication_sharing_renewal FOR UPDATE TO js_ops USING(authz.owns_report(report_id))
 WITH CHECK(authz.owns_report(report_id) AND state='CANCELLED');
CREATE VIEW ops.publication_sharing_renewal_review WITH(security_barrier=true) AS
 SELECT w.id AS request_id,w.report_id,p.publication_version,
 authz.sharing_renewal_allowed(w.id,w.report_id,p.publication_version) AS can_renew,
 n.id AS renewal_id,n.state AS renewal_state,n.version AS renewal_version,n.publication_version AS renewal_publication_version,
 n.created_at AS renewed_at,n.cancelled_at AS renewal_cancelled_at,
 (n.state='ACTIVE' AND authz.sharing_renewal_target(w.id,w.report_id,p.publication_version)) AS can_undo
 FROM ops.publication_withdrawal_request w JOIN ops.report s ON s.id=w.report_id
 JOIN ops.intake_review i ON i.report_id=s.id JOIN ops.publication_binding b ON b.case_id=i.case_id
 JOIN social.case_receipt p ON p.id=b.receipt_id
 LEFT JOIN LATERAL(SELECT * FROM ops.publication_sharing_renewal x WHERE x.request_id=w.id ORDER BY x.created_at DESC,x.id DESC LIMIT 1) n ON true
 WHERE w.state='APPROVED' AND s.publication_preference='SANITIZED_RECEIPT' AND w.case_id=i.case_id AND w.receipt_id=p.id AND authz.owns_report(w.report_id);
CREATE OR REPLACE VIEW ops.publication_sharing_eligibility WITH(security_barrier=true) AS
 SELECT c.id AS case_id,NOT EXISTS(SELECT FROM ops.case_observation o JOIN ops.report r ON r.id=o.report_id WHERE o.case_id=c.id AND r.publication_preference='PRIVATE')
 AND NOT EXISTS(SELECT FROM ops.publication_withdrawal_request w WHERE w.case_id=c.id
 AND (w.state='REQUESTED' OR (w.state='APPROVED' AND NOT EXISTS(SELECT FROM ops.publication_withdrawal_request newer WHERE newer.report_id=w.report_id AND newer.case_id=w.case_id AND newer.state='APPROVED' AND ROW(newer.created_at,newer.id)>ROW(w.created_at,w.id)) AND NOT EXISTS(SELECT FROM ops.publication_sharing_renewal n WHERE n.request_id=w.id AND n.state='ACTIVE')))) AS allowed
 FROM ops.case_record c WHERE authz.has_role('PUBLISHER') AND authz.case_access(c.id);
REVOKE ALL ON ops.publication_sharing_renewal_review FROM PUBLIC;

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Retained resident sharing permission requires a reviewed forward migration'; END $$;
-- +goose StatementEnd

-- +goose Up
CREATE TABLE ops.publication_withdrawal_request (
 id uuid PRIMARY KEY,
 report_id uuid NOT NULL REFERENCES ops.report(id),
 case_id uuid NOT NULL REFERENCES ops.case_record(id),
 receipt_id uuid NOT NULL REFERENCES social.case_receipt(id),
 client_request_id uuid NOT NULL,
 publication_version bigint NOT NULL CHECK(publication_version>0),
 reason_code text NOT NULL CHECK(reason_code IN ('PRIVACY','LOCATION','SHARING_PREFERENCE')),
 state text NOT NULL DEFAULT 'REQUESTED' CHECK(state IN ('REQUESTED','APPROVED','DECLINED','CANCELLED')),
 version bigint NOT NULL DEFAULT 1 CHECK(version>0),
 created_at timestamptz NOT NULL DEFAULT statement_timestamp(),
 UNIQUE(report_id,client_request_id)
);
CREATE UNIQUE INDEX publication_withdrawal_snapshot_once ON ops.publication_withdrawal_request(report_id,receipt_id,publication_version) WHERE state<>'CANCELLED';
CREATE UNIQUE INDEX publication_withdrawal_one_open ON ops.publication_withdrawal_request(report_id) WHERE state='REQUESTED';
CREATE INDEX publication_withdrawal_queue ON ops.publication_withdrawal_request(state,created_at,id);
CREATE TABLE ops.publication_withdrawal_decision (
 id uuid PRIMARY KEY,
 request_id uuid NOT NULL UNIQUE REFERENCES ops.publication_withdrawal_request(id),
 result text NOT NULL CHECK(result IN ('APPROVED','DECLINED')),
 case_version bigint NOT NULL CHECK(case_version>0),
 publication_version bigint NOT NULL CHECK(publication_version>0),
 internal_reason text NOT NULL CHECK(length(btrim(internal_reason)) BETWEEN 5 AND 1000),
 resident_reason text NOT NULL CHECK(length(btrim(resident_reason)) BETWEEN 5 AND 1000),
 reviewer_ref uuid NOT NULL REFERENCES identity.principal(id),
 decided_at timestamptz NOT NULL DEFAULT statement_timestamp()
);
CREATE TABLE ops.publication_withdrawal_cancel (
 request_id uuid PRIMARY KEY REFERENCES ops.publication_withdrawal_request(id),
 created_at timestamptz NOT NULL DEFAULT statement_timestamp()
);
-- Private owner scope still requires the independently validated vault claim.
-- Publishers receive no report ID, alias, principal, statement or free-text concern.
-- +goose StatementBegin
CREATE FUNCTION authz.withdrawal_request_allowed(rid uuid,cid uuid,receipt uuid,revision bigint) RETURNS boolean
 LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $$
 SELECT authz.owns_report(rid)
 AND EXISTS(SELECT FROM ops.report r JOIN ops.intake_review i ON i.report_id=r.id
 JOIN ops.publication_binding b ON b.case_id=i.case_id JOIN social.case_receipt p ON p.id=b.receipt_id
 WHERE r.id=rid AND r.publication_preference='SANITIZED_RECEIPT' AND i.case_id=cid AND p.id=receipt
 AND p.publication_state='PUBLISHED' AND p.publication_version=revision)
 AND NOT EXISTS(SELECT FROM ops.publication_withdrawal_request w WHERE w.report_id=rid AND w.state IN ('REQUESTED','APPROVED'))
$$;
-- Outcome inserts lock the canonical request, including direct scoped SQL,
-- so a cancellation marker and publisher decision cannot both commit.
CREATE FUNCTION authz.guard_withdrawal_outcome() RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $$
DECLARE current_state text;
BEGIN
 SELECT w.state INTO current_state FROM ops.publication_withdrawal_request w WHERE w.id=NEW.request_id FOR UPDATE;
 IF current_state IS DISTINCT FROM 'REQUESTED'
 OR EXISTS(SELECT FROM ops.publication_withdrawal_cancel x WHERE x.request_id=NEW.request_id)
 OR EXISTS(SELECT FROM ops.publication_withdrawal_decision d WHERE d.request_id=NEW.request_id)
 THEN RAISE EXCEPTION 'Withdrawal outcome already recorded' USING ERRCODE='23514'; END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER withdrawal_cancel_once BEFORE INSERT ON ops.publication_withdrawal_cancel FOR EACH ROW EXECUTE FUNCTION authz.guard_withdrawal_outcome();
CREATE TRIGGER withdrawal_decision_once BEFORE INSERT ON ops.publication_withdrawal_decision FOR EACH ROW EXECUTE FUNCTION authz.guard_withdrawal_outcome();
CREATE FUNCTION authz.guard_withdrawal_request() RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $$
BEGIN
 IF ROW(NEW.id,NEW.report_id,NEW.case_id,NEW.receipt_id,NEW.client_request_id,NEW.publication_version,NEW.reason_code,NEW.created_at)
 IS DISTINCT FROM ROW(OLD.id,OLD.report_id,OLD.case_id,OLD.receipt_id,OLD.client_request_id,OLD.publication_version,OLD.reason_code,OLD.created_at)
 OR OLD.state<>'REQUESTED' OR NEW.version<>OLD.version+1
 OR NOT ((NEW.state IN ('APPROVED','DECLINED') AND EXISTS(SELECT FROM ops.publication_withdrawal_decision d WHERE d.request_id=OLD.id AND d.result=NEW.state))
 OR (NEW.state='CANCELLED' AND EXISTS(SELECT FROM ops.publication_withdrawal_cancel x WHERE x.request_id=OLD.id)))
 OR (NEW.state='APPROVED' AND NOT EXISTS(SELECT FROM social.case_receipt r WHERE r.id=OLD.receipt_id AND r.publication_state='WITHDRAWN'))
 THEN RAISE EXCEPTION 'Immutable withdrawal request or invalid review transition' USING ERRCODE='23514'; END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION authz.guard_withdrawal_outcome() FROM PUBLIC;
REVOKE ALL ON FUNCTION authz.withdrawal_request_allowed(uuid,uuid,uuid,bigint),authz.guard_withdrawal_request() FROM PUBLIC;
CREATE TRIGGER publication_withdrawal_request_guard BEFORE UPDATE ON ops.publication_withdrawal_request FOR EACH ROW EXECUTE FUNCTION authz.guard_withdrawal_request();
ALTER TABLE ops.publication_withdrawal_request ENABLE ROW LEVEL SECURITY;
ALTER TABLE ops.publication_withdrawal_request FORCE ROW LEVEL SECURITY;
CREATE POLICY withdrawal_owner_read ON ops.publication_withdrawal_request FOR SELECT TO js_ops USING(authz.owns_report(report_id));
CREATE POLICY withdrawal_owner_insert ON ops.publication_withdrawal_request FOR INSERT TO js_ops WITH CHECK(
 state='REQUESTED' AND version=1 AND authz.withdrawal_request_allowed(report_id,case_id,receipt_id,publication_version));
CREATE POLICY withdrawal_publisher_read ON ops.publication_withdrawal_request FOR SELECT TO js_publication USING(authz.has_role('PUBLISHER') AND authz.case_access(case_id));
CREATE POLICY withdrawal_publisher_update ON ops.publication_withdrawal_request FOR UPDATE TO js_publication USING(authz.has_role('PUBLISHER') AND authz.case_access(case_id)) WITH CHECK(authz.has_role('PUBLISHER') AND authz.case_access(case_id));
CREATE POLICY withdrawal_owner_cancel ON ops.publication_withdrawal_request FOR UPDATE TO js_ops USING(authz.owns_report(report_id)) WITH CHECK(authz.owns_report(report_id) AND state='CANCELLED');
ALTER TABLE ops.publication_withdrawal_cancel ENABLE ROW LEVEL SECURITY;
ALTER TABLE ops.publication_withdrawal_cancel FORCE ROW LEVEL SECURITY;
CREATE POLICY withdrawal_cancel_insert ON ops.publication_withdrawal_cancel FOR INSERT TO js_ops WITH CHECK(
 EXISTS(SELECT FROM ops.publication_withdrawal_request w WHERE w.id=request_id AND w.state='REQUESTED' AND authz.owns_report(w.report_id)));
ALTER TABLE ops.publication_withdrawal_decision ENABLE ROW LEVEL SECURITY;
ALTER TABLE ops.publication_withdrawal_decision FORCE ROW LEVEL SECURITY;
CREATE POLICY withdrawal_review_insert ON ops.publication_withdrawal_decision FOR INSERT TO js_publication WITH CHECK(
 authz.has_role('PUBLISHER') AND reviewer_ref=authz.principal()
 AND EXISTS(SELECT FROM ops.publication_withdrawal_request w JOIN ops.case_record c ON c.id=w.case_id
 JOIN social.case_receipt r ON r.id=w.receipt_id WHERE w.id=request_id AND w.state='REQUESTED'
 AND c.version=publication_withdrawal_decision.case_version AND r.publication_version=publication_withdrawal_decision.publication_version AND authz.case_access(w.case_id)));
CREATE VIEW ops.publication_withdrawal_outcome WITH (security_barrier=true) AS
 SELECT d.request_id,d.result,d.resident_reason,d.decided_at FROM ops.publication_withdrawal_decision d
 JOIN ops.publication_withdrawal_request w ON w.id=d.request_id WHERE authz.owns_report(w.report_id);
CREATE VIEW ops.publication_withdrawal_review WITH (security_barrier=true) AS
 SELECT w.id,w.case_id,w.receipt_id,w.publication_version,w.reason_code,w.state,w.version,w.created_at,
 c.version AS case_version,r.publication_state,r.publication_version AS current_publication_version,
 r.title,r.safe_summary,r.area_label,d.result,d.internal_reason,d.resident_reason,d.decided_at
 FROM ops.publication_withdrawal_request w JOIN ops.case_record c ON c.id=w.case_id
 JOIN social.case_receipt r ON r.id=w.receipt_id LEFT JOIN ops.publication_withdrawal_decision d ON d.request_id=w.id
 WHERE authz.has_role('PUBLISHER') AND authz.case_access(w.case_id);
-- Keep pending and approved owner requests effective even if a reviewer tries
-- the normal publication endpoint. A declined request restores fresh-review eligibility.
CREATE VIEW ops.publication_sharing_eligibility WITH (security_barrier=true) AS
 SELECT c.id AS case_id,NOT EXISTS(SELECT FROM ops.case_observation o JOIN ops.report r ON r.id=o.report_id WHERE o.case_id=c.id AND r.publication_preference='PRIVATE')
 AND NOT EXISTS(SELECT FROM ops.publication_withdrawal_request w WHERE w.case_id=c.id AND w.state IN ('REQUESTED','APPROVED')) AS allowed
 FROM ops.case_record c WHERE authz.has_role('PUBLISHER') AND authz.case_access(c.id);
REVOKE ALL ON ops.publication_sharing_eligibility FROM PUBLIC;
REVOKE ALL ON ops.publication_withdrawal_outcome,ops.publication_withdrawal_review FROM PUBLIC;

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Retained resident sharing requests require a reviewed forward migration'; END $$;
-- +goose StatementEnd

-- +goose Up
ALTER TABLE social.appeal ADD CONSTRAINT appeal_grounds_length CHECK(char_length(btrim(grounds)) BETWEEN 5 AND 1000) NOT VALID;
CREATE INDEX appeal_owner_page ON social.appeal(appellant_ref,created_at DESC,id DESC);
CREATE INDEX appeal_open_queue ON social.appeal(created_at,id) WHERE state IN ('OPEN','REVIEWING');
CREATE TABLE social.appeal_decision (
 id uuid PRIMARY KEY,
 appeal_id uuid NOT NULL UNIQUE REFERENCES social.appeal(id),
 result text NOT NULL CHECK(result IN ('UPHELD','REVERSED')),
 author_reason text NOT NULL CHECK(char_length(btrim(author_reason)) BETWEEN 5 AND 1000),
 restoration_state text NOT NULL CHECK(restoration_state IN ('UNCHANGED','RESTORED','NOT_RESTORED')),
 restoration_reason text NOT NULL CHECK(restoration_reason IN ('NONE','TARGET_CHANGED','TARGET_UNAVAILABLE','POSTING_NOT_ALLOWED','PARENT_UNAVAILABLE')),
 reviewer_ref uuid NOT NULL,
 decided_at timestamptz NOT NULL DEFAULT now(),
 CHECK((result='UPHELD' AND restoration_state='UNCHANGED' AND restoration_reason='NONE')
 OR (result='REVERSED' AND ((restoration_state='RESTORED' AND restoration_reason='NONE')
 OR (restoration_state='NOT_RESTORED' AND restoration_reason<>'NONE'))))
);

-- Current grants and original-decision conflicts constrain every runtime read/write.
ALTER TABLE social.appeal ENABLE ROW LEVEL SECURITY;
ALTER TABLE social.appeal FORCE ROW LEVEL SECURITY;
CREATE POLICY appeal_read ON social.appeal FOR SELECT TO js_social USING(
 appellant_ref=authz.principal() OR (authz.has_role('PLATFORM_MODERATOR') AND appellant_ref<>authz.principal()
 AND (reviewer_ref IS NULL OR reviewer_ref=authz.principal())
 AND EXISTS(SELECT FROM social.moderation_decision d JOIN social.moderation_case m ON m.id=d.moderation_case_id
 WHERE d.id=decision_id AND d.actor_ref<>authz.principal() AND (m.reporter_ref IS NULL OR m.reporter_ref<>authz.principal()))));
CREATE POLICY appeal_insert ON social.appeal FOR INSERT TO js_social WITH CHECK(
 appellant_ref=authz.principal() AND state='OPEN' AND reviewer_ref IS NULL AND version=1
 AND EXISTS(SELECT FROM social.author_moderation_decision d WHERE d.id=decision_id AND d.action IN ('RESTRICT','REMOVE')));
CREATE POLICY appeal_update ON social.appeal FOR UPDATE TO js_social USING(
 authz.has_role('PLATFORM_MODERATOR') AND appellant_ref<>authz.principal() AND state IN ('OPEN','REVIEWING')
 AND (reviewer_ref IS NULL OR reviewer_ref=authz.principal())
 AND EXISTS(SELECT FROM social.moderation_decision d JOIN social.moderation_case m ON m.id=d.moderation_case_id
 WHERE d.id=decision_id AND d.actor_ref<>authz.principal() AND (m.reporter_ref IS NULL OR m.reporter_ref<>authz.principal())))
 WITH CHECK(authz.has_role('PLATFORM_MODERATOR') AND reviewer_ref=authz.principal() AND state IN ('REVIEWING','UPHELD','REVERSED'));
ALTER TABLE social.appeal_decision ENABLE ROW LEVEL SECURITY;
ALTER TABLE social.appeal_decision FORCE ROW LEVEL SECURITY;
CREATE POLICY appeal_decision_read ON social.appeal_decision FOR SELECT TO js_social USING(EXISTS(SELECT FROM social.appeal a WHERE a.id=appeal_id));
CREATE POLICY appeal_decision_insert ON social.appeal_decision FOR INSERT TO js_social WITH CHECK(
 authz.has_role('PLATFORM_MODERATOR') AND reviewer_ref=authz.principal()
 AND EXISTS(SELECT FROM social.appeal a WHERE a.id=appeal_id AND a.state='REVIEWING' AND a.reviewer_ref=authz.principal() AND a.appellant_ref<>authz.principal()));

-- +goose StatementBegin
CREATE FUNCTION social.guard_appeal_transition() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog,pg_temp AS $$
DECLARE original_actor uuid; reporter uuid;
BEGIN
 SELECT d.actor_ref,m.reporter_ref INTO original_actor,reporter FROM social.moderation_decision d JOIN social.moderation_case m ON m.id=d.moderation_case_id WHERE d.id=NEW.decision_id;
 IF NEW.reviewer_ref IS NOT NULL AND (NEW.reviewer_ref=original_actor OR NEW.reviewer_ref=NEW.appellant_ref OR NEW.reviewer_ref=reporter) THEN
  RAISE EXCEPTION 'appeals require an independent reviewer' USING ERRCODE='23514';
 END IF;
 IF TG_OP='UPDATE' THEN
  IF ROW(NEW.id,NEW.decision_id,NEW.appellant_ref,NEW.grounds,NEW.created_at) IS DISTINCT FROM ROW(OLD.id,OLD.decision_id,OLD.appellant_ref,OLD.grounds,OLD.created_at)
   OR NEW.version<>OLD.version+1 THEN RAISE EXCEPTION 'appeal identity, grounds and history are immutable' USING ERRCODE='23514'; END IF;
  IF OLD.state='OPEN' AND NEW.state='REVIEWING' AND OLD.reviewer_ref IS NULL AND NEW.reviewer_ref IS NOT NULL THEN RETURN NEW; END IF;
  IF OLD.state='REVIEWING' AND NEW.state IN ('UPHELD','REVERSED') AND NEW.reviewer_ref=OLD.reviewer_ref
   AND EXISTS(SELECT FROM social.appeal_decision d WHERE d.appeal_id=OLD.id AND d.result=NEW.state AND d.reviewer_ref=NEW.reviewer_ref) THEN RETURN NEW; END IF;
  RAISE EXCEPTION 'invalid appeal transition or missing outcome' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
-- SECURITY INVOKER: runtime policies still constrain the related records.
CREATE TRIGGER appeal_transition BEFORE INSERT OR UPDATE ON social.appeal FOR EACH ROW EXECUTE FUNCTION social.guard_appeal_transition();
-- +goose StatementEnd
REVOKE ALL ON FUNCTION social.guard_appeal_transition() FROM PUBLIC;

-- A result cannot commit while its claim is still open or assigned elsewhere.
-- +goose StatementBegin
CREATE FUNCTION social.guard_appeal_outcome() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog,pg_temp AS $$
BEGIN
 IF NOT EXISTS(SELECT FROM social.appeal a WHERE a.id=NEW.appeal_id AND a.state=NEW.result AND a.reviewer_ref=NEW.reviewer_ref) THEN
  RAISE EXCEPTION 'appeal outcome must close the assigned review atomically' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
CREATE CONSTRAINT TRIGGER appeal_outcome_closed AFTER INSERT ON social.appeal_decision DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION social.guard_appeal_outcome();
-- +goose StatementEnd
REVOKE ALL ON FUNCTION social.guard_appeal_outcome() FROM PUBLIC;

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Retained appeal outcomes require a reviewed forward migration'; END $$;
-- +goose StatementEnd

-- +goose Up
-- A dedicated allowlisted outbox. Never fan out infra.outbox or private case data.
CREATE SCHEMA rec_stream;
ALTER TABLE social.recommendation_preference ADD COLUMN stream_subject uuid NOT NULL DEFAULT gen_random_uuid() UNIQUE;
CREATE TABLE rec_stream.authority (
 subject uuid PRIMARY KEY,
 profile_id uuid UNIQUE REFERENCES social.profile(id) ON DELETE SET NULL,
 generation bigint NOT NULL CHECK(generation>0),
 enabled boolean NOT NULL,
 deleted boolean NOT NULL DEFAULT false
);
CREATE TABLE rec_stream.outbox (
 sequence bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 id uuid NOT NULL UNIQUE,
 aggregate_key text NOT NULL,
 subject uuid,
 generation bigint,
 event_type text NOT NULL CHECK(event_type IN ('CONTROL','INTERACTION','CONTENT')),
 payload jsonb NOT NULL,
 created_at timestamptz NOT NULL DEFAULT statement_timestamp(),
 available_at timestamptz NOT NULL DEFAULT statement_timestamp(),
 lease_token uuid,
 lease_until timestamptz,
 attempts integer NOT NULL DEFAULT 0,
 delivered_at timestamptz
);
CREATE INDEX recommendation_stream_due ON rec_stream.outbox(available_at,sequence) WHERE delivered_at IS NULL;
CREATE INDEX recommendation_stream_order ON rec_stream.outbox(aggregate_key,sequence) WHERE delivered_at IS NULL;

-- +goose StatementBegin
CREATE FUNCTION rec_stream.preference_changed() RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
DECLARE s uuid; g bigint; enabled boolean; gone boolean; event uuid;
BEGIN
 gone := TG_OP='DELETE';
 IF gone THEN s:=OLD.stream_subject; g:=OLD.generation+1; enabled:=false;
 ELSE s:=NEW.stream_subject; g:=NEW.generation; enabled:=NEW.personalization_enabled;
 END IF;
 IF TG_OP='UPDATE' AND NEW.generation=OLD.generation THEN RETURN NEW; END IF;
 INSERT INTO rec_stream.authority(subject,profile_id,generation,enabled,deleted)
 VALUES(s,CASE WHEN gone THEN NULL ELSE NEW.profile_id END,g,enabled,gone)
 ON CONFLICT(subject) DO UPDATE SET profile_id=EXCLUDED.profile_id,generation=EXCLUDED.generation,enabled=EXCLUDED.enabled,deleted=EXCLUDED.deleted;
 -- Already in-flight records can still reach Kafka; consumers consult current authority.
 DELETE FROM rec_stream.outbox WHERE subject=s AND event_type='INTERACTION' AND delivered_at IS NULL;
 event:=gen_random_uuid();
 INSERT INTO rec_stream.outbox(id,aggregate_key,subject,generation,event_type,payload)
 VALUES(event,'viewer:'||s,s,g,'CONTROL',jsonb_build_object('schemaVersion',1,'eventId',event,'eventType','CONTROL','subject',s,'generation',g,'enabled',enabled,'deleted',gone,'occurredAt',statement_timestamp()));
 IF gone THEN RETURN OLD; END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER recommendation_stream_preference AFTER INSERT OR UPDATE OR DELETE ON social.recommendation_preference FOR EACH ROW EXECUTE FUNCTION rec_stream.preference_changed();

-- +goose StatementBegin
CREATE FUNCTION rec_stream.interaction_created() RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
DECLARE s uuid; x social.recommendation_exposure%ROWTYPE;
BEGIN
 SELECT stream_subject INTO s FROM social.recommendation_preference WHERE profile_id=NEW.profile_id AND personalization_enabled AND generation=NEW.generation;
 IF s IS NULL THEN RETURN NEW; END IF;
 SELECT * INTO STRICT x FROM social.recommendation_exposure WHERE id=NEW.exposure_id AND profile_id=NEW.profile_id AND generation=NEW.generation;
 INSERT INTO rec_stream.outbox(id,aggregate_key,subject,generation,event_type,payload)
 VALUES(NEW.id,'viewer:'||s,s,NEW.generation,'INTERACTION',jsonb_build_object('schemaVersion',1,'eventId',NEW.id,'eventType','INTERACTION','subject',s,'generation',NEW.generation,'exposureId',NEW.exposure_id,'postId',x.post_id,'revision',x.revision,'action',NEW.kind,'normalizedRead',NEW.normalized_read,'modelVersion',x.model_version,'policyVersion',x.policy_version,'occurredAt',NEW.created_at));
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER recommendation_stream_interaction AFTER INSERT ON social.recommendation_event FOR EACH ROW EXECUTE FUNCTION rec_stream.interaction_created();

-- Content events carry only references to approved published social revisions.
-- Defer until revision inserts in the same transaction are visible.
-- +goose StatementBegin
CREATE FUNCTION rec_stream.content_changed() RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
DECLARE event uuid; published boolean; revision integer;
BEGIN
 IF TG_OP='UPDATE' AND ROW(NEW.state,NEW.published_revision,NEW.version) IS NOT DISTINCT FROM ROW(OLD.state,OLD.published_revision,OLD.version) THEN RETURN NEW; END IF;
 IF TG_OP='DELETE' THEN published:=false; revision:=coalesce(OLD.published_revision,0);
 ELSE
  SELECT p.state='PUBLISHED' AND a.state='ACTIVE' AND r.review_state='APPROVED'
   AND (c.id IS NULL OR (c.state='ACTIVE' AND c.visibility IN ('PUBLIC','RESTRICTED'))),coalesce(p.published_revision,0)
  INTO published,revision FROM social.post p JOIN social.profile a ON a.id=p.author_id
  LEFT JOIN social.post_revision r ON r.post_id=p.id AND r.revision=p.published_revision
  LEFT JOIN social.community c ON c.id=p.community_id WHERE p.id=NEW.id;
 END IF;
 IF NOT coalesce(published,false) AND coalesce(revision,0)=0 THEN RETURN NULL; END IF;
 event:=gen_random_uuid();
 INSERT INTO rec_stream.outbox(id,aggregate_key,event_type,payload)
 VALUES(event,'post:'||coalesce(NEW.id,OLD.id),'CONTENT',jsonb_build_object('schemaVersion',1,'eventId',event,'eventType','CONTENT','postId',coalesce(NEW.id,OLD.id),'revision',coalesce(revision,0),'eligible',coalesce(published,false),'occurredAt',statement_timestamp()));
 RETURN NULL;
END $$;
-- +goose StatementEnd
CREATE CONSTRAINT TRIGGER recommendation_stream_content AFTER INSERT OR UPDATE OR DELETE ON social.post DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION rec_stream.content_changed();

-- A bootstrap CONTROL for existing preferences. Prior behavioral events are not exported.
INSERT INTO rec_stream.authority(subject,profile_id,generation,enabled)
 SELECT stream_subject,profile_id,generation,personalization_enabled FROM social.recommendation_preference;
INSERT INTO rec_stream.outbox(id,aggregate_key,subject,generation,event_type,payload)
 SELECT event,'viewer:'||stream_subject,stream_subject,generation,'CONTROL',jsonb_build_object('schemaVersion',1,'eventId',event,'eventType','CONTROL','subject',stream_subject,'generation',generation,'enabled',personalization_enabled,'deleted',false,'occurredAt',statement_timestamp())
 FROM (SELECT *,gen_random_uuid() event FROM social.recommendation_preference) p;

-- The dedicated worker can only lease allowlisted envelopes and inspect consent authority.
-- +goose StatementBegin
CREATE FUNCTION rec_stream.claim(token uuid, batch_size integer) RETURNS TABLE(id uuid,aggregate_key text,payload jsonb) LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
BEGIN
 IF token IS NULL OR batch_size<1 OR batch_size>100 THEN RAISE EXCEPTION 'invalid claim'; END IF;
 DELETE FROM rec_stream.outbox o WHERE o.event_type='INTERACTION' AND o.delivered_at IS NULL
 AND (o.created_at<statement_timestamp()-interval '30 days' OR NOT EXISTS(
 SELECT FROM rec_stream.authority a JOIN social.profile viewer ON viewer.id=a.profile_id
 JOIN social.post p ON p.id=(o.payload->>'postId')::uuid
 JOIN social.post_revision r ON r.post_id=p.id AND r.revision=p.published_revision
 JOIN social.profile author ON author.id=p.author_id LEFT JOIN social.community c ON c.id=p.community_id
 WHERE a.subject=o.subject AND a.generation=o.generation AND a.enabled AND NOT a.deleted AND viewer.state='ACTIVE'
 AND p.state='PUBLISHED' AND p.published_revision=(o.payload->>'revision')::integer AND r.review_state='APPROVED' AND author.state='ACTIVE'
 AND (c.id IS NULL OR (c.state='ACTIVE' AND c.visibility IN ('PUBLIC','RESTRICTED')))
 AND NOT EXISTS(SELECT FROM social.profile_block b WHERE (b.blocker_id=viewer.id AND b.blocked_id=author.id) OR (b.blocked_id=viewer.id AND b.blocker_id=author.id))
 AND NOT EXISTS(SELECT FROM social.mute m WHERE m.profile_id=viewer.id AND (m.muted_profile_id=author.id OR m.muted_community_id=p.community_id) AND (m.expires_at IS NULL OR m.expires_at>statement_timestamp()))));
 RETURN QUERY WITH due AS (
 SELECT o.sequence FROM rec_stream.outbox o WHERE o.delivered_at IS NULL AND o.available_at<=statement_timestamp()
 AND (o.lease_until IS NULL OR o.lease_until<statement_timestamp())
 AND NOT EXISTS(SELECT FROM rec_stream.outbox earlier WHERE earlier.aggregate_key=o.aggregate_key AND earlier.delivered_at IS NULL AND earlier.sequence<o.sequence)
 ORDER BY o.sequence FOR UPDATE SKIP LOCKED LIMIT batch_size
 ) UPDATE rec_stream.outbox o SET lease_token=token,lease_until=statement_timestamp()+interval '30 seconds',attempts=o.attempts+1 FROM due WHERE o.sequence=due.sequence
 RETURNING o.id,o.aggregate_key,o.payload||jsonb_build_object('entityVersion',o.sequence);
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE FUNCTION rec_stream.ack(event uuid, token uuid) RETURNS boolean LANGUAGE sql SECURITY DEFINER SET search_path=pg_catalog AS $$
 WITH changed AS (UPDATE rec_stream.outbox SET delivered_at=statement_timestamp(),lease_token=NULL,lease_until=NULL WHERE id=event AND lease_token=token AND lease_until>statement_timestamp() RETURNING 1) SELECT EXISTS(SELECT FROM changed);
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE FUNCTION rec_stream.retry(event uuid, token uuid) RETURNS void LANGUAGE sql SECURITY DEFINER SET search_path=pg_catalog AS $$
 UPDATE rec_stream.outbox SET lease_token=NULL,lease_until=NULL,available_at=statement_timestamp()+least(60,attempts*attempts)*interval '1 second' WHERE id=event AND lease_token=token;
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE FUNCTION rec_stream.current_authority(viewer uuid) RETURNS TABLE(generation bigint,enabled boolean,deleted boolean) LANGUAGE sql SECURITY DEFINER SET search_path=pg_catalog AS $$
 SELECT a.generation,a.enabled AND coalesce(p.state='ACTIVE',false) AND NOT a.deleted,a.deleted FROM rec_stream.authority a LEFT JOIN social.profile p ON p.id=a.profile_id WHERE a.subject=viewer;
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE FUNCTION rec_stream.expire() RETURNS void LANGUAGE sql SECURITY DEFINER SET search_path=pg_catalog AS $$
 DELETE FROM rec_stream.outbox WHERE delivered_at<statement_timestamp()-interval '7 days' OR (event_type='INTERACTION' AND created_at<statement_timestamp()-interval '30 days');
$$;
-- +goose StatementEnd
REVOKE ALL ON ALL FUNCTIONS IN SCHEMA rec_stream FROM PUBLIC;
REVOKE ALL ON SCHEMA rec_stream FROM PUBLIC;
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Stream rollback requires a reviewed forward migration'; END $$;
-- +goose StatementEnd

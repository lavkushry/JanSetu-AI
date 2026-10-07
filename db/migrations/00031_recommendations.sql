-- +goose Up
-- Behavioral data is separate from notification preferences and never accessible to the vault or operations roles.
CREATE TABLE social.recommendation_preference (
 profile_id uuid PRIMARY KEY REFERENCES social.profile(id) ON DELETE CASCADE,
 personalization_enabled boolean NOT NULL DEFAULT false,
 interests text[] NOT NULL DEFAULT '{}',
 languages text[] NOT NULL DEFAULT '{}',
 locality text NOT NULL DEFAULT '',
 generation bigint NOT NULL DEFAULT 1 CHECK(generation>0),
 version bigint NOT NULL DEFAULT 1,
 CHECK(cardinality(interests)<=20 AND cardinality(languages)<=10)
);
CREATE TABLE social.recommendation_snapshot (
 id uuid PRIMARY KEY,
 viewer_id uuid NOT NULL,
 query_key text NOT NULL,
 generation bigint NOT NULL,
 expires_at timestamptz NOT NULL,
 payload jsonb NOT NULL
);
CREATE INDEX recommendation_snapshot_expiry ON social.recommendation_snapshot(expires_at);
CREATE TABLE social.recommendation_exposure (
 id uuid PRIMARY KEY,
 profile_id uuid NOT NULL REFERENCES social.profile(id) ON DELETE CASCADE,
 post_id uuid NOT NULL REFERENCES social.post(id) ON DELETE CASCADE,
 revision integer NOT NULL,
 generation bigint NOT NULL,
 model_version text NOT NULL,
 policy_version text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),
 expires_at timestamptz NOT NULL
);
CREATE INDEX recommendation_exposure_owner ON social.recommendation_exposure(profile_id,generation);
CREATE TABLE social.recommendation_event (
 id uuid PRIMARY KEY,
 profile_id uuid NOT NULL REFERENCES social.profile(id) ON DELETE CASCADE,
 exposure_id uuid NOT NULL REFERENCES social.recommendation_exposure(id) ON DELETE CASCADE,
 generation bigint NOT NULL,
 kind text NOT NULL CHECK(kind IN ('READ','SKIP','MORE','LESS','SATISFIED','DISSATISFIED')),
 normalized_read double precision NOT NULL DEFAULT 0 CHECK(normalized_read BETWEEN 0 AND 1),
 request_hash bytea NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(exposure_id,kind)
);
CREATE INDEX recommendation_event_owner ON social.recommendation_event(profile_id,generation,created_at);
ALTER TABLE social.recommendation_preference ENABLE ROW LEVEL SECURITY;
ALTER TABLE social.recommendation_preference FORCE ROW LEVEL SECURITY;
CREATE POLICY recommendation_preference_owner ON social.recommendation_preference TO js_social
 USING(profile_id=authz.current_profile()) WITH CHECK(profile_id=authz.current_profile());
ALTER TABLE social.recommendation_snapshot ENABLE ROW LEVEL SECURITY;
ALTER TABLE social.recommendation_snapshot FORCE ROW LEVEL SECURITY;
CREATE POLICY recommendation_snapshot_owner ON social.recommendation_snapshot TO js_social
 USING(viewer_id=coalesce(authz.current_profile(),'00000000-0000-0000-0000-000000000000'::uuid))
 WITH CHECK(viewer_id=coalesce(authz.current_profile(),'00000000-0000-0000-0000-000000000000'::uuid));
ALTER TABLE social.recommendation_exposure ENABLE ROW LEVEL SECURITY;
ALTER TABLE social.recommendation_exposure FORCE ROW LEVEL SECURITY;
CREATE POLICY recommendation_exposure_owner ON social.recommendation_exposure TO js_social
 USING(profile_id=authz.current_profile()) WITH CHECK(profile_id=authz.current_profile());
ALTER TABLE social.recommendation_event ENABLE ROW LEVEL SECURITY;
ALTER TABLE social.recommendation_event FORCE ROW LEVEL SECURITY;
CREATE POLICY recommendation_event_owner ON social.recommendation_event TO js_social
 USING(profile_id=authz.current_profile()) WITH CHECK(profile_id=authz.current_profile());
-- Delete personal features on account deactivation. Reactivation starts a new generation.
-- +goose StatementBegin
CREATE FUNCTION social.clear_recommendations_on_deactivation() RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
BEGIN
 IF NEW.state <> 'ACTIVE' AND OLD.state IS DISTINCT FROM NEW.state THEN
  UPDATE social.recommendation_preference SET personalization_enabled=false,generation=generation+1,version=version+1 WHERE profile_id=NEW.id;
  DELETE FROM social.recommendation_snapshot WHERE viewer_id=NEW.id;
  DELETE FROM social.recommendation_exposure WHERE profile_id=NEW.id;
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION social.clear_recommendations_on_deactivation() FROM PUBLIC;
CREATE TRIGGER clear_recommendations AFTER UPDATE OF state ON social.profile FOR EACH ROW EXECUTE FUNCTION social.clear_recommendations_on_deactivation();
-- The maintenance role can expire data, but cannot read personal rows through RLS.
-- +goose StatementBegin
CREATE FUNCTION social.expire_recommendations() RETURNS void LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
BEGIN
 DELETE FROM social.recommendation_snapshot WHERE expires_at<statement_timestamp();
 DELETE FROM social.recommendation_exposure WHERE created_at<statement_timestamp()-interval '30 days';
END $$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION social.expire_recommendations() FROM PUBLIC;
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Recommendation rollback requires a reviewed forward migration'; END $$;
-- +goose StatementEnd

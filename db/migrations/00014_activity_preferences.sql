-- +goose Up
ALTER TABLE social.mute ADD COLUMN created_at timestamptz NOT NULL DEFAULT statement_timestamp();
CREATE INDEX mute_owner_page ON social.mute(profile_id,created_at DESC,id DESC);
CREATE POLICY preference_owner_insert ON social.feed_preference FOR INSERT TO js_social WITH CHECK(profile_id=authz.current_profile());
CREATE POLICY preference_owner_update ON social.feed_preference FOR UPDATE TO js_social USING(profile_id=authz.current_profile()) WITH CHECK(profile_id=authz.current_profile());
CREATE POLICY mute_owner_insert ON social.mute FOR INSERT TO js_social WITH CHECK(profile_id=authz.current_profile());
CREATE POLICY mute_owner_update ON social.mute FOR UPDATE TO js_social USING(profile_id=authz.current_profile()) WITH CHECK(profile_id=authz.current_profile());
CREATE POLICY mute_owner_delete ON social.mute FOR DELETE TO js_social USING(profile_id=authz.current_profile());

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Preference history rollback requires a reviewed forward migration'; END $$;
-- +goose StatementEnd

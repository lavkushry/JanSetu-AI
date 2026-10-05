-- +goose Up
-- Runtime membership commands may join/leave, never provision community roles.
ALTER TABLE social.community_member ENABLE ROW LEVEL SECURITY;
ALTER TABLE social.community_member FORCE ROW LEVEL SECURITY;
CREATE POLICY membership_read ON social.community_member FOR SELECT TO js_social USING(true);
CREATE POLICY membership_join ON social.community_member FOR INSERT TO js_social WITH CHECK(
 profile_id=authz.current_profile() AND role='MEMBER' AND state IN ('ACTIVE','LEFT')
 AND EXISTS(SELECT FROM social.community c WHERE c.id=community_id AND c.state='ACTIVE' AND c.visibility IN ('PUBLIC','RESTRICTED')));
CREATE POLICY membership_change ON social.community_member FOR UPDATE TO js_social USING(
 profile_id=authz.current_profile() AND state<>'BANNED') WITH CHECK(
 profile_id=authz.current_profile() AND state IN ('ACTIVE','LEFT')
 AND EXISTS(SELECT FROM social.community c WHERE c.id=community_id AND c.state='ACTIVE' AND c.visibility IN ('PUBLIC','RESTRICTED')));

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Community role isolation requires a reviewed forward migration'; END $$;
-- +goose StatementEnd

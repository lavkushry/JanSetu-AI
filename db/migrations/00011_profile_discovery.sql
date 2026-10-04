-- +goose Up
CREATE INDEX blocks_by_owner_and_time ON social.profile_block(blocker_id,created_at DESC,blocked_id DESC);

-- +goose Down
DROP INDEX social.blocks_by_owner_and_time;

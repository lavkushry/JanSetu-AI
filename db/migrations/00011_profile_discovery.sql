-- +goose Up
CREATE INDEX published_posts_by_author ON social.post(author_id,published_at DESC,id DESC) WHERE state='PUBLISHED';
CREATE INDEX blocks_by_owner_and_time ON social.profile_block(blocker_id,created_at DESC,blocked_id DESC);

-- +goose Down
DROP INDEX social.blocks_by_owner_and_time;
DROP INDEX social.published_posts_by_author;

-- +goose Up
-- The existing comment_page_idx includes parent_id for branch lookups. This
-- index supports chronological pages across the entire conversation.
CREATE INDEX comment_chronological_idx ON social.comment(post_id,created_at,id);

-- +goose Down
DROP INDEX social.comment_chronological_idx;

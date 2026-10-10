-- +goose Up
CREATE INDEX recommendation_event_retention ON social.recommendation_event(created_at);

-- Preserve a still-retained child even when its exposure was created earlier.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION social.expire_recommendations() RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
BEGIN
 DELETE FROM social.recommendation_snapshot WHERE expires_at<statement_timestamp();
 DELETE FROM social.recommendation_event WHERE created_at<statement_timestamp()-interval '30 days';
 DELETE FROM social.recommendation_exposure x
 WHERE x.created_at<statement_timestamp()-interval '30 days'
 AND NOT EXISTS (SELECT FROM social.recommendation_event e WHERE e.exposure_id=x.id);
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Feedback retention rollback requires a reviewed forward migration'; END $$;
-- +goose StatementEnd

-- +goose Up
CREATE INDEX recommendation_event_retention ON social.recommendation_event(created_at);

-- Preserve a still-retained child even when its exposure was created earlier.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION social.expire_recommendations() RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
BEGIN
 DELETE FROM social.recommendation_snapshot WHERE expires_at<statement_timestamp();
 -- Owner reset/deactivation deletes the parent before cascading to its events.
 -- Lock parents first and skip in-flight owner commands instead of reversing
 -- that order by deleting their children and then waiting for the parent.
 WITH locked_exposures AS MATERIALIZED (
  SELECT x.id FROM social.recommendation_exposure x
  WHERE x.id IN (SELECT e.exposure_id FROM social.recommendation_event e
                WHERE e.created_at<statement_timestamp()-interval '30 days')
  FOR UPDATE OF x SKIP LOCKED
 )
 DELETE FROM social.recommendation_event e USING locked_exposures x
 WHERE e.exposure_id=x.id AND e.created_at<statement_timestamp()-interval '30 days';
 WITH expired_exposures AS MATERIALIZED (
  SELECT x.id FROM social.recommendation_exposure x
  WHERE x.created_at<statement_timestamp()-interval '30 days'
  AND NOT EXISTS (SELECT FROM social.recommendation_event e WHERE e.exposure_id=x.id)
  FOR UPDATE OF x SKIP LOCKED
 )
 DELETE FROM social.recommendation_exposure x USING expired_exposures old
 WHERE x.id=old.id;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Feedback retention rollback requires a reviewed forward migration'; END $$;
-- +goose StatementEnd

-- +goose Up
ALTER TABLE ops.report ADD COLUMN version bigint NOT NULL DEFAULT 1;
CREATE TABLE ops.intake_review (
  report_id uuid PRIMARY KEY REFERENCES ops.report(id),
  case_id uuid REFERENCES ops.case_record(id),
  state text NOT NULL DEFAULT 'PENDING' CHECK (state IN ('PENDING','LINKED')),
  version bigint NOT NULL DEFAULT 1,
  reviewed_by uuid REFERENCES identity.principal(id),
  reviewed_at timestamptz
);
ALTER TABLE ops.obligation ADD COLUMN work_summary text NOT NULL DEFAULT '';
ALTER TABLE ops.obligation ADD COLUMN completion_actor_ref uuid REFERENCES identity.principal(id);

-- +goose StatementBegin
CREATE FUNCTION social.check_comment_parent() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE parent_depth integer;
BEGIN
  IF TG_OP = 'UPDATE' AND (NEW.parent_id IS DISTINCT FROM OLD.parent_id OR NEW.post_id <> OLD.post_id) THEN
    RAISE EXCEPTION 'comment parent is immutable' USING ERRCODE = '23514';
  END IF;
  IF NEW.parent_id IS NOT NULL THEN
    SELECT depth INTO parent_depth FROM social.comment WHERE id = NEW.parent_id AND post_id = NEW.post_id;
    IF parent_depth IS NULL OR NEW.depth <> parent_depth + 1 THEN
      RAISE EXCEPTION 'invalid comment depth' USING ERRCODE = '23514';
    END IF;
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER comment_parent BEFORE INSERT OR UPDATE ON social.comment
FOR EACH ROW EXECUTE FUNCTION social.check_comment_parent();

CREATE FUNCTION social.check_post_publication() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.state = 'PUBLISHED' THEN
    IF NOT EXISTS (SELECT 1 FROM social.post_revision WHERE post_id = NEW.id
                   AND revision = NEW.published_revision AND review_state = 'APPROVED') THEN
      RAISE EXCEPTION 'publication needs approved revision' USING ERRCODE = '23514';
    END IF;
    IF EXISTS (SELECT 1 FROM social.post_media pm JOIN social.media_asset m ON m.id = pm.media_id
               WHERE pm.post_id = NEW.id AND pm.revision = NEW.published_revision
                 AND (m.state <> 'APPROVED' OR m.report_alias_ref IS NOT NULL)) THEN
      RAISE EXCEPTION 'publication needs approved social media' USING ERRCODE = '23514';
    END IF;
  END IF;
  RETURN NEW;
END $$;
CREATE CONSTRAINT TRIGGER post_publication AFTER INSERT OR UPDATE ON social.post
DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION social.check_post_publication();

CREATE FUNCTION social.check_comment_publication() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.state = 'PUBLISHED' AND NOT EXISTS
    (SELECT 1 FROM social.comment_revision WHERE comment_id = NEW.id
     AND version = NEW.published_version AND review_state = 'APPROVED' AND body = NEW.body) THEN
    RAISE EXCEPTION 'comment needs approved revision' USING ERRCODE = '23514';
  END IF;
  RETURN NEW;
END $$;
CREATE CONSTRAINT TRIGGER comment_publication AFTER INSERT OR UPDATE ON social.comment
DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION social.check_comment_publication();
-- +goose StatementEnd

-- +goose Down
-- This foundation is rolled back through compatible application releases.
-- Do not destroy retained records through a down migration.
SELECT 1;

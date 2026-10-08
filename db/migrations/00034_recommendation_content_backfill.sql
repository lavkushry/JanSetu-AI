-- +goose Up
-- One initial historical pass; internal cursors never leave the database.
CREATE TABLE rec_stream.content_backfill (
 singleton boolean PRIMARY KEY DEFAULT true CHECK(singleton),
 started boolean NOT NULL DEFAULT false,
 upper_id uuid,
 last_id uuid,
 completed boolean NOT NULL DEFAULT false,
 scanned bigint NOT NULL DEFAULT 0 CHECK(scanned>=0),
 enqueued bigint NOT NULL DEFAULT 0 CHECK(enqueued>=0 AND enqueued<=scanned),
 updated_at timestamptz NOT NULL DEFAULT statement_timestamp()
);
INSERT INTO rec_stream.content_backfill(singleton) VALUES(true);

-- +goose StatementBegin
CREATE FUNCTION rec_stream.backfill_content(batch_size integer)
RETURNS TABLE(scanned integer,enqueued integer,completed boolean,total_scanned bigint,total_enqueued bigint)
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog SET lock_timeout='250ms' AS $$
DECLARE checkpoint rec_stream.content_backfill%ROWTYPE; target_id uuid; revision integer; event uuid;
BEGIN
 IF batch_size IS NULL OR batch_size<1 OR batch_size>100 THEN RAISE EXCEPTION 'backfill batch must be 1..100'; END IF;
 SELECT * INTO STRICT checkpoint FROM rec_stream.content_backfill WHERE singleton FOR UPDATE;
 scanned:=0; enqueued:=0;
 IF NOT checkpoint.started THEN
  SELECT p.id INTO checkpoint.upper_id FROM social.post p ORDER BY p.id DESC LIMIT 1;
  checkpoint.started:=true;
 END IF;
 IF NOT checkpoint.completed THEN
  -- Never SKIP LOCKED: advancing past a busy historical row would lose it.
  -- Post locks serialize event sequencing with concurrent edits/deletion.
  FOR target_id IN SELECT p.id FROM social.post p
   WHERE p.id<=checkpoint.upper_id AND (checkpoint.last_id IS NULL OR p.id>checkpoint.last_id)
   ORDER BY p.id LIMIT batch_size FOR UPDATE OF p
  LOOP
   scanned:=scanned+1; checkpoint.last_id:=target_id;
   -- A separate statement observes edits committed before acquiring the row lock.
   SELECT p.published_revision INTO revision
   FROM social.post p JOIN social.profile a ON a.id=p.author_id
   JOIN social.post_revision r ON r.post_id=p.id AND r.revision=p.published_revision
   LEFT JOIN social.community c ON c.id=p.community_id
   WHERE p.id=target_id AND p.state='PUBLISHED' AND a.state='ACTIVE' AND r.review_state='APPROVED'
   AND (c.id IS NULL OR (c.state='ACTIVE' AND c.visibility IN ('PUBLIC','RESTRICTED')))
   AND (p.source_post_id IS NULL OR EXISTS(
    SELECT FROM social.post original JOIN social.profile oa ON oa.id=original.author_id
    JOIN social.post_revision orev ON orev.post_id=original.id AND orev.revision=original.published_revision
    LEFT JOIN social.community oc ON oc.id=original.community_id
    WHERE original.id=p.source_post_id AND original.state='PUBLISHED' AND oa.state='ACTIVE' AND orev.review_state='APPROVED'
    AND (oc.id IS NULL OR (oc.state='ACTIVE' AND oc.visibility IN ('PUBLIC','RESTRICTED')))));
   IF revision IS NOT NULL THEN
    event:=gen_random_uuid();
    INSERT INTO rec_stream.outbox(id,aggregate_key,event_type,payload)
    VALUES(event,'post:'||target_id,'CONTENT',jsonb_build_object('schemaVersion',1,'eventId',event,'eventType','CONTENT','postId',target_id,'revision',revision,'eligible',true,'occurredAt',statement_timestamp()));
    enqueued:=enqueued+1;
   END IF;
  END LOOP;
  checkpoint.completed:=scanned<batch_size;
  UPDATE rec_stream.content_backfill b SET started=checkpoint.started,upper_id=checkpoint.upper_id,last_id=checkpoint.last_id,
   completed=checkpoint.completed,scanned=b.scanned+backfill_content.scanned,enqueued=b.enqueued+backfill_content.enqueued,updated_at=statement_timestamp()
   WHERE singleton RETURNING b.* INTO checkpoint;
 END IF;
 completed:=checkpoint.completed; total_scanned:=checkpoint.scanned; total_enqueued:=checkpoint.enqueued;
 RETURN NEXT;
END $$;
-- +goose StatementEnd
REVOKE ALL ON rec_stream.content_backfill FROM PUBLIC;
REVOKE ALL ON FUNCTION rec_stream.backfill_content(integer) FROM PUBLIC;
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Backfill rollback requires a reviewed forward migration'; END $$;
-- +goose StatementEnd

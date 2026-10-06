-- +goose Up
ALTER TABLE infra.analysis_task DROP CONSTRAINT analysis_task_task_kind_check;
ALTER TABLE infra.analysis_task ADD CONSTRAINT analysis_task_task_kind_check
 CHECK(task_kind IN ('QUALITY','OCR','ISSUE_DETECTION','POTHOLE_DETECTION','REDACTION','VOICE_TRANSCRIPTION'));

-- +goose Down
-- Deliberately fail if pothole tasks exist; never discard private analysis to
-- make a downgrade pass. Drain/remove the new API capability before rollback.
ALTER TABLE infra.analysis_task DROP CONSTRAINT analysis_task_task_kind_check;
ALTER TABLE infra.analysis_task ADD CONSTRAINT analysis_task_task_kind_check
 CHECK(task_kind IN ('QUALITY','OCR','ISSUE_DETECTION','REDACTION','VOICE_TRANSCRIPTION'));

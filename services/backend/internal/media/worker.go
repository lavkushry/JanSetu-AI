package media

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Worker struct {
	Vision      *Detector
	VisionModel string
	DB          *pgxpool.Pool
	Files       *Storage
	Engine      OCR
	Model       string
}

func (w *Worker) Once(ctx context.Context) error {
	if e := w.expireAbandoned(ctx); e != nil {
		return e
	}
	// Expired incomplete uploads lose their capability and bytes. Originals are
	// removed after approval/rejection too; restarting repairs interrupted cleanup.
	rows, e := w.DB.Query(ctx, `UPDATE infra.upload_session SET state='EXPIRED',token_hash=NULL,version=version+1 WHERE state='OPEN' AND expires_at<=statement_timestamp() RETURNING media_id`)
	if e != nil {
		return e
	}
	ids := []uuid.UUID{}
	for rows.Next() {
		var id uuid.UUID
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return e
		}
		ids = append(ids, id)
	}
	rows.Close()
	if e = rows.Err(); e != nil {
		return e
	}
	for _, id := range ids {
		if _, e = w.DB.Exec(ctx, `UPDATE social.media_asset SET state='REJECTED',rejection_code='UPLOAD_EXPIRED',authorization_version=authorization_version+1 WHERE id=$1 AND state='UPLOADING'`, id); e != nil {
			return e
		}
		w.Files.Remove(OriginalKey(id))
	}
	cleanup, e := w.DB.Query(ctx, `SELECT id,state,derivative_key FROM social.media_asset WHERE state IN ('APPROVED','REJECTED','REVOKED') AND storage_cleanup_at IS NULL ORDER BY created_at LIMIT 200`)
	if e != nil {
		return e
	}
	for cleanup.Next() {
		var id uuid.UUID
		var state string
		var derivative *string
		if e = cleanup.Scan(&id, &state, &derivative); e != nil {
			cleanup.Close()
			return e
		}
		if e = w.Files.Remove(OriginalKey(id)); e != nil {
			cleanup.Close()
			return e
		}
		if state == "REVOKED" && derivative != nil {
			if e = w.Files.Remove(*derivative); e != nil {
				cleanup.Close()
				return e
			}
		}
		if _, e = w.DB.Exec(ctx, `UPDATE social.media_asset SET storage_cleanup_at=statement_timestamp() WHERE id=$1 AND state=$2`, id, state); e != nil {
			cleanup.Close()
			return e
		}
	}
	cleanup.Close()
	if e = cleanup.Err(); e != nil {
		return e
	}
	if e = w.prepareOne(ctx); e != nil {
		return e
	}
	return w.analyzeOne(ctx)
}

// Retention is rechecked in a fresh statement after acquiring the media lock.
// An eligibility snapshot taken before a concurrent attachment commits must
// never revoke its photo after waiting for that attachment's row lock.
func (w *Worker) expireAbandoned(ctx context.Context) error {
	return pgx.BeginTxFunc(ctx, w.DB, pgx.TxOptions{}, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `SELECT id FROM social.media_asset WHERE created_at<statement_timestamp()-interval '24 hours' AND state IN ('UPLOADING','QUARANTINED','APPROVED') AND NOT authz.media_retained(id) ORDER BY created_at FOR UPDATE SKIP LOCKED LIMIT 100`)
		if e != nil {
			return e
		}
		ids := []uuid.UUID{}
		for rows.Next() {
			var mid uuid.UUID
			if e = rows.Scan(&mid); e != nil {
				rows.Close()
				return e
			}
			ids = append(ids, mid)
		}
		rows.Close()
		if e = rows.Err(); e != nil {
			return e
		}
		for _, mid := range ids {
			if _, e = tx.Exec(ctx, `UPDATE social.media_asset SET state='REVOKED',authorization_version=authorization_version+1,processing_token=NULL,storage_cleanup_at=NULL WHERE id=$1 AND NOT authz.media_retained(id)`, mid); e != nil {
				return e
			}
		}
		return nil
	})
}

func (w *Worker) prepareOne(ctx context.Context) error {
	token := uuid.New()
	var mid uuid.UUID
	var expectedHash []byte
	e := w.DB.QueryRow(ctx, `UPDATE social.media_asset SET processing_token=$1,processing_until=statement_timestamp()+interval '2 minutes' WHERE id=(SELECT id FROM social.media_asset WHERE state='QUARANTINED' AND (processing_until IS NULL OR processing_until<statement_timestamp()) ORDER BY created_at FOR UPDATE SKIP LOCKED LIMIT 1) RETURNING id,sha256`, token).Scan(&mid, &expectedHash)
	if errors.Is(e, pgx.ErrNoRows) {
		return nil
	}
	if e != nil {
		return e
	}
	original, e := w.Files.Open(OriginalKey(mid))
	code := "FILE_UNAVAILABLE"
	var p Prepared
	if e == nil {
		p, code = Prepare(original)
		_ = original.Close()
	}
	if code == "" && !bytes.Equal(expectedHash, p.Hash) {
		code = "HASH_MISMATCH"
	}
	key := ""
	if code == "" {
		key = DerivativeKey()
		if _, _, e = w.Files.Save(key, bytes.NewReader(p.PNG), MaxDerivativeBytes); e != nil {
			return e
		}
	}
	processed := false
	e = pgx.BeginTxFunc(ctx, w.DB, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var state string
		var current *uuid.UUID
		if e = tx.QueryRow(ctx, `SELECT state,processing_token FROM social.media_asset WHERE id=$1 FOR UPDATE`, mid).Scan(&state, &current); e != nil {
			return e
		}
		if state != "QUARANTINED" || current == nil || *current != token {
			w.Files.Remove(key)
			return nil
		}
		processed = true
		if code != "" {
			_, e = tx.Exec(ctx, `UPDATE social.media_asset SET state='REJECTED',rejection_code=$2,processing_token=NULL,processing_until=NULL,authorization_version=authorization_version+1 WHERE id=$1`, mid, code)
			return e
		}
		_, e = tx.Exec(ctx, `INSERT INTO infra.media_derivative(id,media_id,source_sha256,storage_key,transform_version,pixel_width,pixel_height,original_to_derivative,approved_at) VALUES($1,$2,$3,$4,'decoded-pixels-png-v1',$5,$6,'{"matrix":[1,0,0,0,1,0,0,0,1]}',statement_timestamp())`, uuid.New(), mid, p.Hash, key, p.Width, p.Height)
		if e != nil {
			return e
		}
		_, e = tx.Exec(ctx, `UPDATE social.media_asset SET state='APPROVED',actual_type=$2,derivative_key=$3,processing_token=NULL,processing_until=NULL WHERE id=$1`, mid, p.MIME, key)
		return e
	})
	if e != nil {
		w.Files.Remove(key)
		return e
	}
	if processed {
		w.Files.Remove(OriginalKey(mid))
	}
	return nil
}
func (w *Worker) analyzeOne(ctx context.Context) error {
	token := uuid.New()
	var jid, mid uuid.UUID
	var version int64
	var language string
	var hash []byte
	e := w.DB.QueryRow(ctx, `UPDATE infra.analysis_job SET state='RUNNING',version=version+1,lease_token=$1,lease_until=statement_timestamp()+interval '2 minutes' WHERE id=(SELECT id FROM infra.analysis_job WHERE state='QUEUED' OR (state='RUNNING' AND lease_until<statement_timestamp()) ORDER BY created_at FOR UPDATE SKIP LOCKED LIMIT 1) RETURNING id,media_id,version,language_tag,source_sha256`, token).Scan(&jid, &mid, &version, &language, &hash)
	if errors.Is(e, pgx.ErrNoRows) {
		return nil
	}
	if e != nil {
		return e
	}
	var key string
	var width, height int
	e = w.DB.QueryRow(ctx, `SELECT d.storage_key,d.pixel_width,d.pixel_height FROM infra.media_derivative d JOIN social.media_asset m ON m.id=d.media_id WHERE m.id=$1 AND m.state='APPROVED' AND d.storage_key=m.derivative_key AND d.revoked_at IS NULL`, mid).Scan(&key, &width, &height)
	if e != nil && !errors.Is(e, pgx.ErrNoRows) {
		return e
	}
	rows, e := w.DB.Query(ctx, `SELECT id,task_kind,attempt FROM infra.analysis_task WHERE job_id=$1 AND state IN ('QUEUED','RUNNING') ORDER BY task_kind`, jid)
	if e != nil {
		return e
	}
	type task struct {
		id      uuid.UUID
		kind    string
		attempt int
	}
	tasks := []task{}
	for rows.Next() {
		var t task
		if e = rows.Scan(&t.id, &t.kind, &t.attempt); e != nil {
			rows.Close()
			return e
		}
		tasks = append(tasks, t)
	}
	rows.Close()
	if e = rows.Err(); e != nil {
		return e
	}
	for _, t := range tasks {
		var live bool
		if e = w.DB.QueryRow(ctx, `SELECT authz.media_job_live($1)`, jid).Scan(&live); e != nil {
			return e
		}
		if !live {
			return w.finish(ctx, jid, mid, token, version, true)
		}
		tag, e := w.DB.Exec(ctx, `UPDATE infra.analysis_task SET state='RUNNING',attempt=attempt+1 WHERE id=$1 AND EXISTS(SELECT FROM infra.analysis_job WHERE id=$2 AND lease_token=$3 AND version=$4 AND state='RUNNING')`, t.id, jid, token, version)
		if e != nil {
			return e
		}
		if tag.RowsAffected() != 1 {
			return nil
		}
		state, code, model := "SUCCEEDED", "", "decoded-size-v1"
		result := Result(model, hash, width, height)
		if t.attempt >= 3 {
			state, code = "FAILED", "ATTEMPT_LIMIT"
		} else if key == "" {
			state, code = "FAILED", "FILE_UNAVAILABLE"
		} else if t.kind == "QUALITY" {
			if width < 640 || height < 480 {
				result.Codes = []string{"LOW_RESOLUTION"}
			}
		} else if t.kind == "OCR" {
			model = w.Model
			path, err := w.Files.Path(key)
			if err != nil {
				return err
			}
			result, code = w.Engine.Run(ctx, path, language, model, hash, width, height)
			if code != "" {
				state = "FAILED"
			}
		} else if t.kind == "ISSUE_DETECTION" && w.Vision != nil {
			model = w.VisionModel
			path, err := w.Files.Path(key)
			if err != nil {
				return err
			}
			result, code = w.Vision.Run(ctx, path, model, hash, width, height)
			if code != "" {
				state = "FAILED"
			}
		} else {
			state, code = "UNSUPPORTED", "CAPABILITY_UNAVAILABLE"
		}
		var encoded []byte
		if state == "SUCCEEDED" {
			encoded, e = json.Marshal(result)
			if e != nil {
				return e
			}
		}
		e = pgx.BeginTxFunc(ctx, w.DB, pgx.TxOptions{}, func(tx pgx.Tx) error {
			// All mutation paths lock media before job. Cancellation/revocation fences
			// even a successful OCR process that finishes after permissions change.
			var m uuid.UUID
			if e = tx.QueryRow(ctx, `SELECT id FROM social.media_asset WHERE id=$1 FOR UPDATE`, mid).Scan(&m); e != nil {
				return e
			}
			var active bool
			e = tx.QueryRow(ctx, `SELECT authz.media_job_live(id) FROM infra.analysis_job WHERE id=$1 AND lease_token=$2 AND version=$3 AND state='RUNNING' FOR UPDATE`, jid, token, version).Scan(&active)
			if errors.Is(e, pgx.ErrNoRows) {
				return nil
			}
			if e != nil {
				return e
			}
			if !active {
				return nil
			}
			_, e = tx.Exec(ctx, `UPDATE infra.analysis_task SET state=$2,result=$3,error_code=nullif($4,''),model_version=$5,completed_at=statement_timestamp() WHERE id=$1`, t.id, state, encoded, code, model)
			return e
		})
		if e != nil {
			return e
		}
	}
	return w.finish(ctx, jid, mid, token, version, false)
}
func (w *Worker) finish(ctx context.Context, jid, mid, token uuid.UUID, version int64, cancel bool) error {
	return pgx.BeginTxFunc(ctx, w.DB, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var m uuid.UUID
		if e := tx.QueryRow(ctx, `SELECT id FROM social.media_asset WHERE id=$1 FOR UPDATE`, mid).Scan(&m); e != nil {
			return e
		}
		var live bool
		e := tx.QueryRow(ctx, `SELECT authz.media_job_live(id) FROM infra.analysis_job WHERE id=$1 AND lease_token=$2 AND version=$3 AND state='RUNNING' FOR UPDATE`, jid, token, version).Scan(&live)
		if errors.Is(e, pgx.ErrNoRows) {
			return nil
		}
		if e != nil {
			return e
		}
		if cancel || !live {
			if _, e = tx.Exec(ctx, `UPDATE infra.analysis_task SET state='CANCELLED',result=NULL,error_code='PERMISSION_CHANGED' WHERE job_id=$1`, jid); e != nil {
				return e
			}
			_, e = tx.Exec(ctx, `UPDATE infra.analysis_job SET state='CANCELLED',version=version+1,lease_token=NULL,lease_until=NULL WHERE id=$1`, jid)
			return e
		}
		_, e = tx.Exec(ctx, `UPDATE infra.analysis_job SET state=CASE WHEN NOT EXISTS(SELECT FROM infra.analysis_task WHERE job_id=$1 AND state<>'SUCCEEDED') THEN 'SUCCEEDED' WHEN EXISTS(SELECT FROM infra.analysis_task WHERE job_id=$1 AND state='SUCCEEDED') THEN 'PARTIAL' ELSE 'FAILED' END,version=version+1,lease_token=NULL,lease_until=NULL WHERE id=$1`, jid)
		return e
	})
}

// Run performs one bounded item at a time. Database leases survive process restarts.
func (w *Worker) Run(ctx context.Context, onError func(error)) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if e := w.Once(ctx); e != nil && ctx.Err() == nil {
			onError(e)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

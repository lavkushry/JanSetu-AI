package app

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/lavkushry/JanSetu-AI/services/backend/internal/media"
)

func (a *App) mediaScope(r *http.Request, actor *Actor) error {
	if e := require(actor); e != nil {
		return e
	}
	if a.Media == nil || a.Files == nil {
		return failure(503, "MEDIA_UNAVAILABLE", "Photos are temporarily unavailable. You can submit a text report")
	}
	_, e := a.ownedAliases(r.Context())
	return e
}

type upload struct {
	ID, MediaID       uuid.UUID
	State, MediaState string
	Size, Version     int64
	Expires           time.Time
	TokenHash         []byte
	ETag              *string
	Alias             uuid.UUID
	PartSize          int64
	MIME              string
	Hash              []byte
	Parts             []uploadPart
}
type uploadPart struct {
	Number    int
	Size      int64
	TokenHash []byte
	ETag      *string
}

func (a *App) readUpload(r *http.Request, tx pgx.Tx, mid uuid.UUID, lock bool) (upload, error) {
	u := upload{}
	sql := `SELECT u.id,u.media_id,u.state,m.state,u.declared_size_bytes,u.version,u.expires_at,u.token_hash,u.part_etag,m.report_alias_ref,u.part_size_bytes,m.declared_type,u.expected_sha256 FROM infra.upload_session u JOIN social.media_asset m ON m.id=u.media_id WHERE m.id=$1 AND authz.media_owner(m.id)`
	if lock {
		sql += ` FOR UPDATE OF m,u`
	}
	var e error
	if tx != nil {
		e = tx.QueryRow(r.Context(), sql, mid).Scan(&u.ID, &u.MediaID, &u.State, &u.MediaState, &u.Size, &u.Version, &u.Expires, &u.TokenHash, &u.ETag, &u.Alias, &u.PartSize, &u.MIME, &u.Hash)
	} else {
		e = a.store(r.Context()).QueryRow(r.Context(), sql, mid).Scan(&u.ID, &u.MediaID, &u.State, &u.MediaState, &u.Size, &u.Version, &u.Expires, &u.TokenHash, &u.ETag, &u.Alias, &u.PartSize, &u.MIME, &u.Hash)
	}
	if e != nil {
		return u, e
	}
	if u.Hash == nil {
		u.Parts = []uploadPart{{Number: 1, Size: u.Size, TokenHash: u.TokenHash, ETag: u.ETag}}
		return u, nil
	}
	var rows pgx.Rows
	query := `SELECT part_number,byte_count,token_hash,etag FROM infra.upload_part WHERE upload_id=$1 ORDER BY part_number`
	if tx != nil {
		rows, e = tx.Query(r.Context(), query, u.ID)
	} else {
		rows, e = a.store(r.Context()).Query(r.Context(), query, u.ID)
	}
	if e != nil {
		return u, e
	}
	defer rows.Close()
	for rows.Next() {
		var p uploadPart
		if e = rows.Scan(&p.Number, &p.Size, &p.TokenHash, &p.ETag); e != nil {
			return u, e
		}
		u.Parts = append(u.Parts, p)
	}
	if e = rows.Err(); e != nil {
		return u, e
	}
	if u.PartSize != media.PartBytes || len(u.Parts) != int((u.Size+u.PartSize-1)/u.PartSize) {
		return u, errors.New("invalid stored upload layout")
	}
	for i, p := range u.Parts {
		if p.Number != i+1 || p.Size != min(u.PartSize, u.Size-int64(i)*u.PartSize) {
			return u, errors.New("invalid stored upload part")
		}
	}
	return u, nil
}
func uploadDTO(u upload, tokens map[int]string) map[string]any {
	parts := []any{}
	completed := []any{}
	for _, p := range u.Parts {
		if token := tokens[p.Number]; token != "" {
			parts = append(parts, map[string]any{"number": p.Number, "url": "/api/media/" + u.MediaID.String() + "/parts/" + strconv.Itoa(p.Number) + "?token=" + token, "expiresAt": u.Expires.UTC().Format(time.RFC3339Nano)})
		}
		if p.ETag != nil {
			completed = append(completed, map[string]any{"number": p.Number, "etag": *p.ETag})
		}
	}
	var fingerprint any
	if u.Hash != nil {
		fingerprint = media.ETag(u.Hash)
	}
	return map[string]any{"mediaId": u.MediaID, "uploadId": u.ID, "state": u.State, "partSize": u.PartSize, "byteCount": u.Size, "mimeType": u.MIME, "sourceSha256": fingerprint, "parts": parts, "completedParts": completed, "expiresAt": u.Expires.UTC().Format(time.RFC3339Nano), "version": u.Version}
}
func (a *App) createUpload(w http.ResponseWriter, r *http.Request, actor *Actor) (any, int, error) {
	if e := require(actor); e != nil {
		return nil, 0, e
	}
	if a.Media == nil || a.Files == nil {
		return nil, 0, failure(503, "MEDIA_UNAVAILABLE", "Photos are temporarily unavailable")
	}
	var b struct {
		ClientSubmissionID uuid.UUID  `json:"clientSubmissionId"`
		MIME               string     `json:"mimeType"`
		Size               int64      `json:"byteCount"`
		Purpose            string     `json:"purpose"`
		ClientUploadID     *uuid.UUID `json:"clientUploadId"`
		SourceSHA          *string    `json:"sourceSha256"`
	}
	if e := decode(r, &b); e != nil {
		return nil, 0, e
	}
	if b.ClientSubmissionID == uuid.Nil || b.Purpose != "REPORT" || b.Size < 1 || b.Size > media.MaxBytes || (b.MIME != "image/jpeg" && b.MIME != "image/png" && b.MIME != "image/webp") {
		return nil, 0, invalid("Choose a JPEG, PNG, or WebP photo up to 10 MiB for this report")
	}
	var fingerprint []byte
	if (b.ClientUploadID == nil) != (b.SourceSHA == nil) {
		return nil, 0, invalid("Choose an upload identity and photo fingerprint together")
	}
	if b.ClientUploadID != nil {
		var err error
		fingerprint, err = hex.DecodeString(*b.SourceSHA)
		if *b.ClientUploadID == uuid.Nil || err != nil || len(fingerprint) != 32 {
			return nil, 0, invalid("Invalid upload identity or photo fingerprint")
		}
	}
	alias, e := a.reportAlias(r.Context(), b.ClientSubmissionID)
	if e != nil {
		return nil, 0, e
	}
	if _, e = a.ownedAliases(r.Context()); e != nil {
		return nil, 0, e
	}
	tx, e := a.begin(r.Context())
	if e != nil {
		return nil, 0, e
	}
	defer tx.Rollback(r.Context())
	// Serialize quota checks; every row counted is independently owner-scoped by RLS.
	if _, e = tx.Exec(r.Context(), `SELECT pg_advisory_xact_lock(77120261008)`); e != nil {
		return nil, 0, e
	}
	// Recover a lost allocation response before charging quota again. The alias
	// scopes this identity to the resident's exact draft, including closed uploads.
	if b.ClientUploadID != nil {
		var mid uuid.UUID
		e = tx.QueryRow(r.Context(), `SELECT id FROM social.media_asset WHERE report_alias_ref=$1 AND client_upload_id=$2`, alias, *b.ClientUploadID).Scan(&mid)
		if e == nil {
			u, err := a.readUpload(r, tx, mid, true)
			if err != nil {
				return nil, 0, err
			}
			if u.Size != b.Size || u.MIME != b.MIME || !bytes.Equal(u.Hash, fingerprint) {
				return nil, 0, failure(409, "UPLOAD_ID_CONFLICT", "This upload identity belongs to a different photo")
			}
			if err = tx.Commit(r.Context()); err != nil {
				return nil, 0, err
			}
			return uploadDTO(u, nil), 201, nil
		}
		if !errors.Is(e, pgx.ErrNoRows) {
			return nil, 0, e
		}
	}
	var perReport int
	if e = tx.QueryRow(r.Context(), `SELECT count(*) FROM social.media_asset WHERE report_alias_ref=$1 AND state NOT IN ('REJECTED','REVOKED')`, alias).Scan(&perReport); e != nil {
		return nil, 0, e
	}
	if perReport >= 4 {
		return nil, 0, invalid("Attach up to four photos for this report")
	}
	var count int
	var total int64
	e = tx.QueryRow(r.Context(), `SELECT count(*),coalesce(sum(byte_count),0) FROM social.media_asset WHERE state NOT IN ('REJECTED','REVOKED') AND authz.media_owner(id)`).Scan(&count, &total)
	if e != nil {
		return nil, 0, e
	}
	if count >= 40 || total+b.Size > 100<<20 {
		return nil, 0, failure(429, "MEDIA_QUOTA", "Your local photo storage limit is reached")
	}
	var attached bool
	e = tx.QueryRow(r.Context(), `SELECT EXISTS(SELECT FROM social.media_asset WHERE report_alias_ref=$1 AND authz.media_attached(id))`, alias).Scan(&attached)
	if e != nil {
		return nil, 0, e
	}
	if attached {
		return nil, 0, failure(409, "REPORT_ALREADY_SUBMITTED", "This report has already been submitted")
	}
	u := upload{ID: uuid.New(), MediaID: uuid.New(), State: "OPEN", Size: b.Size, Version: 1, Expires: time.Now().Add(30 * time.Minute), PartSize: media.MaxBytes, MIME: b.MIME, Hash: fingerprint}
	if fingerprint != nil {
		u.PartSize = media.PartBytes
	}
	token, e := randomSecret()
	if e != nil {
		return nil, 0, e
	}
	_, e = tx.Exec(r.Context(), `INSERT INTO social.media_asset(id,report_alias_ref,storage_key,declared_type,byte_count,state,client_upload_id) VALUES($1,$2,$3,$4,$5,'UPLOADING',$6)`, u.MediaID, alias, media.OriginalKey(u.MediaID), b.MIME, b.Size, b.ClientUploadID)
	if e != nil {
		return nil, 0, e
	}
	_, e = tx.Exec(r.Context(), `INSERT INTO infra.upload_session(id,media_id,storage_upload_ref,part_size_bytes,declared_size_bytes,state,expires_at,token_hash,expected_sha256) VALUES($1,$2,$3,$4,$5,'OPEN',$6,$7,$8)`, u.ID, u.MediaID, u.ID.String(), u.PartSize, u.Size, u.Expires, tokenHash(token), fingerprint)
	if e != nil {
		return nil, 0, e
	}
	tokens := map[int]string{}
	for number, offset := 1, int64(0); offset < u.Size; number, offset = number+1, offset+u.PartSize {
		size := min(u.PartSize, u.Size-offset)
		if fingerprint != nil {
			token, e = randomSecret()
			if e != nil {
				return nil, 0, e
			}
			if _, e = tx.Exec(r.Context(), `INSERT INTO infra.upload_part(upload_id,part_number,byte_count,token_hash) VALUES($1,$2,$3,$4)`, u.ID, number, size, tokenHash(token)); e != nil {
				return nil, 0, e
			}
		}
		u.Parts = append(u.Parts, uploadPart{Number: number, Size: size})
		tokens[number] = token
	}
	if e = tx.Commit(r.Context()); e != nil {
		return nil, 0, e
	}
	return uploadDTO(u, tokens), 201, nil
}
func (a *App) uploadStatus(w http.ResponseWriter, r *http.Request, actor *Actor) (any, int, error) {
	if e := a.mediaScope(r, actor); e != nil {
		return nil, 0, e
	}
	mid, e := id(r, "id")
	if e != nil {
		return nil, 0, e
	}
	tx, e := a.begin(r.Context())
	if e != nil {
		return nil, 0, e
	}
	defer tx.Rollback(r.Context())
	u, e := a.readUpload(r, tx, mid, true)
	if e != nil {
		return nil, 0, e
	}
	if e = tx.Commit(r.Context()); e != nil {
		return nil, 0, e
	}
	return uploadDTO(u, nil), 200, nil
}
func (a *App) renewUpload(w http.ResponseWriter, r *http.Request, actor *Actor) (any, int, error) {
	if e := a.mediaScope(r, actor); e != nil {
		return nil, 0, e
	}
	mid, e := id(r, "id")
	if e != nil {
		return nil, 0, e
	}
	version, e := expected(r)
	if e != nil {
		return nil, 0, e
	}
	var b struct {
		Parts []int `json:"partNumbers"`
	}
	if e = decode(r, &b); e != nil {
		return nil, 0, e
	}
	if len(b.Parts) < 1 || len(b.Parts) > media.MaxParts {
		return nil, 0, invalid("Choose valid photo parts")
	}
	tx, e := a.begin(r.Context())
	if e != nil {
		return nil, 0, e
	}
	defer tx.Rollback(r.Context())
	u, e := a.readUpload(r, tx, mid, true)
	if e != nil {
		return nil, 0, e
	}
	if u.Version != version {
		return nil, 0, conflict()
	}
	if u.State != "OPEN" || u.MediaState != "UPLOADING" || !u.Expires.After(time.Now()) {
		return nil, 0, failure(409, "UPLOAD_CLOSED", "Start a new photo upload")
	}
	seen := map[int]bool{}
	for _, n := range b.Parts {
		if n < 1 || n > len(u.Parts) || seen[n] {
			return nil, 0, invalid("Choose distinct photo parts within this upload")
		}
		seen[n] = true
	}
	tokens := map[int]string{}
	for _, n := range b.Parts {
		token, err := randomSecret()
		if err != nil {
			return nil, 0, err
		}
		tokens[n] = token
		if u.Hash == nil {
			_, e = tx.Exec(r.Context(), `UPDATE infra.upload_session SET token_hash=$2 WHERE id=$1`, u.ID, tokenHash(token))
		} else {
			_, e = tx.Exec(r.Context(), `UPDATE infra.upload_part SET token_hash=$3 WHERE upload_id=$1 AND part_number=$2`, u.ID, n, tokenHash(token))
		}
		if e != nil {
			return nil, 0, e
		}
	}
	u.Version++
	_, e = tx.Exec(r.Context(), `UPDATE infra.upload_session SET version=version+1 WHERE media_id=$1`, mid)
	if e != nil {
		return nil, 0, e
	}
	if e = tx.Commit(r.Context()); e != nil {
		return nil, 0, e
	}
	return uploadDTO(u, tokens), 200, nil
}
func (a *App) uploadPart(w http.ResponseWriter, r *http.Request, actor *Actor) (any, int, error) {
	if e := a.mediaScope(r, actor); e != nil {
		return nil, 0, e
	}
	mid, e := id(r, "id")
	if e != nil {
		return nil, 0, e
	}
	number, err := strconv.Atoi(r.PathValue("number"))
	if err != nil || number < 1 || number > media.MaxParts || strconv.Itoa(number) != r.PathValue("number") {
		return nil, 0, unavailable()
	}
	tx, e := a.begin(r.Context())
	if e != nil {
		return nil, 0, e
	}
	defer tx.Rollback(r.Context())
	u, e := a.readUpload(r, tx, mid, true)
	if e != nil {
		return nil, 0, e
	}
	if number > len(u.Parts) {
		return nil, 0, unavailable()
	}
	part := u.Parts[number-1]
	hash := tokenHash(r.URL.Query().Get("token"))
	if len(part.TokenHash) != 32 || subtle.ConstantTimeCompare(hash, part.TokenHash) != 1 {
		return nil, 0, forbidden()
	}
	if u.State != "OPEN" || u.MediaState != "UPLOADING" || !u.Expires.After(time.Now()) {
		return nil, 0, failure(410, "UPLOAD_EXPIRED", "Start a new photo upload")
	}
	raw, e := io.ReadAll(io.LimitReader(r.Body, part.Size+1))
	if e != nil || int64(len(raw)) != part.Size {
		return nil, 0, invalid("Photo byte count must match the upload request")
	}
	digest := sha256.Sum256(raw)
	etag := media.ETag(digest[:])
	if part.ETag != nil && *part.ETag != etag {
		return nil, 0, failure(409, "PART_CONFLICT", "The uploaded photo differs. Start a new upload")
	}
	key := media.OriginalKey(mid)
	if u.Hash != nil {
		key = media.PartKey(u.ID, number)
	}
	// Identical retries also repair a missing/corrupt file without changing its identity.
	if _, _, e = a.Files.Save(key, bytes.NewReader(raw), part.Size); e != nil {
		return nil, 0, e
	}
	if part.ETag == nil {
		if u.Hash != nil {
			if _, e = tx.Exec(r.Context(), `UPDATE infra.upload_part SET etag=$3 WHERE upload_id=$1 AND part_number=$2`, u.ID, number, etag); e != nil {
				return nil, 0, e
			}
		}
		if u.Hash == nil {
			_, e = tx.Exec(r.Context(), `UPDATE infra.upload_session SET part_etag=$2,version=version+1 WHERE media_id=$1`, mid, etag)
		} else {
			_, e = tx.Exec(r.Context(), `UPDATE infra.upload_session SET version=version+1 WHERE media_id=$1`, mid)
		}
		if e != nil {
			return nil, 0, e
		}
	}
	if e = tx.Commit(r.Context()); e != nil {
		return nil, 0, e
	}
	w.Header().Set("ETag", `"`+etag+`"`)
	return map[string]any{"number": number, "etag": etag}, 200, nil
}
func (a *App) completeUpload(w http.ResponseWriter, r *http.Request, actor *Actor) (any, int, error) {
	if e := a.mediaScope(r, actor); e != nil {
		return nil, 0, e
	}
	mid, e := id(r, "id")
	if e != nil {
		return nil, 0, e
	}
	var b struct {
		Parts []struct {
			Number int    `json:"number"`
			ETag   string `json:"etag"`
		} `json:"parts"`
	}
	if e = decode(r, &b); e != nil {
		return nil, 0, e
	}
	tx, e := a.begin(r.Context())
	if e != nil {
		return nil, 0, e
	}
	defer tx.Rollback(r.Context())
	u, e := a.readUpload(r, tx, mid, true)
	if e != nil {
		return nil, 0, e
	}
	if len(b.Parts) != len(u.Parts) || len(b.Parts) < 1 {
		return nil, 0, invalid("Complete every photo part first")
	}
	seen := map[int]bool{}
	for _, p := range b.Parts {
		if p.Number < 1 || p.Number > len(u.Parts) || seen[p.Number] || u.Parts[p.Number-1].ETag == nil || p.ETag != *u.Parts[p.Number-1].ETag {
			return nil, 0, invalid("Complete every distinct photo part with its correct hash")
		}
		seen[p.Number] = true
	}
	if u.State == "COMPLETE" {
		return map[string]any{"mediaId": mid, "state": u.MediaState}, 202, nil
	}
	if u.State != "OPEN" || !u.Expires.After(time.Now()) {
		return nil, 0, failure(410, "UPLOAD_EXPIRED", "Start a new photo upload")
	}
	var digest []byte
	if u.Hash == nil {
		f, err := a.Files.Open(media.OriginalKey(mid))
		if err != nil {
			return nil, 0, err
		}
		h := sha256.New()
		n, err := io.Copy(h, io.LimitReader(f, media.MaxBytes+1))
		f.Close()
		if err != nil || n != u.Size || media.ETag(h.Sum(nil)) != *u.ETag {
			return nil, 0, invalid("The uploaded photo is incomplete")
		}
		digest = h.Sum(nil)
	} else {
		parts := make([]media.StoredPart, 0, len(u.Parts))
		for _, p := range u.Parts {
			parts = append(parts, media.StoredPart{Key: media.PartKey(u.ID, p.Number), Size: p.Size, ETag: *p.ETag})
		}
		digest, e = a.Files.Assemble(media.OriginalKey(mid), parts, u.Hash)
		if errors.Is(e, media.ErrIntegrity) {
			return nil, 0, failure(422, "UPLOAD_INTEGRITY", "Photo integrity check failed. Remove this photo and add it again")
		}
		if e != nil {
			return nil, 0, e
		}
	}
	if _, e = tx.Exec(r.Context(), `UPDATE social.media_asset SET state='QUARANTINED',sha256=$2 WHERE id=$1 AND state='UPLOADING'`, mid, digest); e != nil {
		return nil, 0, e
	}
	if _, e = tx.Exec(r.Context(), `UPDATE infra.upload_session SET state='COMPLETE',token_hash=NULL,version=version+1 WHERE media_id=$1`, mid); e != nil {
		return nil, 0, e
	}
	if u.Hash != nil {
		if _, e = tx.Exec(r.Context(), `UPDATE infra.upload_part SET token_hash=NULL WHERE upload_id=$1`, u.ID); e != nil {
			return nil, 0, e
		}
	}
	if e = tx.Commit(r.Context()); e != nil {
		return nil, 0, e
	}
	return map[string]any{"mediaId": mid, "state": "QUARANTINED"}, 202, nil
}
func (a *App) abortUpload(w http.ResponseWriter, r *http.Request, actor *Actor) (any, int, error) {
	if e := a.mediaScope(r, actor); e != nil {
		return nil, 0, e
	}
	mid, e := id(r, "id")
	if e != nil {
		return nil, 0, e
	}
	version, e := expected(r)
	if e != nil {
		return nil, 0, e
	}
	tx, e := a.begin(r.Context())
	if e != nil {
		return nil, 0, e
	}
	defer tx.Rollback(r.Context())
	u, e := a.readUpload(r, tx, mid, true)
	if e != nil {
		return nil, 0, e
	}
	if version != u.Version {
		return nil, 0, conflict()
	}
	var attached bool
	if e = tx.QueryRow(r.Context(), `SELECT authz.media_attached($1)`, mid).Scan(&attached); e != nil {
		return nil, 0, e
	}
	if attached {
		return nil, 0, failure(409, "EVIDENCE_RETAINED", "This photo belongs to a submitted report")
	}
	var key *string
	if e = tx.QueryRow(r.Context(), `SELECT derivative_key FROM social.media_asset WHERE id=$1`, mid).Scan(&key); e != nil {
		return nil, 0, e
	}
	// Cancel while the media is still approved so task RLS can see the pending jobs.
	if _, e = tx.Exec(r.Context(), `UPDATE infra.analysis_task SET state='CANCELLED',result=NULL,error_code='CANCELLED' WHERE job_id IN (SELECT id FROM infra.analysis_job WHERE media_id=$1)`, mid); e != nil {
		return nil, 0, e
	}
	if _, e = tx.Exec(r.Context(), `UPDATE infra.analysis_job SET state='CANCELLED',version=version+1,lease_token=NULL WHERE media_id=$1`, mid); e != nil {
		return nil, 0, e
	}
	if _, e = tx.Exec(r.Context(), `UPDATE social.media_asset SET state='REVOKED',authorization_version=authorization_version+1,processing_token=NULL,storage_cleanup_at=NULL WHERE id=$1`, mid); e != nil {
		return nil, 0, e
	}
	if _, e = tx.Exec(r.Context(), `UPDATE infra.upload_session SET state='ABORTED',token_hash=NULL,version=version+1 WHERE media_id=$1`, mid); e != nil {
		return nil, 0, e
	}
	if u.Hash != nil {
		if _, e = tx.Exec(r.Context(), `UPDATE infra.upload_part SET token_hash=NULL WHERE upload_id=$1`, u.ID); e != nil {
			return nil, 0, e
		}
	}
	if e = tx.Commit(r.Context()); e != nil {
		return nil, 0, e
	}
	a.Files.Remove(media.OriginalKey(mid))
	a.Files.RemoveParts(u.ID)
	if key != nil {
		a.Files.Remove(*key)
	}
	return nil, 204, nil
}

type MediaView struct {
	ID                   uuid.UUID        `json:"id"`
	State                string           `json:"state"`
	AuthorizationVersion int64            `json:"authorizationVersion"`
	RejectionCode        *string          `json:"rejectionCode"`
	Derivatives          []DerivativeView `json:"derivatives"`
}
type DerivativeView struct {
	ID     uuid.UUID `json:"id"`
	URL    string    `json:"url"`
	Width  int       `json:"width"`
	Height int       `json:"height"`
}

func (a *App) getMedia(w http.ResponseWriter, r *http.Request, actor *Actor) (any, int, error) {
	if e := a.mediaScope(r, actor); e != nil {
		return nil, 0, e
	}
	mid, e := id(r, "id")
	if e != nil {
		return nil, 0, e
	}
	v := MediaView{Derivatives: []DerivativeView{}}
	e = a.store(r.Context()).QueryRow(r.Context(), `SELECT id,state,authorization_version,rejection_code FROM social.media_asset WHERE id=$1`, mid).Scan(&v.ID, &v.State, &v.AuthorizationVersion, &v.RejectionCode)
	if e != nil {
		return nil, 0, e
	}
	if v.State == "APPROVED" {
		d := DerivativeView{URL: "/api/media/" + mid.String() + "/content"}
		e = a.store(r.Context()).QueryRow(r.Context(), `SELECT d.id,d.pixel_width,d.pixel_height FROM infra.media_derivative d JOIN social.media_asset m ON m.id=d.media_id WHERE m.id=$1 AND d.storage_key=m.derivative_key AND d.revoked_at IS NULL`, mid).Scan(&d.ID, &d.Width, &d.Height)
		if e != nil {
			return nil, 0, e
		}
		v.Derivatives = append(v.Derivatives, d)
	}
	return v, 200, nil
}
func (a *App) mediaContent(w http.ResponseWriter, r *http.Request, actor *Actor) (any, int, error) {
	if e := a.mediaScope(r, actor); e != nil {
		return nil, 0, e
	}
	mid, e := id(r, "id")
	if e != nil {
		return nil, 0, e
	}
	var key string
	tx, e := a.begin(r.Context())
	if e != nil {
		return nil, 0, e
	}
	defer tx.Rollback(r.Context())
	e = tx.QueryRow(r.Context(), `SELECT derivative_key FROM social.media_asset WHERE id=$1 AND state='APPROVED' FOR SHARE`, mid).Scan(&key)
	if e != nil {
		return nil, 0, e
	}
	f, e := a.Files.Open(key)
	if e != nil {
		return nil, 0, e
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil || info.Size() > media.MaxDerivativeBytes {
		return nil, 0, errors.New("invalid derivative")
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
	w.Header().Set("Content-Disposition", "inline; filename=report-photo.png")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.WriteHeader(200)
	_, _ = io.Copy(w, f)
	return nil, -1, nil
}

type AnalysisTask struct {
	ID        uuid.UUID       `json:"id"`
	Kind      string          `json:"kind"`
	State     string          `json:"state"`
	Result    json.RawMessage `json:"result"`
	ErrorCode *string         `json:"errorCode"`
	Retryable bool            `json:"retryable"`
}
type AnalysisView struct {
	ID      uuid.UUID      `json:"id"`
	MediaID uuid.UUID      `json:"mediaId"`
	State   string         `json:"state"`
	Version int64          `json:"version"`
	Tasks   []AnalysisTask `json:"tasks"`
}

func (a *App) analysis(r *http.Request, jid uuid.UUID) (AnalysisView, error) {
	v := AnalysisView{Tasks: []AnalysisTask{}}
	tx, e := a.begin(r.Context())
	if e != nil {
		return v, e
	}
	defer tx.Rollback(r.Context())
	// Lock order matches worker/cancellation. Return the job and all task results
	// from one stable, reauthorized transaction rather than mixed snapshots.
	var mid uuid.UUID
	if e = tx.QueryRow(r.Context(), `SELECT media_id FROM infra.analysis_job WHERE id=$1`, jid).Scan(&mid); e != nil {
		return v, e
	}
	var locked uuid.UUID
	if e = tx.QueryRow(r.Context(), `SELECT id FROM social.media_asset WHERE id=$1 AND state='APPROVED' FOR SHARE`, mid).Scan(&locked); e != nil {
		return v, e
	}
	if e = tx.QueryRow(r.Context(), `SELECT id,media_id,state,version FROM infra.analysis_job WHERE id=$1 FOR SHARE`, jid).Scan(&v.ID, &v.MediaID, &v.State, &v.Version); e != nil {
		return v, e
	}
	rows, e := tx.Query(r.Context(), `SELECT id,task_kind,state,result,error_code,attempt FROM infra.analysis_task WHERE job_id=$1 ORDER BY task_kind`, jid)
	if e != nil {
		return v, e
	}
	for rows.Next() {
		var t AnalysisTask
		var attempt int
		if e = rows.Scan(&t.ID, &t.Kind, &t.State, &t.Result, &t.ErrorCode, &attempt); e != nil {
			rows.Close()
			return v, e
		}
		t.Retryable = t.State == "FAILED" && attempt < 3 && t.ErrorCode != nil && (*t.ErrorCode == "OCR_TIMEOUT" || *t.ErrorCode == "OCR_FAILED" || *t.ErrorCode == "VISION_TIMEOUT" || *t.ErrorCode == "VISION_FAILED" || *t.ErrorCode == "FILE_UNAVAILABLE")
		v.Tasks = append(v.Tasks, t)
	}
	rows.Close()
	if e = rows.Err(); e != nil {
		return v, e
	}
	return v, tx.Commit(r.Context())
}

func (a *App) createAnalysis(w http.ResponseWriter, r *http.Request, actor *Actor) (any, int, error) {
	if e := a.mediaScope(r, actor); e != nil {
		return nil, 0, e
	}
	mid, e := id(r, "id")
	if e != nil {
		return nil, 0, e
	}
	var b struct {
		Tasks    []string `json:"tasks"`
		Language string   `json:"languageTag"`
	}
	if e = decode(r, &b); e != nil {
		return nil, 0, e
	}
	if len(b.Tasks) < 1 || len(b.Tasks) > 5 || !textValid(b.Language, 2, 40) {
		return nil, 0, invalid("Choose analysis tasks and a language")
	}
	seen := map[string]bool{}
	for _, kind := range b.Tasks {
		if seen[kind] || (kind != "QUALITY" && kind != "OCR" && kind != "ISSUE_DETECTION" && kind != "REDACTION" && kind != "VOICE_TRANSCRIPTION") {
			return nil, 0, invalid("Choose distinct supported task names")
		}
		seen[kind] = true
	}
	tx, e := a.begin(r.Context())
	if e != nil {
		return nil, 0, e
	}
	defer tx.Rollback(r.Context())
	var alias uuid.UUID
	var hash []byte
	var version int64
	e = tx.QueryRow(r.Context(), `SELECT report_alias_ref,sha256,authorization_version FROM social.media_asset WHERE id=$1 AND state='APPROVED' AND authz.media_owner(id) FOR UPDATE`, mid).Scan(&alias, &hash, &version)
	if e != nil {
		return nil, 0, e
	}
	var existing int
	if e = tx.QueryRow(r.Context(), `SELECT count(*) FROM infra.analysis_job WHERE media_id=$1`, mid).Scan(&existing); e != nil {
		return nil, 0, e
	}
	if existing >= 5 {
		return nil, 0, failure(429, "ANALYSIS_QUOTA", "Use existing results or continue with your own text")
	}
	jid := uuid.New()
	_, e = tx.Exec(r.Context(), `INSERT INTO infra.analysis_job(id,media_id,report_alias_ref,source_sha256,authorization_version,language_tag,state,expires_at,session_hash,auth_mode,auth_issuer) VALUES($1,$2,$3,$4,$5,$6,'QUEUED',statement_timestamp()+interval '24 hours',$7,$8,$9)`, jid, mid, alias, hash, version, b.Language, tokenHash(scope(r.Context()).Session), a.Config.AuthMode, a.Config.OIDCIssuer)
	if e != nil {
		return nil, 0, e
	}
	for _, kind := range b.Tasks {
		state, code := "QUEUED", ""
		if kind != "OCR" && kind != "QUALITY" && !(kind == "ISSUE_DETECTION" && a.Config.VisionBinary != "") {
			state, code = "UNSUPPORTED", "CAPABILITY_UNAVAILABLE"
		}
		if kind == "OCR" && b.Language != "en" && b.Language != "en-IN" && b.Language != "en-US" && b.Language != "en-GB" {
			state, code = "UNSUPPORTED", "LANGUAGE_UNAVAILABLE"
		}
		if _, e = tx.Exec(r.Context(), `INSERT INTO infra.analysis_task(id,job_id,task_kind,state,result_schema_version,error_code) VALUES($1,$2,$3,$4,1,nullif($5,''))`, uuid.New(), jid, kind, state, code); e != nil {
			return nil, 0, e
		}
	}
	if e = tx.Commit(r.Context()); e != nil {
		return nil, 0, e
	}
	v, e := a.analysis(r, jid)
	return v, 202, e
}
func (a *App) getAnalysis(w http.ResponseWriter, r *http.Request, actor *Actor) (any, int, error) {
	if e := a.mediaScope(r, actor); e != nil {
		return nil, 0, e
	}
	jid, e := id(r, "id")
	if e != nil {
		return nil, 0, e
	}
	v, e := a.analysis(r, jid)
	return v, 200, e
}
func (a *App) changeAnalysis(w http.ResponseWriter, r *http.Request, actor *Actor, cancel bool) (any, int, error) {
	if e := a.mediaScope(r, actor); e != nil {
		return nil, 0, e
	}
	jid, e := id(r, "id")
	if e != nil {
		return nil, 0, e
	}
	version, e := expected(r)
	if e != nil {
		return nil, 0, e
	}
	var b struct {
		Tasks []string `json:"tasks"`
	}
	if !cancel {
		if e = decode(r, &b); e != nil {
			return nil, 0, e
		}
		if len(b.Tasks) < 1 || len(b.Tasks) > 5 {
			return nil, 0, invalid("Choose failed tasks to retry")
		}
	}
	v, e := a.analysis(r, jid)
	if e != nil {
		return nil, 0, e
	}
	tx, e := a.begin(r.Context())
	if e != nil {
		return nil, 0, e
	}
	defer tx.Rollback(r.Context())
	var mid uuid.UUID
	if e = tx.QueryRow(r.Context(), `SELECT id FROM social.media_asset WHERE id=$1 FOR UPDATE`, v.MediaID).Scan(&mid); e != nil {
		return nil, 0, e
	}
	var state string
	var current int64
	if e = tx.QueryRow(r.Context(), `SELECT state,version FROM infra.analysis_job WHERE id=$1 FOR UPDATE`, jid).Scan(&state, &current); e != nil {
		return nil, 0, e
	}
	if current != version {
		return nil, 0, conflict()
	}
	if cancel {
		if state != "QUEUED" && state != "RUNNING" {
			return nil, 0, failure(409, "ANALYSIS_STATE", "This analysis has already finished")
		}
		if _, e = tx.Exec(r.Context(), `UPDATE infra.analysis_task SET state='CANCELLED',result=NULL,error_code='CANCELLED' WHERE job_id=$1`, jid); e != nil {
			return nil, 0, e
		}
		_, e = tx.Exec(r.Context(), `UPDATE infra.analysis_job SET state='CANCELLED',version=version+1,lease_token=NULL,lease_until=NULL WHERE id=$1`, jid)
	} else {
		if state != "PARTIAL" && state != "FAILED" {
			return nil, 0, failure(409, "ANALYSIS_STATE", "Only failed tasks in a finished analysis can be retried")
		}
		seen := map[string]bool{}
		for _, kind := range b.Tasks {
			if seen[kind] {
				return nil, 0, invalid("Choose distinct tasks")
			}
			seen[kind] = true
			tag, err := tx.Exec(r.Context(), `UPDATE infra.analysis_task SET state='QUEUED',result=NULL,error_code=NULL,completed_at=NULL WHERE job_id=$1 AND task_kind=$2 AND state='FAILED' AND attempt<3 AND error_code IN ('OCR_TIMEOUT','OCR_FAILED','VISION_TIMEOUT','VISION_FAILED','FILE_UNAVAILABLE')`, jid, kind)
			if err != nil {
				return nil, 0, err
			}
			if tag.RowsAffected() != 1 {
				return nil, 0, invalid("This task cannot be retried")
			}
		}
		_, e = tx.Exec(r.Context(), `UPDATE infra.analysis_job SET state='QUEUED',version=version+1,lease_token=NULL,lease_until=NULL,session_hash=$2,auth_mode=$3,auth_issuer=$4 WHERE id=$1`, jid, tokenHash(scope(r.Context()).Session), a.Config.AuthMode, a.Config.OIDCIssuer)
	}
	if e != nil {
		return nil, 0, e
	}
	if e = tx.Commit(r.Context()); e != nil {
		return nil, 0, e
	}
	if cancel {
		return nil, 204, nil
	}
	result, e := a.analysis(r, jid)
	return result, 202, e
}
func (a *App) retryAnalysis(w http.ResponseWriter, r *http.Request, actor *Actor) (any, int, error) {
	return a.changeAnalysis(w, r, actor, false)
}
func (a *App) cancelAnalysis(w http.ResponseWriter, r *http.Request, actor *Actor) (any, int, error) {
	return a.changeAnalysis(w, r, actor, true)
}

package app

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lavkushry/JanSetu-AI/services/backend/internal/media"
)

func mediaWorker(t *testing.T) *media.Worker {
	t.Helper()
	a := testApp(t)
	engine := media.OCR{Binary: "tesseract"}
	model, e := engine.Version(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	return &media.Worker{DB: integrationMediaWorker, Files: a.Files, Engine: engine, Model: model}
}
func uploadFixture(t *testing.T, c client, submission uuid.UUID, raw []byte) uuid.UUID {
	t.Helper()
	r := c.request("POST", "media/uploads", map[string]any{"clientSubmissionId": submission, "purpose": "REPORT", "mimeType": "image/png", "byteCount": len(raw)}, 0, "")
	if r.Code != 201 {
		t.Fatalf("create upload: %d %s", r.Code, r.Body)
	}
	var u struct {
		MediaID uuid.UUID `json:"mediaId"`
		Parts   []struct {
			URL string `json:"url"`
		} `json:"parts"`
	}
	if e := json.Unmarshal(r.Body.Bytes(), &u); e != nil {
		t.Fatal(e)
	}
	req := httptest.NewRequest("PUT", strings.Replace(u.Parts[0].URL, "/api/", "/v1/", 1), bytes.NewReader(raw))
	req.AddCookie(c.cookie)
	req.Header.Set("X-JanSetu-CSRF", "1")
	put := httptest.NewRecorder()
	c.app.Handler().ServeHTTP(put, req)
	if put.Code != 200 {
		t.Fatalf("upload: %d %s", put.Code, put.Body)
	}
	var part map[string]any
	json.Unmarshal(put.Body.Bytes(), &part)
	completed := c.request("POST", "media/"+u.MediaID.String()+"/complete", map[string]any{"parts": []any{part}}, 0, "")
	if completed.Code != 202 {
		t.Fatalf("complete: %d %s", completed.Code, completed.Body)
	}
	return u.MediaID
}
func runMedia(t *testing.T) {
	t.Helper()
	if e := mediaWorker(t).Once(context.Background()); e != nil {
		t.Fatal(e)
	}
}
func readAnalysis(t *testing.T, c client, jid uuid.UUID) AnalysisView {
	t.Helper()
	r := c.request("GET", "analyses/"+jid.String(), nil, 0, "")
	if r.Code != 200 {
		t.Fatalf("analysis: %d %s", r.Code, r.Body)
	}
	var v AnalysisView
	if e := json.Unmarshal(r.Body.Bytes(), &v); e != nil {
		t.Fatal(e)
	}
	return v
}
func startAnalysis(t *testing.T, c client, mid uuid.UUID, kinds []string, language string) uuid.UUID {
	t.Helper()
	r := c.request("POST", "media/"+mid.String()+"/analyses", map[string]any{"tasks": kinds, "languageTag": language}, 0, "")
	if r.Code != 202 {
		t.Fatalf("start: %d %s", r.Code, r.Body)
	}
	var v AnalysisView
	json.Unmarshal(r.Body.Bytes(), &v)
	return v.ID
}
func TestPrivatePhotoOCRReportAndScope(t *testing.T) {
	a := testApp(t)
	owner := login(t, a, 0)
	other := login(t, a, 1)
	coordinator := login(t, a, 2)
	sub := uuid.New()
	raw, e := os.ReadFile("../media/testdata/notice.png")
	if e != nil {
		t.Fatal(e)
	}
	mid := uploadFixture(t, owner, sub, raw)
	if r := other.request("GET", "media/"+mid.String(), nil, 0, ""); r.Code != 404 {
		t.Fatalf("foreign media %d", r.Code)
	}
	if r := owner.request("GET", "media/"+mid.String()+"/content", nil, 0, ""); r.Code != 404 {
		t.Fatal("quarantine served")
	}
	runMedia(t)
	if r := owner.request("GET", "media/"+mid.String()+"/content", nil, 0, ""); r.Code != 200 || r.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("private derivative: %d %s", r.Code, r.Body)
	}
	jid := startAnalysis(t, owner, mid, []string{"QUALITY", "OCR", "ISSUE_DETECTION"}, "en-IN")
	runMedia(t)
	v := readAnalysis(t, owner, jid)
	if v.State != "PARTIAL" {
		t.Fatalf("partial expected: %+v", v)
	}
	var ocr AnalysisTask
	for _, task := range v.Tasks {
		if task.Kind == "OCR" {
			ocr = task
		}
		if task.Kind == "ISSUE_DETECTION" && (task.State != "UNSUPPORTED" || task.Retryable) {
			t.Fatal(task)
		}
	}
	if ocr.State != "SUCCEEDED" {
		t.Fatal(ocr)
	}
	var result media.ImageResult
	json.Unmarshal(ocr.Result, &result)
	if len(result.Regions) == 0 || result.SourceSHA256 == "" {
		t.Fatal("real extraction missing")
	}
	if r := other.request("GET", "analyses/"+jid.String(), nil, 0, ""); r.Code != 404 {
		t.Fatal("foreign OCR exposed")
	}
	region := result.Regions[0]
	b := ReportInput{ClientSubmissionID: sub, Statement: "Fictional broken street light for private photo testing", LanguageTag: "en-IN", Category: "LIGHT", LocationLabel: "Fictional test crossing", PublicationPreference: "SANITIZED_RECEIPT"}
	b.MediaIDs = []uuid.UUID{mid}
	b.OCRCorrections = []OCRCorrection{{TaskID: ocr.ID, RegionID: region.ID, OriginalText: region.Text, CorrectedText: "Resident corrected word", AppliedAt: time.Now().UTC()}}
	forged := b
	forged.OCRCorrections = append([]OCRCorrection{}, b.OCRCorrections...)
	forged.OCRCorrections[0].OriginalText = "Forged original text"
	mustStatus(t, owner.request("POST", "service-reports", forged, 0, sub.String()), 422)
	first := owner.request("POST", "service-reports", b, 0, sub.String())
	if first.Code != 201 {
		t.Fatalf("attach: %d %s", first.Code, first.Body)
	}
	again := owner.request("POST", "service-reports", b, 0, sub.String())
	if again.Code != 201 || !reflect.DeepEqual(parsed[map[string]any](t, first), parsed[map[string]any](t, again)) {
		t.Fatal("report retry did not deduplicate")
	}
	var ack struct {
		ID uuid.UUID `json:"id"`
	}
	json.Unmarshal(first.Body.Bytes(), &ack)
	mine := owner.request("GET", "my-reports/"+ack.ID.String(), nil, 0, "")
	if mine.Code != 200 || !strings.Contains(mine.Body.String(), mid.String()) {
		t.Fatal(mine.Body.String())
	}
	if r := coordinator.request("GET", "media/"+mid.String()+"/content", nil, 0, ""); r.Code != 200 {
		t.Fatalf("scoped coordinator photo: %d %s", r.Code, r.Body)
	}
	// Submitted evidence cannot be deleted as a draft attachment.
	status := owner.request("GET", "media/"+mid.String()+"/upload", nil, 0, "")
	var upload struct {
		Version int64 `json:"version"`
	}
	json.Unmarshal(status.Body.Bytes(), &upload)
	if r := owner.request("DELETE", "media/"+mid.String()+"/upload", nil, upload.Version, ""); r.Code != 409 {
		t.Fatal("submitted evidence removed")
	}
	for _, path := range []string{"feed", "search?q=BROKEN"} {
		r := (client{app: a}).request("GET", path, nil, 0, "")
		if strings.Contains(r.Body.String(), mid.String()) || strings.Contains(r.Body.String(), jid.String()) || strings.Contains(r.Body.String(), "Resident corrected word") {
			t.Fatal("private data in public DTO")
		}
	}
	persisted := readAnalysis(t, owner, jid)
	for _, task := range persisted.Tasks {
		if task.Kind == "OCR" && !bytes.Equal(task.Result, ocr.Result) {
			t.Fatal("correction mutated raw OCR")
		}
	}
}
func TestInvalidImagesUnsupportedLanguageAndCancellation(t *testing.T) {
	a := testApp(t)
	owner := login(t, a, 0)
	mid := uploadFixture(t, owner, uuid.New(), []byte("<svg>not safe</svg>"))
	runMedia(t)
	r := owner.request("GET", "media/"+mid.String(), nil, 0, "")
	if !strings.Contains(r.Body.String(), "REJECTED") {
		t.Fatal(r.Body.String())
	}
	if r = owner.request("GET", "media/"+mid.String()+"/content", nil, 0, ""); r.Code != 404 {
		t.Fatal("invalid image served")
	}
	var raw bytes.Buffer
	png.Encode(&raw, image.NewRGBA(image.Rect(0, 0, 100, 100)))
	mid = uploadFixture(t, owner, uuid.New(), raw.Bytes())
	runMedia(t)
	jid := startAnalysis(t, owner, mid, []string{"QUALITY", "OCR"}, "hi-IN")
	runMedia(t)
	v := readAnalysis(t, owner, jid)
	if v.State != "PARTIAL" {
		t.Fatal(v)
	}
	for _, task := range v.Tasks {
		if task.Kind == "OCR" && (task.State != "UNSUPPORTED" || len(task.Result) > 0 && string(task.Result) != "null") {
			t.Fatal("fabricated unsupported language result")
		}
	}
	jid = startAnalysis(t, owner, mid, []string{"OCR"}, "en-IN")
	v = readAnalysis(t, owner, jid)
	if r = owner.request("DELETE", "analyses/"+jid.String(), nil, v.Version+1, ""); r.Code != 412 {
		t.Fatal("stale cancel accepted")
	}
	if r = owner.request("DELETE", "analyses/"+jid.String(), nil, v.Version, ""); r.Code != 204 {
		t.Fatal(r.Body.String())
	}
	runMedia(t)
	v = readAnalysis(t, owner, jid)
	if v.State != "CANCELLED" {
		t.Fatal(v)
	}
	for _, task := range v.Tasks {
		if len(task.Result) > 0 && string(task.Result) != "null" {
			t.Fatal("cancelled result applied")
		}
	}
}

func TestFailedTaskRetryRetainsSuccessAndRevocationFencesWorker(t *testing.T) {
	a := testApp(t)
	owner := login(t, a, 0)
	raw, e := os.ReadFile("../media/testdata/notice.png")
	if e != nil {
		t.Fatal(e)
	}
	mid := uploadFixture(t, owner, uuid.New(), raw)
	runMedia(t)
	jid := startAnalysis(t, owner, mid, []string{"QUALITY", "OCR"}, "en-IN")
	// Fault-inject a dependency failure, never synthetic successful OCR results.
	failing := t.TempDir() + "/ocr-unavailable"
	if e = os.WriteFile(failing, []byte("#!/bin/sh\nexit 1\n"), 0700); e != nil {
		t.Fatal(e)
	}
	worker := mediaWorker(t)
	worker.Engine = media.OCR{Binary: failing}
	if e = worker.Once(context.Background()); e != nil {
		t.Fatal(e)
	}
	v := readAnalysis(t, owner, jid)
	if v.State != "PARTIAL" {
		t.Fatal(v)
	}
	var quality json.RawMessage
	for _, task := range v.Tasks {
		if task.Kind == "QUALITY" {
			quality = task.Result
		}
		if task.Kind == "OCR" && (task.State != "FAILED" || !task.Retryable) {
			t.Fatal(task)
		}
	}
	r := owner.request("POST", "analyses/"+jid.String()+"/retry", map[string]any{"tasks": []string{"QUALITY"}}, v.Version, "")
	if r.Code != 422 {
		t.Fatal("successful task retried")
	}
	r = owner.request("POST", "analyses/"+jid.String()+"/retry", map[string]any{"tasks": []string{"OCR"}}, v.Version, "")
	if r.Code != 202 {
		t.Fatalf("retry: %d %s", r.Code, r.Body)
	}
	runMedia(t)
	v = readAnalysis(t, owner, jid)
	if v.State != "SUCCEEDED" {
		t.Fatal(v)
	}
	for _, task := range v.Tasks {
		if task.Kind == "QUALITY" && !bytes.Equal(task.Result, quality) {
			t.Fatal("retry erased successful result")
		}
	}
	// A newly queued job is fenced when the session that requested it is revoked.
	jid = startAnalysis(t, owner, mid, []string{"OCR"}, "en-IN")
	r = owner.request("POST", "me/logout", nil, 0, "")
	mustStatus(t, r, 204)
	runMedia(t)
	owner = login(t, a, 0)
	v = readAnalysis(t, owner, jid)
	if v.State != "CANCELLED" {
		t.Fatal(v)
	}
	for _, task := range v.Tasks {
		if len(task.Result) > 0 && string(task.Result) != "null" {
			t.Fatal("revoked session persisted result")
		}
	}
	// Runtime separation is exercised with the actual media-worker credential.
	if _, e = a.DB.Exec(context.Background(), `SELECT authz.report_ocr_region($1,$2,'word-1')`, uuid.New(), uuid.New()); e == nil {
		t.Fatal("social credential can call private OCR helper")
	}
	if _, e = a.Publication.Exec(context.Background(), `SELECT authz.media_retained($1)`, mid); e == nil {
		t.Fatal("publication credential can call worker retention helper")
	}
	if _, e = integrationMediaWorker.Exec(context.Background(), `SELECT statement FROM ops.report LIMIT 1`); e == nil {
		t.Fatal("media worker read report text")
	}
	if _, e = integrationMediaWorker.Exec(context.Background(), `SELECT token_hash FROM identity.session LIMIT 1`); e == nil {
		t.Fatal("media worker read session tokens")
	}
}
func TestUploadExpiryCapabilitiesAndForeignAttachment(t *testing.T) {
	a := testApp(t)
	owner := login(t, a, 0)
	other := login(t, a, 1)
	sub := uuid.New()
	r := owner.request("POST", "media/uploads", map[string]any{"clientSubmissionId": sub, "purpose": "REPORT", "mimeType": "image/png", "byteCount": 3}, 0, "")
	mustStatus(t, r, 201)
	var u struct {
		MediaID uuid.UUID `json:"mediaId"`
		Version int64     `json:"version"`
		Parts   []struct {
			URL string `json:"url"`
		} `json:"parts"`
	}
	json.Unmarshal(r.Body.Bytes(), &u)
	req := httptest.NewRequest("PUT", strings.Replace(u.Parts[0].URL, "/api/", "/v1/", 1), strings.NewReader("abc"))
	req.AddCookie(other.cookie)
	req.Header.Set("X-JanSetu-CSRF", "1")
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, req)
	mustStatus(t, w, 404)
	if _, e := integrationAdmin.Exec(context.Background(), `UPDATE infra.upload_session SET expires_at=statement_timestamp()-interval '1 second' WHERE media_id=$1`, u.MediaID); e != nil {
		t.Fatal(e)
	}
	req = httptest.NewRequest("PUT", strings.Replace(u.Parts[0].URL, "/api/", "/v1/", 1), strings.NewReader("abc"))
	req.AddCookie(owner.cookie)
	req.Header.Set("X-JanSetu-CSRF", "1")
	w = httptest.NewRecorder()
	a.Handler().ServeHTTP(w, req)
	mustStatus(t, w, 410)
	runMedia(t)
	if r = owner.request("GET", "media/"+u.MediaID.String(), nil, 0, ""); !strings.Contains(r.Body.String(), "UPLOAD_EXPIRED") {
		t.Fatal(r.Body.String())
	}
	raw, e := os.ReadFile("../media/testdata/notice.png")
	if e != nil {
		t.Fatal(e)
	}
	mid := uploadFixture(t, owner, uuid.New(), raw)
	runMedia(t)
	input := ReportInput{ClientSubmissionID: uuid.New(), Statement: "Fictional report with another resident's photo", LanguageTag: "en-IN", Category: "OTHER", LocationLabel: "Fictional test area", PublicationPreference: "PRIVATE", MediaIDs: []uuid.UUID{mid}}
	r = other.request("POST", "service-reports", input, 0, input.ClientSubmissionID.String())
	mustStatus(t, r, 422)
	var exists bool
	if e = integrationAdmin.QueryRow(context.Background(), `SELECT EXISTS(SELECT FROM ops.report WHERE client_submission_id=$1)`, input.ClientSubmissionID).Scan(&exists); e != nil || exists {
		t.Fatal("failed attachment left a report")
	}
}

func TestCancellationDuringOCRDiscardsActualLateOutput(t *testing.T) {
	a := testApp(t)
	owner := login(t, a, 0)
	raw, e := os.ReadFile("../media/testdata/notice.png")
	if e != nil {
		t.Fatal(e)
	}
	mid := uploadFixture(t, owner, uuid.New(), raw)
	runMedia(t)
	jid := startAnalysis(t, owner, mid, []string{"OCR"}, "en-IN")
	dir := t.TempDir()
	started := dir + "/started"
	release := dir + "/release"
	script := dir + "/delayed-ocr"
	// Trusted test-only wrapper holds an actual Tesseract invocation outside its
	// database transaction while the resident cancels the job.
	body := "#!/bin/sh\ntouch '" + started + "'\nwhile [ ! -f '" + release + "' ]; do sleep 0.01; done\nexec /usr/bin/tesseract \"$@\"\n"
	if e = os.WriteFile(script, []byte(body), 0700); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	defer os.WriteFile(release, nil, 0600)
	worker := mediaWorker(t)
	worker.Engine = media.OCR{Binary: script}
	finished := make(chan error, 1)
	go func() { finished <- worker.Once(ctx) }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, e = os.Stat(started); e == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("OCR did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	v := readAnalysis(t, owner, jid)
	if v.State != "RUNNING" {
		t.Fatal(v)
	}
	mustStatus(t, owner.request("DELETE", "analyses/"+jid.String(), nil, v.Version, ""), 204)
	if e = os.WriteFile(release, nil, 0600); e != nil {
		t.Fatal(e)
	}
	select {
	case e = <-finished:
		if e != nil {
			t.Fatal(e)
		}
	case <-ctx.Done():
		t.Fatal("worker did not finish")
	}
	v = readAnalysis(t, owner, jid)
	if v.State != "CANCELLED" {
		t.Fatal(v)
	}
	for _, task := range v.Tasks {
		if len(task.Result) > 0 && string(task.Result) != "null" {
			t.Fatal("late OCR persisted after cancellation")
		}
	}
}

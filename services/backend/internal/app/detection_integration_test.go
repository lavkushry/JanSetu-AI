package app

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lavkushry/JanSetu-AI/services/backend/internal/media"
)

func detectionApp(t *testing.T) (*App, *media.Worker) {
	t.Helper()
	base := testApp(t)
	binary := os.Getenv("JANSETU_VISION_BINARY")
	if binary == "" {
		t.Skip("set JANSETU_VISION_BINARY; CI requires actual model inference")
	}
	cfg := base.Config
	cfg.VisionBinary = binary
	a := cloneTestApp(t, cfg)
	worker := mediaWorker(t)
	worker.Vision = &media.Detector{Binary: binary}
	model, err := worker.Vision.Version(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	worker.VisionModel = model
	return a, worker
}

func TestImageRecognitionRetryPreservesOCRAndScope(t *testing.T) {
	a, worker := detectionApp(t)
	owner, other := login(t, a, 0), login(t, a, 1)
	raw, err := os.ReadFile("../media/testdata/notice.png")
	if err != nil {
		t.Fatal(err)
	}
	mid := uploadFixture(t, owner, uuid.New(), raw)
	if err = worker.Once(context.Background()); err != nil {
		t.Fatal(err)
	}
	jid := startAnalysis(t, owner, mid, []string{"QUALITY", "OCR", "ISSUE_DETECTION"}, "en-IN")
	actual := worker.Vision.Binary
	failure := filepath.Join(t.TempDir(), "unavailable")
	if err = os.WriteFile(failure, []byte("#!/bin/sh\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	worker.Vision.Binary = failure
	if err = worker.Once(context.Background()); err != nil {
		t.Fatal(err)
	}
	view := readAnalysis(t, owner, jid)
	if view.State != "PARTIAL" {
		t.Fatal(view)
	}
	preserved := map[string]json.RawMessage{}
	for _, task := range view.Tasks {
		if task.Kind == "ISSUE_DETECTION" {
			if task.State != "FAILED" || !task.Retryable || task.ErrorCode == nil || *task.ErrorCode != "VISION_FAILED" {
				t.Fatal(task)
			}
		} else {
			if task.State != "SUCCEEDED" {
				t.Fatal(task)
			}
			preserved[task.Kind] = task.Result
		}
	}
	mustStatus(t, other.request("GET", "analyses/"+jid.String(), nil, 0, ""), 404)
	mustStatus(t, other.request("POST", "analyses/"+jid.String()+"/retry", map[string]any{"tasks": []string{"ISSUE_DETECTION"}}, view.Version, ""), 404)
	mustStatus(t, owner.request("POST", "analyses/"+jid.String()+"/retry", map[string]any{"tasks": []string{"OCR"}}, view.Version, ""), 422)
	mustStatus(t, owner.request("POST", "analyses/"+jid.String()+"/retry", map[string]any{"tasks": []string{"ISSUE_DETECTION"}}, view.Version, ""), 202)
	worker.Vision.Binary = actual
	if err = worker.Once(context.Background()); err != nil {
		t.Fatal(err)
	}
	view = readAnalysis(t, owner, jid)
	if view.State != "SUCCEEDED" {
		t.Fatal(view)
	}
	for _, task := range view.Tasks {
		if task.Kind != "ISSUE_DETECTION" && !bytes.Equal(task.Result, preserved[task.Kind]) {
			t.Fatal("retry changed completed OCR/quality")
		}
		if task.Kind == "ISSUE_DETECTION" {
			var result media.ImageResult
			if err = json.Unmarshal(task.Result, &result); err != nil {
				t.Fatal(err)
			}
			if !result.Empty || len(result.Regions) != 0 || result.ModelVersion != media.DetectionModel {
				t.Fatal("empty was fabricated or model unpinned")
			}
		}
	}
}

func TestActualObjectsWithUnsupportedOCRAndPrivateSubmission(t *testing.T) {
	a, worker := detectionApp(t)
	owner := login(t, a, 0)
	raw, err := os.ReadFile("../../../../tests/fixtures/vision-people.png")
	if err != nil {
		t.Fatal(err)
	}
	submission := uuid.New()
	mid := uploadFixture(t, owner, submission, raw)
	if err = worker.Once(context.Background()); err != nil {
		t.Fatal(err)
	}
	jid := startAnalysis(t, owner, mid, []string{"OCR", "ISSUE_DETECTION"}, "hi-IN")
	if err = worker.Once(context.Background()); err != nil {
		t.Fatal(err)
	}
	view := readAnalysis(t, owner, jid)
	if view.State != "PARTIAL" {
		t.Fatal(view)
	}
	for _, task := range view.Tasks {
		if task.Kind == "OCR" && task.State != "UNSUPPORTED" {
			t.Fatal(task)
		}
		if task.Kind == "ISSUE_DETECTION" {
			var result media.ImageResult
			if task.State != "SUCCEEDED" {
				t.Fatal(task)
			}
			if err = json.Unmarshal(task.Result, &result); err != nil {
				t.Fatal(err)
			}
			if result.Empty || len(result.Regions) < 1 || result.Regions[0].Label != "person" || result.Regions[0].Confidence != nil {
				t.Fatal(result)
			}
		}
	}
	input := ReportInput{ClientSubmissionID: submission, Statement: "Fictional manual description selected by the resident", LanguageTag: "hi-IN", Category: "OTHER", LocationLabel: "Fictional image fixture", PublicationPreference: "PRIVATE", MediaIDs: []uuid.UUID{mid}}
	mustStatus(t, owner.request("POST", "service-reports", input, 0, submission.String()), 201)
	public := (client{app: a}).request("GET", "feed", nil, 0, "")
	if strings.Contains(public.Body.String(), jid.String()) || strings.Contains(public.Body.String(), mid.String()) {
		t.Fatal("private analysis exposed")
	}
}

func TestInFlightObjectRecognitionCancelledBeforePersistence(t *testing.T) {
	a, worker := detectionApp(t)
	owner := login(t, a, 0)
	raw, err := os.ReadFile("../../../../tests/fixtures/vision-people.png")
	if err != nil {
		t.Fatal(err)
	}
	mid := uploadFixture(t, owner, uuid.New(), raw)
	if err = worker.Once(context.Background()); err != nil {
		t.Fatal(err)
	}
	jid := startAnalysis(t, owner, mid, []string{"ISSUE_DETECTION"}, "en-IN")
	dir := t.TempDir()
	started, release, script := dir+"/started", dir+"/release", dir+"/delayed"
	// Fixed, trusted test wrapper delays actual inference outside the transaction.
	body := "#!/bin/sh\ntouch '" + started + "'\nwhile [ ! -f '" + release + "' ]; do sleep 0.01; done\nexec '" + worker.Vision.Binary + "' \"$@\"\n"
	if err = os.WriteFile(script, []byte(body), 0700); err != nil {
		t.Fatal(err)
	}
	worker.Vision.Binary = script
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	defer os.WriteFile(release, nil, 0600)
	finished := make(chan error, 1)
	go func() { finished <- worker.Once(ctx) }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err = os.Stat(started); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("inference did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	view := readAnalysis(t, owner, jid)
	mustStatus(t, owner.request("DELETE", "analyses/"+jid.String(), nil, view.Version, ""), 204)
	if err = os.WriteFile(release, nil, 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("inference did not finish")
	}
	view = readAnalysis(t, owner, jid)
	if view.State != "CANCELLED" {
		t.Fatal(view)
	}
	for _, task := range view.Tasks {
		if len(task.Result) > 0 && string(task.Result) != "null" {
			t.Fatal("late result persisted")
		}
	}
}

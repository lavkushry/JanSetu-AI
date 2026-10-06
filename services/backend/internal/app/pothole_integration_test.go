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

func potholeApp(t *testing.T) (*App, *media.Worker) {
	t.Helper()
	base := testApp(t)
	binary := os.Getenv("JANSETU_POTHOLE_BINARY")
	if binary == "" {
		t.Skip("set JANSETU_POTHOLE_BINARY; CI requires actual pothole model inference")
	}
	cfg := base.Config
	cfg.PotholeBinary = binary
	cfg.VisionBinary = os.Getenv("JANSETU_VISION_BINARY")
	a := cloneTestApp(t, cfg)
	worker := mediaWorker(t)
	worker.Pothole = &media.Detector{Binary: binary, Kind: "POTHOLE_DETECTION"}
	model, err := worker.Pothole.Version(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	worker.PotholeModel = model
	if cfg.VisionBinary != "" {
		worker.Vision = &media.Detector{Binary: cfg.VisionBinary}
		worker.VisionModel, err = worker.Vision.Version(context.Background())
		if err != nil {
			t.Fatal(err)
		}
	}
	return a, worker
}

func TestPotholeRetryPreservesOCRAndScope(t *testing.T) {
	a, worker := potholeApp(t)
	owner, other := login(t, a, 0), login(t, a, 1)
	caps := parsed[struct {
		AnalysisCapabilities []struct{ Kind, Status, Note string }
	}](t, owner.request("GET", "capabilities", nil, 0, ""))
	found := false
	for _, c := range caps.AnalysisCapabilities {
		if c.Kind == "POTHOLE_DETECTION" {
			found = c.Status == "EVALUATING" && strings.Contains(c.Note, "False positives")
		}
	}
	if !found {
		t.Fatal("must advertise experimental limits")
	}
	raw, err := os.ReadFile("../media/testdata/notice.png")
	if err != nil {
		t.Fatal(err)
	}
	mid := uploadFixture(t, owner, uuid.New(), raw)
	if err = worker.Once(context.Background()); err != nil {
		t.Fatal(err)
	}
	jid := startAnalysis(t, owner, mid, []string{"QUALITY", "OCR", "ISSUE_DETECTION", "POTHOLE_DETECTION"}, "en-IN")
	actual := worker.Pothole.Binary
	failure := filepath.Join(t.TempDir(), "unavailable")
	if err = os.WriteFile(failure, []byte("#!/bin/sh\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	worker.Pothole.Binary = failure
	if err = worker.Once(context.Background()); err != nil {
		t.Fatal(err)
	}
	view := readAnalysis(t, owner, jid)
	if view.State != "PARTIAL" {
		t.Fatal(view)
	}
	preserved := map[string]json.RawMessage{}
	for _, task := range view.Tasks {
		if task.Kind == "POTHOLE_DETECTION" {
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
	mustStatus(t, other.request("POST", "analyses/"+jid.String()+"/retry", map[string]any{"tasks": []string{"POTHOLE_DETECTION"}}, view.Version, ""), 404)
	mustStatus(t, owner.request("POST", "analyses/"+jid.String()+"/retry", map[string]any{"tasks": []string{"OCR"}}, view.Version, ""), 422)
	mustStatus(t, owner.request("POST", "analyses/"+jid.String()+"/retry", map[string]any{"tasks": []string{"POTHOLE_DETECTION"}}, view.Version, ""), 202)
	worker.Pothole.Binary = actual
	if err = worker.Once(context.Background()); err != nil {
		t.Fatal(err)
	}
	view = readAnalysis(t, owner, jid)
	if view.State != "SUCCEEDED" {
		t.Fatal(view)
	}
	for _, task := range view.Tasks {
		if task.Kind != "POTHOLE_DETECTION" && !bytes.Equal(task.Result, preserved[task.Kind]) {
			t.Fatal("retry changed completed OCR/quality")
		}
		if task.Kind == "POTHOLE_DETECTION" {
			var result media.ImageResult
			if err = json.Unmarshal(task.Result, &result); err != nil {
				t.Fatal(err)
			}
			if !result.Empty || len(result.Regions) != 0 || result.ModelVersion != worker.PotholeModel {
				t.Fatal("empty was fabricated or model unpinned")
			}
		}
	}
}

func TestActualPotholeWithUnsupportedOCRAndPrivateSubmission(t *testing.T) {
	a, worker := potholeApp(t)
	owner := login(t, a, 0)
	raw, err := os.ReadFile("../../../../tests/fixtures/pothole-positive.png")
	if err != nil {
		t.Fatal(err)
	}
	submission := uuid.New()
	mid := uploadFixture(t, owner, submission, raw)
	if err = worker.Once(context.Background()); err != nil {
		t.Fatal(err)
	}
	jid := startAnalysis(t, owner, mid, []string{"OCR", "POTHOLE_DETECTION"}, "hi-IN")
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
		if task.Kind == "POTHOLE_DETECTION" {
			var result media.ImageResult
			if task.State != "SUCCEEDED" {
				t.Fatal(task)
			}
			if err = json.Unmarshal(task.Result, &result); err != nil {
				t.Fatal(err)
			}
			if result.Empty || len(result.Regions) < 1 || result.Regions[0].Label != "pothole" || result.Regions[0].Confidence != nil {
				t.Fatal(result)
			}
		}
	}
	input := ReportInput{ClientSubmissionID: submission, Statement: "Fictional manual description selected by the resident", LanguageTag: "hi-IN", Category: "ROAD", LocationLabel: "Fictional image fixture", PublicationPreference: "PRIVATE", MediaIDs: []uuid.UUID{mid}, RoadDetails: &RoadDetails{RoadName: "Fictional reviewed road", IssueKind: "POTHOLE", RoadType: "UNKNOWN"}}
	mustStatus(t, owner.request("POST", "service-reports", input, 0, submission.String()), 201)
	public := (client{app: a}).request("GET", "feed", nil, 0, "")
	if strings.Contains(public.Body.String(), jid.String()) || strings.Contains(public.Body.String(), mid.String()) {
		t.Fatal("private analysis exposed")
	}
}

func TestInFlightPotholeCancelledBeforePersistence(t *testing.T) {
	a, worker := potholeApp(t)
	owner := login(t, a, 0)
	raw, err := os.ReadFile("../../../../tests/fixtures/pothole-positive.png")
	if err != nil {
		t.Fatal(err)
	}
	mid := uploadFixture(t, owner, uuid.New(), raw)
	if err = worker.Once(context.Background()); err != nil {
		t.Fatal(err)
	}
	jid := startAnalysis(t, owner, mid, []string{"POTHOLE_DETECTION"}, "en-IN")
	dir := t.TempDir()
	started, release, script := dir+"/started", dir+"/release", dir+"/delayed"
	// Fixed, trusted test wrapper delays actual inference outside the transaction.
	body := "#!/bin/sh\ntouch '" + started + "'\nwhile [ ! -f '" + release + "' ]; do sleep 0.01; done\nexec '" + worker.Pothole.Binary + "' \"$@\"\n"
	if err = os.WriteFile(script, []byte(body), 0700); err != nil {
		t.Fatal(err)
	}
	worker.Pothole.Binary = script
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
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

func TestPotholeDisabledDoesNotBlockManualPhoto(t *testing.T) {
	base := testApp(t)
	cfg := base.Config
	cfg.PotholeBinary = ""
	a := cloneTestApp(t, cfg)
	owner := login(t, a, 0)
	worker := mediaWorker(t)
	raw, err := os.ReadFile("../media/testdata/notice.png")
	if err != nil {
		t.Fatal(err)
	}
	mid := uploadFixture(t, owner, uuid.New(), raw)
	if err = worker.Once(context.Background()); err != nil {
		t.Fatal(err)
	}
	jid := startAnalysis(t, owner, mid, []string{"QUALITY", "POTHOLE_DETECTION"}, "en-IN")
	if err = worker.Once(context.Background()); err != nil {
		t.Fatal(err)
	}
	view := readAnalysis(t, owner, jid)
	if view.State != "PARTIAL" {
		t.Fatal(view)
	}
	for _, task := range view.Tasks {
		if task.Kind == "POTHOLE_DETECTION" && (task.State != "UNSUPPORTED" || task.Retryable || string(task.Result) != "null") {
			t.Fatal(task)
		}
		if task.Kind == "QUALITY" && task.State != "SUCCEEDED" {
			t.Fatal(task)
		}
	}
}

package media

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDetectionRejectsUntrustedResults(t *testing.T) {
	valid := `{"width":640,"height":480,"empty":false,"regions":[{"label":"person","polygon":[[1,2],[100,2],[100,200],[1,200]]}]}`
	result, code := parseDetection([]byte(valid), DetectionModel, []byte{1}, 640, 480)
	if code != "" || len(result.Regions) != 1 || result.Regions[0].Confidence != nil || result.Regions[0].Text != "" || result.SourceSHA256 != "01" {
		t.Fatalf("valid: %+v %s", result, code)
	}
	cases := []string{
		strings.Replace(valid, `"width":640`, `"width":641`, 1),
		strings.Replace(valid, `"person"`, `"pothole"`, 1),
		strings.Replace(valid, `[1,2]`, `[-1,2]`, 1),
		strings.Replace(valid, `[100,200]`, `[100,9999]`, 1),
		strings.Replace(valid, `[100,2]`, `[99,3]`, 1),
		strings.Replace(valid, `[1,2]`, `[1,2,3]`, 1),
		strings.Replace(valid, `"empty":false`, `"empty":true`, 1),
		strings.Replace(valid, `"empty":false,`, ``, 1),
		strings.Replace(valid, `"label":"person"`, `"label":"person","confidence":0.99`, 1),
		valid + `{}`,
		`{"width":640,"height":480,"empty":true,"regions":null}`,
	}
	for _, input := range cases {
		if _, code := parseDetection([]byte(input), DetectionModel, nil, 640, 480); code != "VISION_INVALID_RESULT" {
			t.Fatalf("accepted %s", input)
		}
	}
	var overLimit []map[string]any
	for i := 0; i < 101; i++ {
		overLimit = append(overLimit, map[string]any{"label": "person", "polygon": [][2]int{{1, 2}, {100, 2}, {100, 200}, {1, 200}}})
	}
	tooMany, _ := json.Marshal(map[string]any{"width": 640, "height": 480, "empty": false, "regions": overLimit})
	if _, code := parseDetection(tooMany, DetectionModel, nil, 640, 480); code != "VISION_INVALID_RESULT" {
		t.Fatal("region limit bypassed")
	}
	if result, code := parseDetection([]byte(`{"width":640,"height":480,"empty":true,"regions":[]}`), DetectionModel, nil, 640, 480); code != "" || !result.Empty || len(result.Regions) != 0 {
		t.Fatal("successful empty missing")
	}
}

func TestDetectorEnvironmentAndOutputBounds(t *testing.T) {
	t.Setenv("JANSETU_SECRET_SENTINEL", "private-test-value")
	binary := filepath.Join(t.TempDir(), "detector")
	body := "#!/bin/sh\nif [ -n \"${JANSETU_SECRET_SENTINEL+x}\" ]; then exit 1; fi\nprintf '%s\\n' '" + DetectionModel + "'\n"
	if err := os.WriteFile(binary, []byte(body), 0700); err != nil {
		t.Fatal(err)
	}
	d := Detector{Binary: binary}
	if _, err := d.Version(context.Background()); err != nil {
		t.Fatal("inherited private environment:", err)
	}
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nhead -c 40000 /dev/zero\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, code := d.Run(context.Background(), "unused.png", DetectionModel, nil, 640, 480); code != "VISION_INVALID_RESULT" {
		t.Fatal("output overflow is retryable:", code)
	}
	if _, err := d.Version(context.Background()); err == nil {
		t.Fatal("wrong engine accepted")
	}
}

func TestPinnedDetectorActualInference(t *testing.T) {
	binary := os.Getenv("JANSETU_VISION_BINARY")
	if binary == "" {
		t.Skip("set JANSETU_VISION_BINARY after make vision-setup; CI requires this")
	}
	d := Detector{Binary: binary}
	model, err := d.Version(context.Background())
	if err != nil || model != DetectionModel {
		t.Fatalf("readiness: %s %v", model, err)
	}
	path, err := filepath.Abs("../../../../tests/fixtures/vision-people.png")
	if err != nil {
		t.Fatal(err)
	}
	result, code := d.Run(context.Background(), path, model, []byte{1, 2}, 640, 480)
	if code != "" || result.Empty || len(result.Regions) < 1 || result.Regions[0].Label != "person" || result.Regions[0].Confidence != nil {
		t.Fatalf("actual: %+v %s", result, code)
	}
	encoded, _ := json.Marshal(result)
	if strings.Contains(string(encoded), `"text"`) || strings.Contains(string(encoded), `"languageTag"`) {
		t.Fatal("object result contains OCR fields")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, code := d.Run(ctx, path, model, nil, 640, 480); code != "VISION_TIMEOUT" {
		t.Fatal(code)
	}
}

func TestPotholeResultVocabularyAndVersion(t *testing.T) {
	valid := `{"width":640,"height":480,"empty":false,"regions":[{"label":"pothole","polygon":[[1,2],[100,2],[100,200],[1,200]]}]}`
	result, code := parseCandidates([]byte(valid), PotholeModel, []byte{1}, 640, 480, "POTHOLE_DETECTION")
	if code != "" || len(result.Regions) != 1 || result.Regions[0].Confidence != nil || result.Codes[0] != "POTHOLE_CANDIDATES_ONLY" {
		t.Fatalf("%+v %s", result, code)
	}
	for _, bad := range []string{strings.Replace(valid, `"pothole"`, `"person"`, 1), strings.Replace(valid, `[100,200]`, `[9999,200]`, 1), strings.Replace(valid, `"label":"pothole"`, `"label":"pothole","severity":"URGENT"`, 1)} {
		if _, code := parseCandidates([]byte(bad), PotholeModel, nil, 640, 480, "POTHOLE_DETECTION"); code != "VISION_INVALID_RESULT" {
			t.Fatal("accepted", bad)
		}
	}
	version := PotholeModel + "/onnx-sha256:" + strings.Repeat("a", 64)
	if !potholeVersion.MatchString(version) || potholeVersion.MatchString(PotholeModel) || potholeVersion.MatchString(version+"/caller") {
		t.Fatal("unpinned version accepted")
	}
}

func TestPinnedPotholeActualInference(t *testing.T) {
	binary := os.Getenv("JANSETU_POTHOLE_BINARY")
	if binary == "" {
		t.Skip("set JANSETU_POTHOLE_BINARY after make pothole-setup; CI requires this")
	}
	detector := Detector{Binary: binary, Kind: "POTHOLE_DETECTION"}
	version, err := detector.Version(context.Background())
	if err != nil || !potholeVersion.MatchString(version) {
		t.Fatalf("readiness %s %v", version, err)
	}
	path, err := filepath.Abs("../../../../tests/fixtures/pothole-positive.png")
	if err != nil {
		t.Fatal(err)
	}
	result, code := detector.Run(context.Background(), path, version, []byte{1, 2}, 583, 1200)
	if code != "" || result.Empty || len(result.Regions) == 0 || result.Regions[0].Label != "pothole" || result.Regions[0].Confidence != nil {
		t.Fatalf("actual %+v %s", result, code)
	}
	for _, r := range result.Regions {
		if r.Text != "" {
			t.Fatal("pothole contains OCR text")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, code := detector.Run(ctx, path, version, nil, 583, 1200); code != "VISION_TIMEOUT" {
		t.Fatal(code)
	}
}

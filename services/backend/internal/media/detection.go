package media

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"
)

const DetectionModel = "yolox-nano-0.1.1rc0/sha256:c789161ed43c8269fcd4e67c67eeeb4e80c622da2eb296a20bc6007bd18a0b7d/ort-1.23.2/opencv-4.12.0/road-objects-v1"

// Detector invokes one fixed local executable against a decoded private image.
// It receives no credentials, caller-selected options, or external image URLs.
type Detector struct{ Binary string }

func (d Detector) command(ctx context.Context, argument string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, d.Binary, argument)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "OMP_NUM_THREADS=1", "OPENBLAS_NUM_THREADS=1", "MKL_NUM_THREADS=1", "ORT_DISABLE_TELEMETRY=1"}
	cmd.Stderr = io.Discard
	return cmd
}

func (d Detector) Version(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd := d.command(ctx, "--version")
	output := &limitedBuffer{limit: 512}
	cmd.Stdout = output
	if err := cmd.Run(); err != nil {
		return "", err
	}
	if strings.TrimSpace(output.String()) != DetectionModel {
		return "", errors.New("unexpected image recognition model")
	}
	return DetectionModel, nil
}

func (d Detector) Run(ctx context.Context, path, model string, hash []byte, width, height int) (ImageResult, string) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd := d.command(ctx, path)
	output := &limitedBuffer{limit: 32 * 1024}
	cmd.Stdout = output
	if err := cmd.Run(); err != nil {
		if output.exceeded {
			return ImageResult{}, "VISION_INVALID_RESULT"
		}
		if ctx.Err() != nil {
			return ImageResult{}, "VISION_TIMEOUT"
		}
		return ImageResult{}, "VISION_FAILED"
	}
	return parseDetection(output.Bytes(), model, hash, width, height)
}

func parseDetection(data []byte, model string, hash []byte, width, height int) (ImageResult, string) {
	var wire struct {
		Width   int   `json:"width"`
		Height  int   `json:"height"`
		Empty   *bool `json:"empty"`
		Regions []struct {
			Label   string  `json:"label"`
			Polygon [][]int `json:"polygon"`
		} `json:"regions"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil || wire.Width != width || wire.Height != height || wire.Empty == nil || wire.Regions == nil || len(wire.Regions) > 100 || *wire.Empty != (len(wire.Regions) == 0) {
		return ImageResult{}, "VISION_INVALID_RESULT"
	}
	if decoder.Decode(new(any)) != io.EOF {
		return ImageResult{}, "VISION_INVALID_RESULT"
	}
	allowed := map[string]bool{"person": true, "bicycle": true, "car": true, "motorcycle": true, "bus": true, "truck": true, "traffic light": true, "fire hydrant": true, "stop sign": true, "bench": true}
	result := Result(model, hash, width, height)
	result.Empty = *wire.Empty
	result.Codes = []string{"OBJECT_CANDIDATES_ONLY", "UNSUPPORTED_HAZARD_TAXONOMY"}
	for i, region := range wire.Regions {
		if !allowed[region.Label] || len(region.Polygon) != 4 {
			return ImageResult{}, "VISION_INVALID_RESULT"
		}
		p := make([][2]int, 4)
		for j, point := range region.Polygon {
			if len(point) != 2 {
				return ImageResult{}, "VISION_INVALID_RESULT"
			}
			p[j] = [2]int{point[0], point[1]}
		}
		// Only nonempty axis-aligned boxes with the adapter's fixed corner order.
		if p[0][0] < 0 || p[0][1] < 0 || p[2][0] > width || p[2][1] > height || p[0][0] >= p[2][0] || p[0][1] >= p[2][1] || p[1] != [2]int{p[2][0], p[0][1]} || p[3] != [2]int{p[0][0], p[2][1]} {
			return ImageResult{}, "VISION_INVALID_RESULT"
		}
		// Engine scores only select candidates. Probability calibration is pending.
		result.Regions = append(result.Regions, Region{ID: fmt.Sprintf("d%d", i+1), Label: region.Label, Polygon: p, Confidence: nil})
	}
	return result, ""
}

package media

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Region struct {
	ID          string   `json:"id"`
	Text        string   `json:"text,omitempty"`
	LanguageTag string   `json:"languageTag,omitempty"`
	Label       string   `json:"label,omitempty"`
	Polygon     [][2]int `json:"polygon"`
	Confidence  *float64 `json:"confidence"`
}
type ImageResult struct {
	SchemaVersion   int    `json:"schemaVersion"`
	ModelVersion    string `json:"modelVersion"`
	SourceSHA256    string `json:"sourceSha256"`
	CoordinateSpace string `json:"coordinateSpace"`
	OriginalSize    struct {
		Width  int `json:"width"`
		Height int `json:"height"`
	} `json:"originalSize"`
	Regions []Region `json:"regions"`
	Codes   []string `json:"codes,omitempty"`
	Empty   bool     `json:"empty"`
}

func Result(model string, hash []byte, w, h int) ImageResult {
	r := ImageResult{SchemaVersion: 1, ModelVersion: model, SourceSHA256: ETag(hash), CoordinateSpace: "ORIGINAL_PIXELS", Regions: []Region{}}
	r.OriginalSize.Width = w
	r.OriginalSize.Height = h
	return r
}

// Tesseract runs only against the internally chosen, decoded PNG. No shell,
// requester-supplied config/path/URL, network tool, or generated instruction execution.
type OCR struct{ Binary string }

func (o OCR) Version(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, o.Binary, "--version")
	b := &limitedBuffer{limit: 4096}
	cmd.Stdout = b
	cmd.Stderr = io.Discard
	if e := cmd.Run(); e != nil {
		return "", e
	}
	line, _, _ := strings.Cut(b.String(), "\n")
	if !strings.HasPrefix(line, "tesseract ") {
		return "", errors.New("unexpected OCR engine")
	}
	packs := &limitedBuffer{limit: 8192}
	list := exec.CommandContext(ctx, o.Binary, "--list-langs")
	list.Stdout = packs
	list.Stderr = io.Discard
	if e := list.Run(); e != nil {
		return "", e
	}
	if !strings.Contains("\n"+packs.String()+"\n", "\neng\n") {
		return "", errors.New("English trained data unavailable")
	}
	_, rest, ok := strings.Cut(packs.String(), "\"")
	if !ok {
		return "", errors.New("OCR asset path unavailable")
	}
	dir, _, ok := strings.Cut(rest, "\"")
	if !ok {
		return "", errors.New("OCR asset path unavailable")
	}
	data, e := os.ReadFile(filepath.Join(dir, "eng.traineddata"))
	if e != nil {
		return "", e
	}
	hash := sha256.Sum256(data)
	return strings.TrimSpace(line) + fmt.Sprintf("/eng-sha256:%x", hash), nil
}

type limitedBuffer struct {
	bytes.Buffer
	limit    int
	exceeded bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if len(p) > b.limit-b.Len() {
		b.exceeded = true
		return 0, errors.New("OCR output limit")
	}
	return b.Buffer.Write(p)
}
func (o OCR) Run(ctx context.Context, path, language, model string, hash []byte, w, h int) (ImageResult, string) {
	if language != "en" && language != "en-IN" && language != "en-US" && language != "en-GB" {
		return ImageResult{}, "LANGUAGE_UNAVAILABLE"
	}
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, o.Binary, path, "stdout", "-l", "eng", "--psm", "11", "tsv")
	cmd.Env = []string{"PATH=/usr/bin:/bin", "OMP_THREAD_LIMIT=1"}
	out := &limitedBuffer{limit: 1 << 20}
	cmd.Stdout = out
	cmd.Stderr = io.Discard
	if e := cmd.Run(); e != nil {
		if ctx.Err() != nil {
			return ImageResult{}, "OCR_TIMEOUT"
		}
		return ImageResult{}, "OCR_FAILED"
	}
	r, e := ParseTSV(out.Bytes(), model, language, hash, w, h)
	if e != nil {
		return ImageResult{}, "OCR_INVALID_RESULT"
	}
	return r, ""
}
func ParseTSV(b []byte, model, language string, hash []byte, w, h int) (ImageResult, error) {
	r := Result(model, hash, w, h)
	scanner := bufio.NewScanner(bytes.NewReader(b))
	scanner.Buffer(make([]byte, 4096), 1<<20)
	if !scanner.Scan() || scanner.Text() != "level\tpage_num\tblock_num\tpar_num\tline_num\tword_num\tleft\ttop\twidth\theight\tconf\ttext" {
		return r, errors.New("invalid TSV header")
	}
	for scanner.Scan() {
		row := strings.SplitN(scanner.Text(), "\t", 12)
		if len(row) != 12 {
			return r, errors.New("invalid TSV row")
		}
		if row[0] != "5" || strings.TrimSpace(row[11]) == "" {
			continue
		}
		if len(r.Regions) >= 500 || len([]rune(row[11])) > 500 {
			return r, errors.New("OCR region limit")
		}
		var e error
		coords := [4]int{}
		for i := range coords {
			coords[i], e = strconv.Atoi(row[6+i])
			if e != nil {
				return r, e
			}
		}
		x, y, rw, rh := coords[0], coords[1], coords[2], coords[3]
		if x < 0 || y < 0 || rw <= 0 || rh <= 0 || x > w || y > h || rw > w-x || rh > h-y {
			return r, errors.New("invalid OCR coordinates")
		}
		// Raw engine confidence is uncalibrated; never present it as a probability.
		r.Regions = append(r.Regions, Region{ID: "word-" + strconv.Itoa(len(r.Regions)+1), Text: row[11], LanguageTag: language, Polygon: [][2]int{{x, y}, {x + rw, y}, {x + rw, y + rh}, {x, y + rh}}})
	}
	if e := scanner.Err(); e != nil {
		return r, e
	}
	r.Empty = len(r.Regions) == 0
	encoded, e := json.Marshal(r)
	if e != nil || len(encoded) > 256<<10 {
		return r, errors.New("result limit")
	}
	return r, nil
}

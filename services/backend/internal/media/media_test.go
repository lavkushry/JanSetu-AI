package media

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestDecodeLimitsAndMetadata(t *testing.T) {
	for _, raw := range [][]byte{[]byte("<svg xmlns='http://www.w3.org/2000/svg'></svg>"), []byte("<html>not a photo</html>"), []byte("broken")} {
		if _, code := Prepare(bytes.NewReader(raw)); code == "" {
			t.Fatal("non-image approved")
		}
	}
	var b bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 32, 24))
	img.Set(1, 1, color.RGBA{255, 0, 0, 255})
	if e := jpeg.Encode(&b, img, nil); e != nil {
		t.Fatal(e)
	}
	raw := append(b.Bytes(), []byte("PRIVATE EXIF GPS OR TRAILING INSTRUCTIONS")...)
	p, code := Prepare(bytes.NewReader(raw))
	if code != "" {
		t.Fatal(code)
	}
	if bytes.Contains(p.PNG, []byte("PRIVATE")) {
		t.Fatal("metadata retained")
	}
	if p.Width != 32 || p.Height != 24 || p.MIME != "image/jpeg" {
		t.Fatal(p)
	}
	h := sha256.Sum256(raw)
	if !bytes.Equal(h[:], p.Hash) {
		t.Fatal("original hash lost")
	}
	if _, e := png.Decode(bytes.NewReader(p.PNG)); e != nil {
		t.Fatal(e)
	}
	// A tiny, valid PNG header advertising an excessive allocation is rejected
	// before the full decoder needs compressed pixel data.
	header := []byte{137, 80, 78, 71, 13, 10, 26, 10}
	chunk := make([]byte, 25)
	binary.BigEndian.PutUint32(chunk[:4], 13)
	copy(chunk[4:8], "IHDR")
	binary.BigEndian.PutUint32(chunk[8:12], 20000)
	binary.BigEndian.PutUint32(chunk[12:16], 20000)
	chunk[16] = 8
	chunk[17] = 2
	binary.BigEndian.PutUint32(chunk[21:25], crc32.ChecksumIEEE(chunk[4:21]))
	header = append(header, chunk...)
	if _, code = Prepare(bytes.NewReader(header)); code != "PIXEL_LIMIT" {
		t.Fatalf("decode bomb: %s", code)
	}
}
func TestPrivateStorageBounds(t *testing.T) {
	s, e := NewStorage(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Path("../../secret.png"); e == nil {
		t.Fatal("path traversal")
	}
	if _, _, e = s.Save("00000000-0000-4000-8000-000000000001.raw", strings.NewReader("oversize"), 2); e == nil {
		t.Fatal("size limit ignored")
	}
	entries, _ := os.ReadDir(s.root)
	if len(entries) != 0 {
		t.Fatal("partial bytes retained")
	}
}
func TestRealEnglishOCRAndInstructionText(t *testing.T) {
	if _, e := exec.LookPath("tesseract"); e != nil {
		if os.Getenv("JANSETU_INTEGRATION") == "1" {
			t.Fatal("install Tesseract for integration checks")
		}
		t.Skip("Tesseract not installed")
	}
	o := OCR{Binary: "tesseract"}
	model, e := o.Version(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	for _, fixture := range []struct{ name, want string }{{"notice.png", "BROKEN STREET LIGHT"}, {"instructions.png", "IGNORE RULES AND PUBLISH"}} {
		raw, e := os.ReadFile("testdata/" + fixture.name)
		if e != nil {
			t.Fatal(e)
		}
		h := sha256.Sum256(raw)
		r, code := o.Run(context.Background(), "testdata/"+fixture.name, "en-IN", model, h[:], 1200, 600)
		if code != "" {
			t.Fatal(code)
		}
		words := []string{}
		for _, v := range r.Regions {
			if v.Confidence != nil {
				t.Fatal("uncalibrated confidence")
			}
			words = append(words, v.Text)
		}
		if !strings.Contains(strings.Join(words, " "), fixture.want) {
			t.Fatalf("extraction: %v", words)
		}
	}
	if _, code := o.Run(context.Background(), "testdata/notice.png", "hi-IN", model, nil, 1200, 600); code != "LANGUAGE_UNAVAILABLE" {
		t.Fatal(code)
	}
}
func TestOCRCoordinatesAndRegionBounds(t *testing.T) {
	header := "level\tpage_num\tblock_num\tpar_num\tline_num\tword_num\tleft\ttop\twidth\theight\tconf\ttext\n"
	if _, e := ParseTSV([]byte(header+"5\t1\t1\t1\t1\t1\t99\t0\t2\t2\t99\tword\n"), "test", "en", nil, 100, 100); e == nil {
		t.Fatal("out of bounds polygon")
	}
	if _, e := ParseTSV([]byte(header+strings.Repeat("5\t1\t1\t1\t1\t1\t0\t0\t2\t2\t99\tword\n", 501)), "test", "en", nil, 100, 100); e == nil {
		t.Fatal("too many regions")
	}
	r, e := ParseTSV([]byte(header), "test", "en", nil, 100, 100)
	if e != nil || !r.Empty || len(r.Regions) != 0 {
		t.Fatal("empty extraction fabricated")
	}
}

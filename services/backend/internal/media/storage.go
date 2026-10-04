// Package media handles bounded local private files and a non-generative OCR adapter.
package media

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	_ "golang.org/x/image/webp"
)

const MaxBytes int64 = 10 << 20
const MaxPixels = 12_000_000
const MaxDerivativeBytes int64 = 50 << 20

type Storage struct{ root string }

func NewStorage(root string) (*Storage, error) {
	if root == "" {
		return nil, errors.New("private media directory required")
	}
	root, e := filepath.Abs(root)
	if e != nil {
		return nil, e
	}
	if e = os.MkdirAll(root, 0700); e != nil {
		return nil, e
	}
	return &Storage{root: root}, nil
}
func (s *Storage) Path(key string) (string, error) {
	stem, ext, ok := strings.Cut(key, ".")
	if _, e := uuid.Parse(stem); !ok || e != nil || (ext != "raw" && ext != "png") {
		return "", errors.New("invalid internal media key")
	}
	return filepath.Join(s.root, key), nil
}
func (s *Storage) Open(key string) (*os.File, error) {
	p, e := s.Path(key)
	if e != nil {
		return nil, e
	}
	return os.Open(p)
}
func (s *Storage) Remove(key string) error {
	p, e := s.Path(key)
	if e != nil {
		return e
	}
	e = os.Remove(p)
	if os.IsNotExist(e) {
		return nil
	}
	return e
}

// Save atomically replaces a fixed internal key only after the bounded write succeeds.
func (s *Storage) Save(key string, r io.Reader, limit int64) ([]byte, int64, error) {
	p, e := s.Path(key)
	if e != nil {
		return nil, 0, e
	}
	f, e := os.CreateTemp(s.root, "pending-")
	if e != nil {
		return nil, 0, e
	}
	defer os.Remove(f.Name())
	defer f.Close()
	h := sha256.New()
	n, e := io.Copy(io.MultiWriter(f, h), io.LimitReader(r, limit+1))
	if e != nil {
		return nil, n, e
	}
	if n == 0 || n > limit {
		return nil, n, errors.New("file size limit")
	}
	if e = f.Sync(); e != nil {
		return nil, n, e
	}
	if e = f.Close(); e != nil {
		return nil, n, e
	}
	if e = os.Rename(f.Name(), p); e != nil {
		return nil, n, e
	}
	return h.Sum(nil), n, nil
}

type Prepared struct {
	PNG           []byte
	Hash          []byte
	Width, Height int
	MIME          string
}

// DecodeConfig precedes Decode so tiny files cannot allocate an unbounded image.
// Re-encoding preserves decoded pixel coordinates and drops EXIF/text/other metadata.
func Prepare(r io.Reader) (Prepared, string) {
	raw, e := io.ReadAll(io.LimitReader(r, MaxBytes+1))
	if e != nil || len(raw) == 0 || int64(len(raw)) > MaxBytes {
		return Prepared{}, "INVALID_SIZE"
	}
	cfg, format, e := image.DecodeConfig(bytes.NewReader(raw))
	if e != nil {
		return Prepared{}, "INVALID_IMAGE"
	}
	if format != "jpeg" && format != "png" && format != "webp" {
		return Prepared{}, "UNSUPPORTED_FORMAT"
	}
	if cfg.Width < 1 || cfg.Height < 1 || cfg.Width > 8192 || cfg.Height > 8192 || int64(cfg.Width)*int64(cfg.Height) > MaxPixels {
		return Prepared{}, "PIXEL_LIMIT"
	}
	decoded, decodedFormat, e := image.Decode(bytes.NewReader(raw))
	if e != nil || decodedFormat != format {
		return Prepared{}, "INVALID_IMAGE"
	}
	if decoded.Bounds().Dx() != cfg.Width || decoded.Bounds().Dy() != cfg.Height {
		return Prepared{}, "INVALID_IMAGE"
	}
	var out bytes.Buffer
	if e = png.Encode(&out, decoded); e != nil || int64(out.Len()) > MaxDerivativeBytes {
		return Prepared{}, "INVALID_IMAGE"
	}
	h := sha256.Sum256(raw)
	mime := "image/" + format
	if format == "jpeg" {
		mime = "image/jpeg"
	}
	return Prepared{out.Bytes(), h[:], cfg.Width, cfg.Height, mime}, ""
}
func OriginalKey(id uuid.UUID) string { return id.String() + ".raw" }
func DerivativeKey() string           { return uuid.NewString() + ".png" }
func ETag(hash []byte) string         { return fmt.Sprintf("%x", hash) }

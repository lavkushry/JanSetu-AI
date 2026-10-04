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
const PartBytes int64 = 2 << 20
const MaxParts = 5
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
	validPart := len(ext) == 2 && ext[0] == 'p' && ext[1] >= '1' && ext[1] <= '5'
	if _, e := uuid.Parse(stem); !ok || e != nil || (ext != "raw" && ext != "png" && !validPart) {
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
	return s.save(key, r, limit, nil)
}
func (s *Storage) save(key string, r io.Reader, limit int64, expected []byte) ([]byte, int64, error) {
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
	if expected != nil && !bytes.Equal(h.Sum(nil), expected) {
		return nil, n, ErrIntegrity
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

var ErrIntegrity = errors.New("upload integrity mismatch")

type StoredPart struct {
	Key, ETag string
	Size      int64
}

// Assemble validates every immutable part and the full fingerprint before rename.
func (s *Storage) Assemble(key string, parts []StoredPart, expected []byte) ([]byte, error) {
	if len(parts) < 1 || len(parts) > MaxParts || len(expected) != 32 {
		return nil, ErrIntegrity
	}
	readers := make([]io.Reader, 0, len(parts))
	var total int64
	for _, part := range parts {
		if part.Size < 1 || part.Size > PartBytes || total+part.Size > MaxBytes {
			return nil, ErrIntegrity
		}
		file, err := s.Open(part.Key)
		if err != nil {
			if os.IsNotExist(err) {
				return nil, ErrIntegrity
			}
			return nil, err
		}
		raw, err := io.ReadAll(io.LimitReader(file, part.Size+1))
		file.Close()
		digest := sha256.Sum256(raw)
		if err != nil || int64(len(raw)) != part.Size || ETag(digest[:]) != part.ETag {
			return nil, ErrIntegrity
		}
		total += part.Size
		readers = append(readers, bytes.NewReader(raw))
	}
	hash, _, err := s.save(key, io.MultiReader(readers...), total, expected)
	return hash, err
}
func PartKey(uploadID uuid.UUID, number int) string { return fmt.Sprintf("%s.p%d", uploadID, number) }
func (s *Storage) RemoveParts(uploadID uuid.UUID) error {
	for number := 1; number <= MaxParts; number++ {
		if err := s.Remove(PartKey(uploadID, number)); err != nil {
			return err
		}
	}
	return nil
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

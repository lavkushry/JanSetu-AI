package media

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"io"
	"testing"

	"github.com/google/uuid"
)

func TestMultipartAssemblyVerifiesBeforeAtomicReplacement(t *testing.T) {
	s, err := NewStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	u, mid := uuid.New(), uuid.New()
	parts := []StoredPart{}
	whole := []byte{}
	for i, raw := range [][]byte{[]byte("first part"), []byte("second part")} {
		h, n, e := s.Save(PartKey(u, i+1), bytes.NewReader(raw), PartBytes)
		if e != nil {
			t.Fatal(e)
		}
		parts = append(parts, StoredPart{Key: PartKey(u, i+1), ETag: ETag(h), Size: n})
		whole = append(whole, raw...)
	}
	key := OriginalKey(mid)
	if _, _, err = s.Save(key, bytes.NewReader([]byte("prior valid bytes")), MaxBytes); err != nil {
		t.Fatal(err)
	}
	want := sha256.Sum256(whole)
	wrong := sha256.Sum256([]byte("different file"))
	if _, err = s.Assemble(key, parts, wrong[:]); !errors.Is(err, ErrIntegrity) {
		t.Fatal("whole hash not enforced", err)
	}
	f, _ := s.Open(key)
	previous, _ := io.ReadAll(f)
	f.Close()
	if string(previous) != "prior valid bytes" {
		t.Fatal("failed assembly replaced original")
	}
	if _, _, err = s.Save(parts[0].Key, bytes.NewReader([]byte("corrupted!")), PartBytes); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Assemble(key, parts, want[:]); !errors.Is(err, ErrIntegrity) {
		t.Fatal("part hash not enforced", err)
	}
	if err = s.Remove(parts[0].Key); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Assemble(key, parts, want[:]); !errors.Is(err, ErrIntegrity) {
		t.Fatal("missing part accepted", err)
	}
	if _, _, err = s.Save(parts[0].Key, bytes.NewReader([]byte("first part")), PartBytes); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Assemble(key, parts, want[:]); err != nil {
		t.Fatal(err)
	}
	f, _ = s.Open(key)
	assembled, _ := io.ReadAll(f)
	f.Close()
	if !bytes.Equal(assembled, whole) {
		t.Fatal("parts not assembled in order")
	}
	for _, invalid := range [][]StoredPart{nil, make([]StoredPart, 6), {{Key: parts[0].Key, Size: PartBytes + 1, ETag: parts[0].ETag}}} {
		if _, err = s.Assemble(key, invalid, want[:]); err == nil {
			t.Fatal("assembly bounds ignored")
		}
	}
	for _, suffix := range []string{"p0", "p6", "p11", "p1/../../secret"} {
		if _, err = s.Path(u.String() + "." + suffix); err == nil {
			t.Fatal("unsafe part path", suffix)
		}
	}
	for i := 0; i < 2; i++ {
		if err = s.RemoveParts(u); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range parts {
		if f, e := s.Open(p.Key); e == nil {
			f.Close()
			t.Fatal("cleanup retained part")
		}
	}
}

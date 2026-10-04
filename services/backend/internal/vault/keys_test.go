package vault

import (
	"bytes"
	"encoding/json"
	"github.com/google/uuid"
	"os"
	"path/filepath"
	"testing"
)

func testKeys() Keys {
	return Keys{Version: "test-v1", EncryptionKey: bytes.Repeat([]byte{1}, 32), LookupKey: bytes.Repeat([]byte{2}, 32), SigningKey: bytes.Repeat([]byte{3}, 32)}
}
func TestLocatorEncryptionBindsSubjectAndVersion(t *testing.T) {
	k := testKeys()
	subject, principal := uuid.New(), uuid.New()
	first, err := k.seal(subject, principal)
	if err != nil {
		t.Fatal(err)
	}
	second, err := k.seal(subject, principal)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(first, second) || bytes.Contains(first, principal[:]) {
		t.Fatal("encryption reused a nonce or retained plaintext")
	}
	clear, err := k.open(subject, first)
	if err != nil || clear != principal {
		t.Fatal("round trip failed", err)
	}
	if _, err = k.open(uuid.New(), first); err == nil {
		t.Fatal("ciphertext moved to another subject")
	}
	changed := k
	changed.Version = "test-v2"
	if _, err = changed.open(subject, first); err == nil {
		t.Fatal("version context was ignored")
	}
	corrupt := append([]byte(nil), first...)
	corrupt[len(corrupt)-1] ^= 1
	if _, err = k.open(subject, corrupt); err == nil {
		t.Fatal("tampered ciphertext accepted")
	}
	if _, err = k.open(subject, []byte{1}); err == nil {
		t.Fatal("truncated ciphertext accepted")
	}
	if bytes.Equal(k.lookup(principal), k.lookup(uuid.New())) {
		t.Fatal("lookup collisions")
	}
	if !bytes.Equal(k.lookup(principal), k.lookup(principal)) {
		t.Fatal("lookup is unstable")
	}
}
func TestKeyFileRejectsMissingAndReusedKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keys.json")
	k := testKeys()
	b, _ := json.Marshal(k)
	if err := os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadKeys(path); err != nil {
		t.Fatal(err)
	}
	k.LookupKey = k.EncryptionKey
	b, _ = json.Marshal(k)
	_ = os.WriteFile(path, b, 0600)
	if _, err := LoadKeys(path); err == nil {
		t.Fatal("reused encryption and lookup keys accepted")
	}
	if _, err := LoadKeys(path + "missing"); err == nil {
		t.Fatal("missing keys accepted")
	}
}

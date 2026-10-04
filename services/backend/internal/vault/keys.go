package vault

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"os"
)

type Keys struct {
	Version       string `json:"version"`
	EncryptionKey []byte `json:"encryptionKey"`
	LookupKey     []byte `json:"lookupKey"`
	SigningKey    []byte `json:"signingKey"`
}

func LoadKeys(path string) (Keys, error) {
	var k Keys
	b, err := os.ReadFile(path)
	if err != nil {
		return k, errors.New("vault key file unavailable")
	}
	if json.Unmarshal(b, &k) != nil || k.Version == "" || len(k.Version) > 80 || len(k.EncryptionKey) != 32 || len(k.LookupKey) != 32 || len(k.SigningKey) != 32 {
		return k, errors.New("vault needs a version and three independent 32-byte keys")
	}
	if hmac.Equal(k.EncryptionKey, k.LookupKey) || hmac.Equal(k.EncryptionKey, k.SigningKey) || hmac.Equal(k.LookupKey, k.SigningKey) {
		return k, errors.New("vault keys must be independent")
	}
	return k, nil
}
func (k Keys) lookup(principal uuid.UUID) []byte {
	m := hmac.New(sha256.New, k.LookupKey)
	m.Write([]byte("account-locator:v1:" + principal.String()))
	return m.Sum(nil)
}
func (k Keys) sign(claim string) string {
	m := hmac.New(sha256.New, k.SigningKey)
	m.Write([]byte(claim))
	return fmtHex(m.Sum(nil))
}
func fmtHex(b []byte) string { return hex.EncodeToString(b) }
func (k Keys) aad(subject uuid.UUID) []byte {
	return []byte("vault/account_locator/principal/" + subject.String() + "/" + k.Version)
}
func (k Keys) seal(subject, principal uuid.UUID) ([]byte, error) {
	block, err := aes.NewCipher(k.EncryptionKey)
	if err != nil {
		return nil, err
	}
	g, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, g.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return nil, err
	}
	return g.Seal(nonce, nonce, principal[:], k.aad(subject)), nil
}
func (k Keys) open(subject uuid.UUID, b []byte) (uuid.UUID, error) {
	block, err := aes.NewCipher(k.EncryptionKey)
	if err != nil {
		return uuid.Nil, err
	}
	g, err := cipher.NewGCM(block)
	if err != nil {
		return uuid.Nil, err
	}
	if len(b) < g.NonceSize() {
		return uuid.Nil, errors.New("invalid ciphertext")
	}
	value, err := g.Open(nil, b[:g.NonceSize()], b[g.NonceSize():], k.aad(subject))
	if err != nil {
		return uuid.Nil, err
	}
	return uuid.FromBytes(value)
}

func (k Keys) fingerprint() []byte { b, _ := json.Marshal(k); hash := sha256.Sum256(b); return hash[:] }
func VerifyKeys(ctx context.Context, db *pgxpool.Pool, k Keys) error {
	var version string
	var fingerprint []byte
	if err := db.QueryRow(ctx, "SELECT key_version,fingerprint FROM vault.key_configuration WHERE singleton=true").Scan(&version, &fingerprint); err != nil {
		return errors.New("vault key binding unavailable")
	}
	if version != k.Version || !hmac.Equal(fingerprint, k.fingerprint()) {
		return errors.New("vault keys changed; reviewed rotation required")
	}
	return nil
}

// UpgradeLegacy preserves subjects and aliases, verifies the encrypted replacement,
// and removes the old live plaintext mapping in one transaction. Backups/WAL need separate retention.
func UpgradeLegacy(ctx context.Context, conn *pgx.Conn, k Keys) error {
	return pgx.BeginTxFunc(ctx, conn, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "LOCK TABLE vault.principal_subject,vault.account_locator,vault.key_configuration IN ACCESS EXCLUSIVE MODE"); err != nil {
			return err
		}
		var keyVersion string
		var fingerprint []byte
		err := tx.QueryRow(ctx, "SELECT key_version,fingerprint FROM vault.key_configuration WHERE singleton=true").Scan(&keyVersion, &fingerprint)
		if errors.Is(err, pgx.ErrNoRows) {
			// An upgrade from the first encrypted-locator release verifies existing data.
			existing, err := tx.Query(ctx, "SELECT subject_id,principal_ciphertext,key_version,lookup_hash FROM vault.account_locator")
			if err != nil {
				return err
			}
			for existing.Next() {
				var subject uuid.UUID
				var encrypted, lookup []byte
				var version string
				if err = existing.Scan(&subject, &encrypted, &version, &lookup); err != nil {
					existing.Close()
					return err
				}
				principal, err := k.open(subject, encrypted)
				if err != nil || version != k.Version || !hmac.Equal(lookup, k.lookup(principal)) {
					existing.Close()
					return errors.New("existing locator keys do not match")
				}
			}
			err = existing.Err()
			existing.Close()
			if err != nil {
				return err
			}
			if _, err = tx.Exec(ctx, "INSERT INTO vault.key_configuration(singleton,key_version,fingerprint) VALUES(true,$1,$2)", k.Version, k.fingerprint()); err != nil {
				return err
			}
		} else if err != nil {
			return err
		} else if keyVersion != k.Version || !hmac.Equal(fingerprint, k.fingerprint()) {
			return errors.New("vault keys changed; reviewed rotation required")
		}
		rows, err := tx.Query(ctx, "SELECT principal_ref,subject_id FROM vault.principal_subject")
		if err != nil {
			return err
		}
		type binding struct{ principal, subject uuid.UUID }
		bindings := []binding{}
		for rows.Next() {
			var b binding
			if err = rows.Scan(&b.principal, &b.subject); err != nil {
				rows.Close()
				return err
			}
			bindings = append(bindings, b)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for _, b := range bindings {
			encrypted, err := k.seal(b.subject, b.principal)
			if err != nil {
				return err
			}
			if _, err = tx.Exec(ctx, "INSERT INTO vault.account_locator(lookup_hash,subject_id,principal_ciphertext,key_version) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING", k.lookup(b.principal), b.subject, encrypted, k.Version); err != nil {
				return err
			}
			var stored []byte
			var version string
			var subject uuid.UUID
			if err = tx.QueryRow(ctx, "SELECT subject_id,principal_ciphertext,key_version FROM vault.account_locator WHERE lookup_hash=$1", k.lookup(b.principal)).Scan(&subject, &stored, &version); err != nil {
				return err
			}
			clear, err := k.open(subject, stored)
			if err != nil || clear != b.principal || subject != b.subject || version != k.Version {
				return errors.New("legacy vault migration verification failed")
			}
		}
		if _, err = tx.Exec(ctx, "DELETE FROM vault.principal_subject"); err != nil {
			return err
		}
		var mismatches int
		if err = tx.QueryRow(ctx, "SELECT count(*) FROM vault.account_locator WHERE key_version<>$1", k.Version).Scan(&mismatches); err != nil {
			return err
		}
		if mismatches != 0 {
			return errors.New("vault key version changed; reviewed rotation required")
		}
		return nil
	})
}

package vault

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"io"
	"net/http"
	"time"
)

type Service struct {
	DB, Auth            *pgxpool.Pool
	Keys                Keys
	Token, Mode, Issuer string
}
type Grant struct {
	Aliases   []uuid.UUID `json:"aliases"`
	Alias     uuid.UUID   `json:"alias,omitempty"`
	Claim     string      `json:"claim"`
	Signature string      `json:"signature"`
}
type claim struct {
	SessionHash string      `json:"sessionHash"`
	Aliases     []uuid.UUID `json:"aliases"`
	ExpiresAt   int64       `json:"expiresAt"`
}

func (s *Service) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health/ready", s.ready)
	mux.HandleFunc("GET /aliases", s.aliases)
	mux.HandleFunc("POST /aliases", s.aliases)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if len(s.Token) < 32 || !hmac.Equal([]byte(r.Header.Get("Authorization")), []byte("Bearer "+s.Token)) {
			http.Error(w, "unavailable", 401)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
		defer cancel()
		r = r.WithContext(ctx)
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		mux.ServeHTTP(w, r)
	})
}
func (s *Service) ready(w http.ResponseWriter, r *http.Request) {
	if s.DB.Ping(r.Context()) != nil || s.Auth.Ping(r.Context()) != nil {
		http.Error(w, "unavailable", 503)
		return
	}
	w.WriteHeader(204)
}
func (s *Service) audit(ctx context.Context, tx pgx.Tx, subject *uuid.UUID, purpose, result string, trace uuid.UUID) error {
	fields := []string{}
	if result == "ALLOWED" {
		fields = []string{"report_alias_id"}
	}
	_, err := tx.Exec(ctx, "INSERT INTO vault.audit_event(id,purpose_code,subject_id,action,result_code,field_allowlist,trace_id) VALUES($1,$2,$3,'ALIAS_ACCESS',$4,$5,$6)", uuid.New(), purpose, subject, result, fields, trace)
	return err
}
func (s *Service) aliases(w http.ResponseWriter, r *http.Request) {
	purpose := "SELF_REPORT_ALIASES"
	if r.Method == "POST" {
		purpose = "SELF_REPORT_ALIAS_ISSUE"
	}
	trace, _ := uuid.Parse(r.Header.Get("X-Request-ID"))
	if trace == uuid.Nil {
		trace = uuid.New()
	}
	raw := r.Header.Get("X-JanSetu-Session")
	hash := sha256.Sum256([]byte(raw))
	var principal uuid.UUID
	err := s.Auth.QueryRow(r.Context(), "SELECT principal_id FROM authz.authenticate($1,$2,$3)", hash[:], s.Mode, s.Issuer).Scan(&principal)
	if len(raw) != 43 || errors.Is(err, pgx.ErrNoRows) {
		auditErr := pgx.BeginTxFunc(r.Context(), s.DB, pgx.TxOptions{}, func(tx pgx.Tx) error { return s.audit(r.Context(), tx, nil, purpose, "DENIED", trace) })
		if auditErr != nil {
			http.Error(w, "unavailable", 503)
			return
		}
		http.Error(w, "sign in", 401)
		return
	}
	if err != nil {
		http.Error(w, "unavailable", 503)
		return
	}
	var submission uuid.UUID
	if r.Method == "POST" {
		var b struct {
			Submission uuid.UUID `json:"submissionId"`
		}
		d := json.NewDecoder(r.Body)
		d.DisallowUnknownFields()
		if d.Decode(&b) != nil || d.Decode(&struct{}{}) != io.EOF || b.Submission == uuid.Nil {
			http.Error(w, "invalid request", 400)
			return
		}
		submission = b.Submission
	} else if r.URL.RawQuery != "" {
		http.Error(w, "invalid request", 400)
		return
	}
	grant := Grant{Aliases: []uuid.UUID{}}
	err = pgx.BeginTxFunc(r.Context(), s.DB, pgx.TxOptions{}, func(tx pgx.Tx) error {
		lookup := s.Keys.lookup(principal)
		if _, err := tx.Exec(r.Context(), "SELECT pg_advisory_xact_lock(hashtextextended($1,0))", fmtHex(lookup)); err != nil {
			return err
		}
		var subject uuid.UUID
		err := tx.QueryRow(r.Context(), "SELECT subject_id FROM vault.account_locator WHERE lookup_hash=$1", lookup).Scan(&subject)
		if errors.Is(err, pgx.ErrNoRows) {
			if submission == uuid.Nil {
				return s.audit(r.Context(), tx, nil, purpose, "ALLOWED", trace)
			}
			subject = uuid.New()
			encrypted, err := s.Keys.seal(subject, principal)
			if err != nil {
				return err
			}
			if _, err = tx.Exec(r.Context(), "INSERT INTO vault.subject(id,safe_contact_policy,retention_policy_id) VALUES($1,'{}','local-demo-v1')", subject); err != nil {
				return err
			}
			if _, err = tx.Exec(r.Context(), "INSERT INTO vault.account_locator(lookup_hash,subject_id,principal_ciphertext,key_version) VALUES($1,$2,$3,$4)", lookup, subject, encrypted, s.Keys.Version); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		if submission != uuid.Nil {
			if err = tx.QueryRow(r.Context(), "INSERT INTO vault.pseudonym_binding(alias_id,subject_id,scope_kind,scope_id) VALUES($1,$2,'REPORT',$3) ON CONFLICT(subject_id,scope_kind,scope_id) DO UPDATE SET scope_id=EXCLUDED.scope_id RETURNING alias_id", uuid.New(), subject, submission).Scan(&grant.Alias); err != nil {
				return err
			}
			grant.Aliases = []uuid.UUID{grant.Alias}
		} else {
			rows, err := tx.Query(r.Context(), "SELECT alias_id FROM vault.pseudonym_binding WHERE subject_id=$1 AND scope_kind='REPORT' ORDER BY created_at,alias_id LIMIT 5001", subject)
			if err != nil {
				return err
			}
			for rows.Next() {
				var alias uuid.UUID
				if err = rows.Scan(&alias); err != nil {
					rows.Close()
					return err
				}
				grant.Aliases = append(grant.Aliases, alias)
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return err
			}
			if len(grant.Aliases) > 5000 {
				return errors.New("alias scope exceeds local bound")
			}
		}
		return s.audit(r.Context(), tx, &subject, purpose, "ALLOWED", trace)
	})
	if err != nil {
		http.Error(w, "unavailable", 503)
		return
	}
	b, err := json.Marshal(claim{SessionHash: fmtHex(hash[:]), Aliases: grant.Aliases, ExpiresAt: time.Now().Add(time.Minute).Unix()})
	if err != nil {
		http.Error(w, "unavailable", 503)
		return
	}
	grant.Claim = string(b)
	grant.Signature = s.Keys.sign(grant.Claim)
	_ = json.NewEncoder(w).Encode(grant)
}

package app

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lavkushry/JanSetu-AI/services/backend/internal/authn"
	"github.com/lavkushry/JanSetu-AI/services/backend/internal/media"
	"github.com/lavkushry/JanSetu-AI/services/backend/internal/platform"
	"github.com/lavkushry/JanSetu-AI/services/backend/internal/store/dbgen"
	"github.com/lavkushry/JanSetu-AI/services/backend/internal/vault"
)

type App struct {
	DB, Auth, Operations, Publication, Worker *pgxpool.Pool
	Vault                                     *vault.Client
	Config                                    platform.Config
	Media                                     *pgxpool.Pool
	Files                                     *media.Storage
	cursorKey                                 []byte
	Identity                                  *authn.Provider
}
type Actor struct {
	PrincipalID, ProfileID, SessionID uuid.UUID
	Roles                             []string
	Agencies                          []dbgen.AgencyGrantsRow
	Profile                           dbgen.ProfileRow
}
type Problem struct {
	Status    int    `json:"status"`
	Code      string `json:"code"`
	Title     string `json:"title"`
	Retryable bool   `json:"retryable"`
	RequestID string `json:"requestId,omitempty"`
}

func (p *Problem) Error() string { return p.Code }
func failure(status int, code, title string) error {
	return &Problem{Status: status, Code: code, Title: title}
}
func invalid(title string) error { return failure(422, "VALIDATION_FAILED", title) }
func unavailable() error         { return failure(404, "UNAVAILABLE", "This item is unavailable") }
func forbidden() error {
	return failure(403, "POLICY_DENIED", "This action is not allowed for your account")
}
func conflict() error {
	return failure(412, "VERSION_CONFLICT", "This item changed. Refresh before trying again")
}
func (a *Actor) Has(role string) bool {
	if a == nil {
		return false
	}
	for _, r := range a.Roles {
		if r == role {
			return true
		}
	}
	return false
}
func (a *Actor) Agency(id uuid.UUID, role string) bool {
	if a == nil {
		return false
	}
	for _, g := range a.Agencies {
		if g.AgencyID == id && (role == "" || g.Role == role) {
			return true
		}
	}
	return false
}
func actorID(a *Actor) uuid.UUID {
	if a == nil {
		return uuid.Nil
	}
	return a.ProfileID
}
func timestamp(v pgtype.Timestamptz) any {
	if !v.Valid {
		return nil
	}
	return v.Time.UTC().Format(time.RFC3339Nano)
}
func nullableUUID(v *uuid.UUID) any {
	if v == nil {
		return nil
	}
	return v.String()
}
func ptr[T any](v T) *T { return &v }

func New(db *pgxpool.Pool, vault *vault.Client, c platform.Config) *App {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		panic(err)
	}
	return &App{DB: db, Auth: db, Operations: db, Publication: db, Worker: db, Vault: vault, Config: c, cursorKey: key}
}

type endpoint func(http.ResponseWriter, *http.Request, *Actor) (any, int, error)

func (a *App) route(fn endpoint) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		requestID := uuid.NewString()
		w.Header().Set("X-Request-ID", requestID)
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		defer func() {
			if recover() != nil {
				slog.Error("request failed", "requestId", requestID)
				writeJSON(w, 500, &Problem{Status: 500, Code: "INTERNAL_ERROR", Title: "The request could not be completed", RequestID: requestID})
			}
		}()
		if r.Method != "GET" && r.Method != "HEAD" {
			if r.Header.Get("X-JanSetu-CSRF") != "1" || (r.Header.Get("Origin") != "" && r.Header.Get("Origin") != a.Config.WebOrigin) {
				writeJSON(w, 403, &Problem{Status: 403, Code: "ORIGIN_DENIED", Title: "Invalid request origin", RequestID: requestID})
				return
			}
		}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		r = a.requestScope(r.WithContext(ctx), requestID)
		actor, err := a.actor(r)
		if err != nil {
			a.respondError(w, err, requestID)
			return
		}
		result, status, err := fn(w, r, actor)
		if err != nil {
			a.respondError(w, err, requestID)
			return
		}
		if status == -1 {
			return
		}
		if status == 0 {
			status = 200
		}
		if status == 204 {
			w.WriteHeader(204)
			return
		}
		writeJSON(w, status, result)
	}
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func (a *App) respondError(w http.ResponseWriter, err error, id string) {
	var problem *Problem
	if !errors.As(err, &problem) {
		if errors.Is(err, pgx.ErrNoRows) {
			problem = &Problem{Status: 404, Code: "UNAVAILABLE", Title: "This item is unavailable"}
		} else {
			var databaseError *pgconn.PgError
			if errors.As(err, &databaseError) {
				if databaseError.Code == "23505" && databaseError.ConstraintName == "report_client_submission_id_key" {
					problem = &Problem{Status: 404, Code: "UNAVAILABLE", Title: "This item is unavailable"}
				} else {
					slog.Error("database operation failed", "requestId", id, "code", databaseError.Code, "constraint", databaseError.ConstraintName)
				}
			} else {
				slog.Error("request failed", "requestId", id, "errorType", fmt.Sprintf("%T", err))
			}
			if problem == nil {
				problem = &Problem{Status: 503, Code: "DEPENDENCY_UNAVAILABLE", Title: "Please try again. Your draft is preserved", Retryable: true}
			}
		}
	}
	copy := *problem
	copy.RequestID = id
	writeJSON(w, copy.Status, &copy)
}
func decode(r *http.Request, v any) error {
	d := json.NewDecoder(io.LimitReader(r.Body, 65537))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return failure(400, "INVALID_REQUEST", "Check the request fields")
	}
	var more any
	if err := d.Decode(&more); err != io.EOF {
		return failure(400, "INVALID_REQUEST", "Send one JSON object")
	}
	return nil
}

// Desired-state commands must distinguish an explicit false/zero from an
// omitted field, otherwise a malformed request could remove a relationship.
func decodeRequired(r *http.Request, v any, fields ...string) error {
	var raw json.RawMessage
	if e := decode(r, &raw); e != nil {
		return e
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil || object == nil {
		return invalid("Provide a JSON object with all required fields")
	}
	for _, field := range fields {
		value, ok := object[field]
		if !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return invalid("Provide all required fields")
		}
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if e := d.Decode(v); e != nil {
		return failure(400, "INVALID_REQUEST", "Check the request fields")
	}
	return nil
}
func id(r *http.Request, key string) (uuid.UUID, error) {
	v, e := uuid.Parse(r.PathValue(key))
	if e != nil {
		return uuid.Nil, unavailable()
	}
	return v, nil
}
func expected(r *http.Request) (int64, error) {
	v := strings.Trim(r.Header.Get("If-Match"), "\"")
	if v == "" {
		return 0, failure(428, "PRECONDITION_REQUIRED", "Refresh this item before changing it")
	}
	n, e := strconv.ParseInt(v, 10, 64)
	if e != nil || n < 1 {
		return 0, invalid("Invalid version")
	}
	return n, nil
}
func require(a *Actor) error {
	if a == nil {
		return failure(401, "AUTH_REQUIRED", "Sign in to continue")
	}
	return nil
}
func profileJSON(p dbgen.ProfileRow) map[string]any {
	return map[string]any{"id": p.ID, "handle": p.Handle, "displayName": p.DisplayName, "bio": p.Bio, "version": p.Version}
}
func (a *App) actor(r *http.Request) (*Actor, error) {
	c, e := r.Cookie("jansetu_session")
	if e == http.ErrNoCookie {
		return nil, nil
	}
	if e != nil {
		return nil, nil
	}
	hash := sha256.Sum256([]byte(c.Value))
	q := dbgen.New(a.Auth)
	s, e := q.SessionActor(r.Context(), dbgen.SessionActorParams{TokenHash: hash[:], AuthMethod: a.Config.AuthMode, OidcIssuer: pgtype.Text{String: a.Config.OIDCIssuer, Valid: true}})
	if errors.Is(e, pgx.ErrNoRows) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	if s.ProfileID == nil {
		return nil, nil
	}
	if _, e = a.Auth.Exec(r.Context(), "UPDATE identity.session SET last_seen_at=now() WHERE id=$1 AND revoked_at IS NULL AND expires_at>now() AND last_seen_at>now()-interval '30 minutes'", s.SessionID); e != nil {
		return nil, e
	}
	p, e := q.Profile(r.Context(), *s.ProfileID)
	if e != nil {
		return nil, e
	}
	roles, e := q.PlatformRoles(r.Context(), s.PrincipalID)
	if e != nil {
		return nil, e
	}
	agencies, e := q.AgencyGrants(r.Context(), s.PrincipalID)
	if e != nil {
		return nil, e
	}
	return &Actor{PrincipalID: s.PrincipalID, ProfileID: *s.ProfileID, SessionID: s.SessionID, Roles: roles, Agencies: agencies, Profile: p}, nil
}

var DemoPrincipals = []uuid.UUID{
	uuid.MustParse("10000000-0000-4000-8000-000000000001"), uuid.MustParse("10000000-0000-4000-8000-000000000002"),
	uuid.MustParse("10000000-0000-4000-8000-000000000004"), uuid.MustParse("10000000-0000-4000-8000-000000000005"), uuid.MustParse("10000000-0000-4000-8000-000000000006"),
}

func (a *App) accounts(w http.ResponseWriter, r *http.Request, _ *Actor) (any, int, error) {
	if a.Config.AuthMode != "demo" {
		return nil, 0, unavailable()
	}
	items := []map[string]any{}
	q := dbgen.New(a.Auth)
	for _, pid := range DemoPrincipals {
		var profileID uuid.UUID
		err := a.Auth.QueryRow(r.Context(), "SELECT profile_id FROM identity.principal WHERE id=$1 AND state='ACTIVE'", pid).Scan(&profileID)
		if err != nil {
			return nil, 0, err
		}
		p, err := q.Profile(r.Context(), profileID)
		if err != nil {
			return nil, 0, err
		}
		roles, err := q.PlatformRoles(r.Context(), pid)
		if err != nil {
			return nil, 0, err
		}
		agencies, err := q.AgencyGrants(r.Context(), pid)
		if err != nil {
			return nil, 0, err
		}
		role := "Resident"
		if len(roles) > 0 {
			role = "Coordinator & moderator"
		}
		if len(agencies) > 0 {
			if agencies[0].Role == "VERIFIER" {
				role = "Independent verifier"
			} else {
				role = "Agency officer"
			}
		}
		items = append(items, map[string]any{"id": pid, "profile": profileJSON(p), "role": role})
	}
	return map[string]any{"items": items, "synthetic": true}, 200, nil
}
func (a *App) signIn(w http.ResponseWriter, r *http.Request, _ *Actor) (any, int, error) {
	if a.Config.AuthMode != "demo" {
		return nil, 0, unavailable()
	}
	var body struct {
		PrincipalID uuid.UUID `json:"principalId"`
	}
	if err := decode(r, &body); err != nil {
		return nil, 0, err
	}
	allowed := false
	for _, p := range DemoPrincipals {
		if p == body.PrincipalID {
			allowed = true
		}
	}
	if !allowed {
		return nil, 0, forbidden()
	}
	token, err := randomSecret()
	if err != nil {
		return nil, 0, err
	}
	var oldHash []byte
	if old, e := r.Cookie("jansetu_session"); e == nil {
		oldHash = tokenHash(old.Value)
	}
	err = pgx.BeginTxFunc(r.Context(), a.Auth, pgx.TxOptions{}, func(tx pgx.Tx) error {
		return a.insertAccountSession(r.Context(), tx, body.PrincipalID, token, "demo", oldHash, authn.Identity{})
	})
	if err != nil {
		return nil, 0, err
	}
	a.sessionCookie(w, token, int(sessionLifetime.Seconds()))
	return map[string]any{"signedIn": true}, 200, nil
}
func (a *App) logout(w http.ResponseWriter, r *http.Request, actor *Actor) (any, int, error) {
	if actor != nil {
		err := a.securityTransaction(r, actor, func(tx pgx.Tx) error {
			if _, err := tx.Exec(r.Context(), "UPDATE identity.session SET revoked_at=now() WHERE id=$1", actor.SessionID); err != nil {
				return err
			}
			return accountAudit(r.Context(), tx, actor.PrincipalID, actor.SessionID, "SESSION", "SESSION_LOGOUT")
		})
		if err != nil {
			return nil, 0, err
		}
	}
	a.sessionCookie(w, "", -1)
	return nil, 204, nil
}
func (a *App) me(w http.ResponseWriter, r *http.Request, actor *Actor) (any, int, error) {
	if err := require(actor); err != nil {
		return nil, 0, err
	}
	return map[string]any{"profile": profileJSON(actor.Profile), "roles": actor.Roles, "agencies": actor.Agencies, "synthetic": true}, 200, nil
}

func (a *App) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/auth/login", a.loginRedirect)
	mux.HandleFunc("GET /v1/auth/callback", a.loginCallback)
	mux.HandleFunc("GET /health/live", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, map[string]bool{"ok": true}) })
	mux.HandleFunc("GET /health/ready", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if (a.Media != nil && a.Media.Ping(ctx) != nil) || a.DB.Ping(ctx) != nil || a.Auth.Ping(ctx) != nil || a.Operations.Ping(ctx) != nil || a.Publication.Ping(ctx) != nil || a.Vault.Ready(ctx) != nil {
			writeJSON(w, 503, map[string]bool{"ok": false})
			return
		}
		writeJSON(w, 200, map[string]bool{"ok": true})
	})
	for pattern, fn := range map[string]endpoint{
		"GET /v1/dev/accounts": a.accounts, "POST /v1/dev/session": a.signIn, "POST /v1/me/logout": a.logout, "GET /v1/me": a.me,
		"GET /v1/auth/config": a.authConfig, "GET /v1/me/sessions": a.listSessions, "DELETE /v1/me/sessions/{id}": a.revokeSession, "POST /v1/me/sessions/revoke-others": a.revokeOtherSessions, "PATCH /v1/me/profile": a.updateProfile,
		"POST /v1/media/uploads": a.createUpload, "GET /v1/media/{id}/upload": a.uploadStatus,
		"POST /v1/media/{id}/upload-parts": a.renewUpload, "PUT /v1/media/{id}/parts/{number}": a.uploadPart,
		"POST /v1/media/{id}/complete": a.completeUpload, "DELETE /v1/media/{id}/upload": a.abortUpload,
		"GET /v1/media/{id}": a.getMedia, "GET /v1/media/{id}/content": a.mediaContent,
		"POST /v1/media/{id}/analyses": a.createAnalysis, "GET /v1/analyses/{id}": a.getAnalysis,
		"POST /v1/analyses/{id}/retry": a.retryAnalysis, "DELETE /v1/analyses/{id}": a.cancelAnalysis,
		"GET /v1/capabilities": a.capabilities, "GET /v1/communities": a.communities, "GET /v1/communities/{id}": a.community,
		"PUT /v1/communities/{id}/membership": a.membership, "PUT /v1/communities/{id}/follow": a.communityFollow,
		"GET /v1/feed": a.feed, "GET /v1/search": a.search, "GET /v1/me/bookmarks": a.bookmarks,
		"GET /v1/posts/{id}": a.getPost, "POST /v1/posts": a.createPost, "PATCH /v1/posts/{id}": a.editPost, "DELETE /v1/posts/{id}": a.deletePost,
		"GET /v1/posts/{id}/comments": a.comments, "POST /v1/posts/{id}/comments": a.createComment, "PATCH /v1/comments/{id}": a.editComment, "DELETE /v1/comments/{id}": a.deleteComment,
		"PUT /v1/posts/{id}/vote": a.vote, "PUT /v1/posts/{id}/bookmark": a.bookmark, "PUT /v1/posts/{id}/repost": a.repost,
		"PUT /v1/me/blocks/{id}": a.block, "PUT /v1/me/following/{id}": a.follow,
		"GET /v1/profiles/{id}": a.publicProfile, "GET /v1/profiles/{id}/posts": a.profilePosts, "GET /v1/me/blocks": a.blockedPeople,
		"GET /v1/me/activity": a.activity, "GET /v1/me/activity/summary": a.activitySummary, "PUT /v1/me/activity/{id}/read": a.activityRead,
		"GET /v1/moderation": a.moderationQueue, "POST /v1/moderation/{id}/decisions": a.moderationDecision,
		"GET /v1/case-receipts/{id}": a.getReceipt, "PUT /v1/case-receipts/{id}/follow": a.caseFollow,
		"POST /v1/service-reports": a.submitReport, "GET /v1/my-reports": a.myReports, "GET /v1/my-reports/{id}": a.myReport,
		"GET /v1/authority/intake": a.intakeQueue, "GET /v1/authority/agencies": a.agencies, "POST /v1/authority/reports/{id}/triage": a.triage,
		"GET /v1/authority/cases": a.cases, "GET /v1/authority/cases/{id}": a.caseDetail,
		"POST /v1/authority/obligations/{id}/accept": a.acceptObligation, "POST /v1/authority/obligations/{id}/start": a.startWork,
		"POST /v1/authority/obligations/{id}/completion-claims": a.claimCompletion,
		"POST /v1/authority/cases/{id}/verification-decisions":  a.verify,
		"POST /v1/authority/cases/{id}/publications":            a.publishReceipt,
	} {
		mux.HandleFunc(pattern, a.route(fn))
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		limit := int64(65536)
		if r.Method == "PUT" && strings.HasPrefix(r.URL.Path, "/v1/media/") && strings.Contains(r.URL.Path, "/parts/") {
			limit = media.MaxBytes
		}
		r.Body = http.MaxBytesReader(w, r.Body, limit)
		mux.ServeHTTP(w, r)
	})
}

package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/lavkushry/JanSetu-AI/services/backend/internal/authn"
	"golang.org/x/oauth2"
)

const sessionLifetime = 12 * time.Hour

func randomSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
func tokenHash(token string) []byte { h := sha256.Sum256([]byte(token)); return h[:] }
func (a *App) sessionCookie(w http.ResponseWriter, token string, maxAge int) {
	http.SetCookie(w, &http.Cookie{Name: "jansetu_session", Value: token, Path: "/", HttpOnly: true,
		Secure: a.Config.SecureCookies(), SameSite: http.SameSiteLaxMode, MaxAge: maxAge})
}
func (a *App) flowCookie(w http.ResponseWriter, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{Name: "jansetu_login", Value: value, Path: "/api/auth/callback", HttpOnly: true,
		Secure: a.Config.SecureCookies(), SameSite: http.SameSiteLaxMode, MaxAge: maxAge})
}
func safeReturnPath(path string) bool {
	if path == "" || len(path) > 1000 || !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") ||
		strings.ContainsAny(path, "\\\r\n") {
		return false
	}
	u, err := url.Parse(path)
	return err == nil && u.Host == "" && u.Scheme == "" && !strings.HasPrefix(u.Path, "/api/") &&
		!strings.HasPrefix(u.Path, "//") && !strings.ContainsAny(u.Path, "\\\r\n")
}
func (a *App) authConfig(w http.ResponseWriter, r *http.Request, _ *Actor) (any, int, error) {
	return map[string]any{"mode": a.Config.AuthMode, "loginPath": "/api/auth/login", "synthetic": true}, 200, nil
}
func (a *App) loginRedirect(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	if a.Config.AuthMode != "oidc" || a.Identity == nil {
		a.respondError(w, unavailable(), uuid.NewString())
		return
	}
	returnPath := r.URL.Query().Get("returnTo")
	if returnPath == "" {
		returnPath = "/"
	}
	if !safeReturnPath(returnPath) {
		a.respondError(w, invalid("Choose an application page to return to"), uuid.NewString())
		return
	}
	state, err := randomSecret()
	if err != nil {
		a.respondError(w, err, uuid.NewString())
		return
	}
	browser, err := randomSecret()
	if err != nil {
		a.respondError(w, err, uuid.NewString())
		return
	}
	nonce, err := randomSecret()
	if err != nil {
		a.respondError(w, err, uuid.NewString())
		return
	}
	verifier := oauth2.GenerateVerifier()
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	err = pgx.BeginTxFunc(ctx, a.Auth, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "DELETE FROM identity.login_flow WHERE expires_at < now()"); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO identity.login_flow(state_hash,browser_hash,nonce_hash,pkce_verifier,return_path,expires_at)
		VALUES($1,$2,$3,$4,$5,now()+interval '10 minutes')`, tokenHash(state), tokenHash(browser), tokenHash(nonce), verifier, returnPath)
		return err
	})
	if err != nil {
		a.respondError(w, err, uuid.NewString())
		return
	}
	a.flowCookie(w, browser, 600)
	http.Redirect(w, r, a.Identity.AuthorizationURL(state, nonce, verifier), http.StatusFound)
}

func (a *App) loginCallback(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	a.flowCookie(w, "", -1)
	fail := func() { http.Redirect(w, r, a.Config.WebOrigin+"/?auth=failed", http.StatusSeeOther) }
	if a.Config.AuthMode != "oidc" || a.Identity == nil {
		fail()
		return
	}
	query := r.URL.Query()
	c, err := r.Cookie("jansetu_login")
	if err != nil || len(c.Value) != 43 || len(query["state"]) != 1 || len(query.Get("state")) != 43 {
		fail()
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	var nonceHash []byte
	var verifier, returnPath string
	// Consumption commits before token exchange; two callbacks cannot use one flow.
	err = a.Auth.QueryRow(ctx, `DELETE FROM identity.login_flow WHERE state_hash=$1 AND browser_hash=$2 AND expires_at>now()
	RETURNING nonce_hash,pkce_verifier,return_path`, tokenHash(query.Get("state")), tokenHash(c.Value)).Scan(&nonceHash, &verifier, &returnPath)
	if err != nil || query.Get("error") != "" || len(query["code"]) != 1 || len(query.Get("code")) == 0 || len(query.Get("code")) > 4096 {
		fail()
		return
	}
	identity, err := a.Identity.Exchange(ctx, query.Get("code"), verifier, nonceHash)
	if err != nil {
		fail()
		return
	}
	token, err := randomSecret()
	if err != nil {
		fail()
		return
	}
	var oldHash []byte
	if old, e := r.Cookie("jansetu_session"); e == nil {
		oldHash = tokenHash(old.Value)
	}
	err = a.provisionSession(ctx, identity, token, oldHash)
	if err != nil {
		fail()
		return
	}
	a.sessionCookie(w, token, int(sessionLifetime.Seconds()))
	http.Redirect(w, r, a.Config.WebOrigin+returnPath, http.StatusSeeOther)
}

func accountAudit(ctx context.Context, tx pgx.Tx, actor, object uuid.UUID, kind, action string) error {
	_, err := tx.Exec(ctx, `INSERT INTO infra.audit_event(id,actor_ref,purpose_code,object_kind,object_id,action,result_code,trace_id,occurred_at)
	VALUES($1,$2,'ACCOUNT_SECURITY',$3,$4,$5,'ALLOWED',$6,now())`, uuid.New(), actor, kind, object, action, uuid.NewString())
	return err
}

func (a *App) provisionSession(ctx context.Context, identity authn.Identity, token string, oldHash []byte) error {
	return pgx.BeginTxFunc(ctx, a.Auth, pgx.TxOptions{}, func(tx pgx.Tx) error {
		// Serialize first sign-in of the exact issuer/subject; no email matching.
		if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1,0))", identity.Issuer+"\x1f"+identity.Subject); err != nil {
			return err
		}
		var principal uuid.UUID
		var state string
		err := tx.QueryRow(ctx, "SELECT principal_id,state FROM identity.account_binding WHERE provider=$1 AND provider_subject=$2 FOR UPDATE", identity.Issuer, identity.Subject).Scan(&principal, &state)
		if errors.Is(err, pgx.ErrNoRows) {
			principal = uuid.New()
			profile := uuid.New()
			// A provider's name/email is never silently published as a social identity.
			handle := "resident_" + strings.ReplaceAll(profile.String(), "-", "")[:20]
			if _, err = tx.Exec(ctx, "INSERT INTO social.profile(id,handle,display_name,state) VALUES($1,$2,'New neighbour','ACTIVE')", profile, handle); err != nil {
				return err
			}
			if _, err = tx.Exec(ctx, "INSERT INTO identity.principal(id,profile_id,state) VALUES($1,$2,'ACTIVE')", principal, profile); err != nil {
				return err
			}
			if _, err = tx.Exec(ctx, "INSERT INTO identity.account_binding(provider,provider_subject,principal_id,state) VALUES($1,$2,$3,'ACTIVE')", identity.Issuer, identity.Subject, principal); err != nil {
				return err
			}
			if err = accountAudit(ctx, tx, principal, principal, "PRINCIPAL", "ACCOUNT_PROVISIONED"); err != nil {
				return err
			}
		} else if err != nil {
			return err
		} else if state != "ACTIVE" {
			return forbidden()
		}
		return a.insertAccountSession(ctx, tx, principal, token, "oidc", oldHash, identity)
	})
}

func (a *App) insertAccountSession(ctx context.Context, tx pgx.Tx, principal uuid.UUID, token, method string, oldHash []byte, identity authn.Identity) error {
	var state string
	if err := tx.QueryRow(ctx, "SELECT state FROM identity.principal WHERE id=$1 FOR UPDATE", principal).Scan(&state); err != nil {
		return err
	}
	if state != "ACTIVE" {
		return forbidden()
	}
	var profileState string
	if err := tx.QueryRow(ctx, "SELECT p.state FROM social.profile p JOIN identity.principal ip ON ip.profile_id=p.id WHERE ip.id=$1", principal).Scan(&profileState); err != nil {
		return err
	}
	if profileState != "ACTIVE" {
		return forbidden()
	}
	if oldHash != nil {
		var oldID, owner uuid.UUID
		err := tx.QueryRow(ctx, "UPDATE identity.session SET revoked_at=now() WHERE token_hash=$1 AND revoked_at IS NULL RETURNING id,principal_id", oldHash).Scan(&oldID, &owner)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if err == nil {
			if err = accountAudit(ctx, tx, owner, oldID, "SESSION", "SESSION_REPLACED"); err != nil {
				return err
			}
		}
	}
	// Bound live sessions per account; only the most recent 19 precede this one.
	rows, err := tx.Query(ctx, `UPDATE identity.session SET revoked_at=now() WHERE id IN (
 SELECT id FROM identity.session WHERE principal_id=$1 AND revoked_at IS NULL AND expires_at>now()
 AND last_seen_at>now()-interval '30 minutes' ORDER BY created_at DESC,id DESC OFFSET 19) RETURNING id`, principal)
	if err != nil {
		return err
	}
	evicted := []uuid.UUID{}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		evicted = append(evicted, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range evicted {
		if err := accountAudit(ctx, tx, principal, id, "SESSION", "SESSION_LIMIT_REVOKED"); err != nil {
			return err
		}
	}
	id := uuid.New()
	if _, err := tx.Exec(ctx, "INSERT INTO identity.session(id,principal_id,token_hash,expires_at,auth_method,provider,provider_subject) VALUES($1,$2,$3,$4,$5,NULLIF($6,''),NULLIF($7,''))", id, principal, tokenHash(token), time.Now().Add(sessionLifetime), method, identity.Issuer, identity.Subject); err != nil {
		return err
	}
	return accountAudit(ctx, tx, principal, id, "SESSION", "SESSION_CREATED")
}

type accountSession struct {
	ID         uuid.UUID `json:"id"`
	CreatedAt  any       `json:"createdAt"`
	LastSeenAt any       `json:"lastSeenAt"`
	ExpiresAt  any       `json:"expiresAt"`
	Current    bool      `json:"current"`
	Method     string    `json:"method"`
}

func (a *App) listSessions(w http.ResponseWriter, r *http.Request, actor *Actor) (any, int, error) {
	if err := require(actor); err != nil {
		return nil, 0, err
	}
	rows, err := a.Auth.Query(r.Context(), `SELECT id,created_at,last_seen_at,expires_at,auth_method FROM identity.session
	WHERE principal_id=$1 AND revoked_at IS NULL AND expires_at>now() AND last_seen_at>now()-interval '30 minutes'
	ORDER BY created_at DESC,id DESC LIMIT 20`, actor.PrincipalID)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := []accountSession{}
	for rows.Next() {
		var s accountSession
		var created, seen, expires pgtype.Timestamptz
		if err := rows.Scan(&s.ID, &created, &seen, &expires, &s.Method); err != nil {
			return nil, 0, err
		}
		s.CreatedAt, s.LastSeenAt, s.ExpiresAt = timestamp(created), timestamp(seen), timestamp(expires)
		s.Current = s.ID == actor.SessionID
		items = append(items, s)
	}
	return map[string]any{"items": items}, 200, rows.Err()
}

func (a *App) securityTransaction(r *http.Request, actor *Actor, fn func(pgx.Tx) error) error {
	if err := require(actor); err != nil {
		return err
	}
	return pgx.BeginTxFunc(r.Context(), a.Auth, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := a.configureScope(r.Context(), tx); err != nil {
			return err
		}
		var state string
		if err := tx.QueryRow(r.Context(), "SELECT state FROM identity.principal WHERE id=$1 FOR UPDATE", actor.PrincipalID).Scan(&state); err != nil {
			return err
		}
		if state != "ACTIVE" {
			return forbidden()
		}
		if err := a.checkSession(r.Context(), tx, actor); err != nil {
			return err
		}
		return fn(tx)
	})
}
func (a *App) checkSession(ctx context.Context, tx pgx.Tx, actor *Actor) error {
	var active bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT FROM authz.authenticate($1,$2,$3) WHERE principal_id=$4 AND session_id=$5)`, tokenHash(scope(ctx).Session), a.Config.AuthMode, a.Config.OIDCIssuer, actor.PrincipalID, actor.SessionID).Scan(&active)
	if err != nil {
		return err
	}
	if !active {
		return failure(401, "AUTH_REQUIRED", "Sign in to continue")
	}
	return nil
}
func (a *App) revokeSession(w http.ResponseWriter, r *http.Request, actor *Actor) (any, int, error) {
	if err := require(actor); err != nil {
		return nil, 0, err
	}
	id, err := id(r, "id")
	if err != nil {
		return nil, 0, err
	}
	err = a.securityTransaction(r, actor, func(tx pgx.Tx) error {
		var found uuid.UUID
		if err := tx.QueryRow(r.Context(), "SELECT id FROM identity.session WHERE id=$1 AND principal_id=$2", id, actor.PrincipalID).Scan(&found); err != nil {
			return err
		}
		tag, err := tx.Exec(r.Context(), "UPDATE identity.session SET revoked_at=now() WHERE id=$1 AND revoked_at IS NULL", id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return nil
		}
		return accountAudit(r.Context(), tx, actor.PrincipalID, id, "SESSION", "SESSION_REVOKED")
	})
	if err == nil && id == actor.SessionID {
		a.sessionCookie(w, "", -1)
	}
	return nil, 204, err
}
func (a *App) revokeOtherSessions(w http.ResponseWriter, r *http.Request, actor *Actor) (any, int, error) {
	err := a.securityTransaction(r, actor, func(tx pgx.Tx) error {
		if _, err := tx.Exec(r.Context(), "UPDATE identity.session SET revoked_at=now() WHERE principal_id=$1 AND id<>$2 AND revoked_at IS NULL", actor.PrincipalID, actor.SessionID); err != nil {
			return err
		}
		return accountAudit(r.Context(), tx, actor.PrincipalID, actor.SessionID, "SESSION", "OTHER_SESSIONS_REVOKED")
	})
	return nil, 204, err
}

var handlePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{2,29}$`)

func (a *App) updateProfile(w http.ResponseWriter, r *http.Request, actor *Actor) (any, int, error) {
	var body struct {
		Handle      string `json:"handle"`
		DisplayName string `json:"displayName"`
		Bio         string `json:"bio"`
	}
	if err := decodeRequired(r, &body, "handle", "displayName", "bio"); err != nil {
		return nil, 0, err
	}
	body.DisplayName = strings.TrimSpace(body.DisplayName)
	body.Bio = strings.TrimSpace(body.Bio)
	if !handlePattern.MatchString(body.Handle) || !textValid(body.DisplayName, 1, 80) || !textValid(body.Bio, 0, 500) {
		return nil, 0, invalid("Use a 3–30 character handle, a name up to 80 characters, and a bio up to 500 characters")
	}
	version, err := expected(r)
	if err != nil {
		return nil, 0, err
	}
	err = a.securityTransaction(r, actor, func(tx pgx.Tx) error {
		tag, err := tx.Exec(r.Context(), `UPDATE social.profile SET handle=$1,display_name=$2,bio=$3,version=version+1
		WHERE id=$4 AND state='ACTIVE' AND version=$5`, body.Handle, body.DisplayName, body.Bio, actor.ProfileID, version)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return conflict()
		}
		return accountAudit(r.Context(), tx, actor.PrincipalID, actor.ProfileID, "PROFILE", "PROFILE_UPDATED")
	})
	var dbError *pgconn.PgError
	if errors.As(err, &dbError) && dbError.Code == "23505" {
		return nil, 0, failure(409, "HANDLE_UNAVAILABLE", "This handle is already in use")
	}
	if err != nil {
		return nil, 0, err
	}
	return map[string]any{"updated": true}, 200, nil
}

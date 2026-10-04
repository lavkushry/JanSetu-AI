package app

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/google/uuid"
	"github.com/lavkushry/JanSetu-AI/services/backend/internal/authn"
	"github.com/lavkushry/JanSetu-AI/services/backend/internal/store/dbgen"
	"golang.org/x/oauth2"
)

// A real signed-token/discovery server exercises the entire callback boundary,
// including the provider-side PKCE check. Browser journeys also use real Keycloak.
type testOIDC struct {
	server *httptest.Server
	key    *rsa.PrivateKey
	mu     sync.Mutex
	codes  map[string]testCode
}
type testCode struct{ challenge, token string }

func newTestOIDC(t *testing.T) *testOIDC {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	p := &testOIDC{key: key, codes: map[string]testCode{}}
	p.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			_ = json.NewEncoder(w).Encode(map[string]any{"issuer": p.server.URL, "authorization_endpoint": p.server.URL + "/authorize", "token_endpoint": p.server.URL + "/token", "jwks_uri": p.server.URL + "/keys", "id_token_signing_alg_values_supported": []string{"RS256"}})
		case "/keys":
			_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &key.PublicKey, KeyID: "test-key", Use: "sig", Algorithm: "RS256"}}})
		case "/token":
			_ = r.ParseForm()
			p.mu.Lock()
			code, ok := p.codes[r.Form.Get("code")]
			delete(p.codes, r.Form.Get("code"))
			p.mu.Unlock()
			if !ok || r.Form.Get("client_id") != "jansetu-web" || r.Form.Get("grant_type") != "authorization_code" || oauth2.S256ChallengeFromVerifier(r.Form.Get("code_verifier")) != code.challenge {
				w.WriteHeader(400)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant"})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "unused-provider-access-token", "token_type": "Bearer", "expires_in": 300, "id_token": code.token})
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(p.server.Close)
	return p
}
func (p *testOIDC) application(t *testing.T) *App {
	base := testApp(t)
	cfg := base.Config
	cfg.AuthMode = "oidc"
	cfg.OIDCIssuer = p.server.URL
	cfg.OIDCBackchannel = ""
	a := cloneTestApp(t, cfg)
	var err error
	a.Identity, err = authn.Discover(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

type flow struct {
	state         string
	cookie        *http.Cookie
	authorization url.Values
}

func beginFlow(t *testing.T, a *App) flow {
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/v1/auth/login?returnTo=%2Faccount", nil)
	a.Handler().ServeHTTP(w, r)
	mustStatus(t, w, 302)
	u, err := url.Parse(w.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if q.Get("code_challenge_method") != "S256" || q.Get("nonce") == "" || q.Get("scope") != "openid" {
		t.Fatal("OIDC authorization protections missing")
	}
	return flow{state: q.Get("state"), cookie: w.Result().Cookies()[0], authorization: q}
}
func (p *testOIDC) code(t *testing.T, f flow, subject string, alter func(map[string]any)) string {
	claims := map[string]any{"iss": p.server.URL, "sub": subject, "aud": "jansetu-web", "exp": time.Now().Add(time.Minute).Unix(), "iat": time.Now().Unix(), "nonce": f.authorization.Get("nonce"), "email": "coordinator@example.test", "name": "Kiran Shah", "roles": []string{"COORDINATOR", "PUBLISHER"}}
	if alter != nil {
		alter(claims)
	}
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: p.key}, (&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", "test-key"))
	if err != nil {
		t.Fatal(err)
	}
	signed, err := signer.Sign(jsonBytes(claims))
	if err != nil {
		t.Fatal(err)
	}
	token, err := signed.CompactSerialize()
	if err != nil {
		t.Fatal(err)
	}
	code := uuid.NewString()
	p.mu.Lock()
	p.codes[code] = testCode{challenge: f.authorization.Get("code_challenge"), token: token}
	p.mu.Unlock()
	return code
}
func callback(a *App, f flow, code string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("GET", "/v1/auth/callback?state="+url.QueryEscape(f.state)+"&code="+url.QueryEscape(code), nil)
	if f.cookie != nil {
		r.AddCookie(f.cookie)
	}
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, r)
	return w
}
func signedClient(t *testing.T, a *App, w *httptest.ResponseRecorder) client {
	t.Helper()
	mustStatus(t, w, 303)
	if w.Header().Get("Location") != a.Config.WebOrigin+"/account" {
		t.Fatalf("login failed: %s", w.Header().Get("Location"))
	}
	for _, cookie := range w.Result().Cookies() {
		if cookie.Name == "jansetu_session" {
			if !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode || cookie.MaxAge != 43200 {
				t.Fatal("unsafe session cookie")
			}
			return client{app: a, cookie: cookie}
		}
	}
	t.Fatal("session not created")
	return client{}
}
func assertNoSession(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	mustStatus(t, w, 303)
	if !strings.HasSuffix(w.Header().Get("Location"), "/?auth=failed") {
		t.Fatal("failure exposed provider details")
	}
	for _, cookie := range w.Result().Cookies() {
		if cookie.Name == "jansetu_session" && cookie.Value != "" {
			t.Fatal("invalid flow created a session")
		}
	}
}

func TestOIDCProvisioningAndCallbackBoundary(t *testing.T) {
	testApp(t)
	p := newTestOIDC(t)
	a := p.application(t)
	subject := uuid.NewString()
	f := beginFlow(t, a)
	code := p.code(t, f, subject, nil)
	// A code/state captured in a different browser cannot complete the flow.
	wrong := f
	wrong.cookie = &http.Cookie{Name: "jansetu_login", Value: strings.Repeat("x", 43)}
	assertNoSession(t, callback(a, wrong, code))
	resident := signedClient(t, a, callback(a, f, code))
	me := resident.request("GET", "me", nil, 0, "")
	mustStatus(t, me, 200)
	data := parsed[struct {
		Profile struct {
			ID                  uuid.UUID
			Handle, DisplayName string
		}
		Roles    []string
		Agencies []any
	}](t, me)
	if data.Profile.DisplayName != "New neighbour" || len(data.Roles) != 0 || len(data.Agencies) != 0 || strings.Contains(me.Body.String(), "coordinator@example.test") {
		t.Fatal("provider claims leaked or granted staff permissions")
	}
	mustStatus(t, resident.request("GET", "moderation", nil, 0, ""), 403)
	assertNoSession(t, callback(a, f, code))
	// A second first-login race must reuse the exact account binding.
	f1, f2 := beginFlow(t, a), beginFlow(t, a)
	c1, c2 := p.code(t, f1, subject, nil), p.code(t, f2, subject, nil)
	results := make(chan *httptest.ResponseRecorder, 2)
	var wg sync.WaitGroup
	for _, pair := range []struct {
		f flow
		c string
	}{{f1, c1}, {f2, c2}} {
		wg.Add(1)
		go func() { defer wg.Done(); results <- callback(a, pair.f, pair.c) }()
	}
	wg.Wait()
	close(results)
	for result := range results {
		signedClient(t, a, result)
	}
	var count int
	if err := integrationAdmin.QueryRow(context.Background(), "SELECT count(*) FROM identity.account_binding WHERE provider=$1 AND provider_subject=$2", p.server.URL, subject).Scan(&count); err != nil || count != 1 {
		t.Fatal("duplicate provisioning", err, count)
	}
	// Fixture shortcuts cannot bypass OIDC.
	mustStatus(t, (client{app: a}).request("POST", "dev/session", map[string]any{"principalId": DemoPrincipals[2]}, 0, ""), 404)
	mustStatus(t, (client{app: a}).request("GET", "dev/accounts", nil, 0, ""), 404)
	// Account binding revocation invalidates existing sessions and future sign-in.
	if _, err := integrationAdmin.Exec(context.Background(), "UPDATE identity.account_binding SET state='REVOKED' WHERE provider=$1 AND provider_subject=$2", p.server.URL, subject); err != nil {
		t.Fatal(err)
	}
	mustStatus(t, resident.request("GET", "me", nil, 0, ""), 401)
	f = beginFlow(t, a)
	assertNoSession(t, callback(a, f, p.code(t, f, subject, nil)))
}

func TestOIDCRejectsInvalidIdentityAssertions(t *testing.T) {
	testApp(t)
	p := newTestOIDC(t)
	a := p.application(t)
	cases := map[string]func(map[string]any){
		"wrong nonce":                      func(c map[string]any) { c["nonce"] = "another-flow" },
		"missing nonce":                    func(c map[string]any) { delete(c, "nonce") },
		"wrong issuer":                     func(c map[string]any) { c["iss"] = "https://other.example.test" },
		"wrong audience":                   func(c map[string]any) { c["aud"] = "other-client" },
		"expired":                          func(c map[string]any) { c["exp"] = time.Now().Add(-time.Hour).Unix() },
		"missing subject":                  func(c map[string]any) { delete(c, "sub") },
		"multiple audiences without party": func(c map[string]any) { c["aud"] = []string{"jansetu-web", "other"} },
		"wrong authorized party":           func(c map[string]any) { c["azp"] = "other" },
	}
	for name, alter := range cases {
		t.Run(name, func(t *testing.T) {
			f := beginFlow(t, a)
			assertNoSession(t, callback(a, f, p.code(t, f, uuid.NewString(), alter)))
		})
	}
	t.Run("invalid signature", func(t *testing.T) {
		f := beginFlow(t, a)
		code := p.code(t, f, uuid.NewString(), nil)
		p.mu.Lock()
		saved := p.codes[code]
		parts := strings.Split(saved.token, ".")
		parts[2] = base64.RawURLEncoding.EncodeToString(make([]byte, 256))
		saved.token = strings.Join(parts, ".")
		p.codes[code] = saved
		p.mu.Unlock()
		assertNoSession(t, callback(a, f, code))
	})
	t.Run("missing browser cookie", func(t *testing.T) {
		f := beginFlow(t, a)
		code := p.code(t, f, uuid.NewString(), nil)
		f.cookie = nil
		assertNoSession(t, callback(a, f, code))
	})
	t.Run("PKCE mismatch", func(t *testing.T) {
		f := beginFlow(t, a)
		other := beginFlow(t, a)
		assertNoSession(t, callback(a, f, p.code(t, other, uuid.NewString(), nil)))
	})
	t.Run("expired flow", func(t *testing.T) {
		f := beginFlow(t, a)
		if _, err := integrationAdmin.Exec(context.Background(), "UPDATE identity.login_flow SET expires_at=now()-interval '1 second' WHERE state_hash=$1", tokenHash(f.state)); err != nil {
			t.Fatal(err)
		}
		assertNoSession(t, callback(a, f, p.code(t, f, uuid.NewString(), nil)))
	})
	t.Run("duplicate callback state", func(t *testing.T) {
		f := beginFlow(t, a)
		r := httptest.NewRequest("GET", "/v1/auth/callback?state="+f.state+"&state=another&code=any", nil)
		r.AddCookie(f.cookie)
		w := httptest.NewRecorder()
		a.Handler().ServeHTTP(w, r)
		assertNoSession(t, w)
	})
	for _, path := range []string{"https://outside.test", "//outside.test", "/\\outside.test", "/%2foutside.test", "/api/auth/login", "/hello\r\nLocation: evil"} {
		if safeReturnPath(path) {
			t.Fatalf("unsafe return path accepted: %q", path)
		}
	}
}

func TestSessionOwnershipExpiryAndRevokedCommands(t *testing.T) {
	a := testApp(t)
	first, second := login(t, a, 0), login(t, a, 0)
	stranger := login(t, a, 1)
	listed := first.request("GET", "me/sessions", nil, 0, "")
	mustStatus(t, listed, 200)
	if strings.Contains(listed.Body.String(), "token") || strings.Contains(listed.Body.String(), "provider_subject") {
		t.Fatal("sensitive session data exposed")
	}
	items := parsed[struct {
		Items []struct {
			ID      uuid.UUID
			Current bool
		}
	}](t, listed).Items
	var current uuid.UUID
	for _, s := range items {
		if s.Current {
			current = s.ID
		}
	}
	if current == uuid.Nil {
		t.Fatal("current session not identified")
	}
	mustStatus(t, stranger.request("DELETE", "me/sessions/"+current.String(), nil, 0, ""), 404)
	r := httptest.NewRequest("GET", "/v1/me", nil)
	r.AddCookie(first.cookie)
	actor, err := a.actor(r)
	if err != nil {
		t.Fatal(err)
	}
	mustStatus(t, second.request("POST", "me/sessions/revoke-others", nil, 0, ""), 204)
	mustStatus(t, first.request("GET", "me", nil, 0, ""), 401)
	called := false
	err = a.transaction(context.Background(), actor, func(*dbgen.Queries) error { called = true; return nil })
	if err == nil || called {
		t.Fatal("revoked session entered an authorized command")
	}
	if _, err = integrationAdmin.Exec(context.Background(), "UPDATE identity.session SET last_seen_at=now()-interval '31 minutes' WHERE token_hash=$1", tokenHash(second.cookie.Value)); err != nil {
		t.Fatal(err)
	}
	mustStatus(t, second.request("GET", "me", nil, 0, ""), 401)
	third := login(t, a, 0)
	if _, err = integrationAdmin.Exec(context.Background(), "UPDATE identity.session SET created_at=now()-interval '2 hours',expires_at=now()-interval '1 hour' WHERE token_hash=$1", tokenHash(third.cookie.Value)); err != nil {
		t.Fatal(err)
	}
	mustStatus(t, third.request("GET", "me", nil, 0, ""), 401)
	var events int
	if err = integrationAdmin.QueryRow(context.Background(), "SELECT count(*) FROM infra.audit_event WHERE purpose_code='ACCOUNT_SECURITY'").Scan(&events); err != nil || events == 0 {
		t.Fatal("security audit missing")
	}
	if _, err = integrationAdmin.Exec(context.Background(), "DELETE FROM infra.audit_event WHERE purpose_code='ACCOUNT_SECURITY'"); err == nil {
		t.Fatal("audit mutation allowed")
	}
}

func TestProfileUpdatesAreVersionedAndPrivate(t *testing.T) {
	testApp(t)
	p := newTestOIDC(t)
	a := p.application(t)
	f := beginFlow(t, a)
	owner := signedClient(t, a, callback(a, f, p.code(t, f, uuid.NewString(), nil)))
	body := map[string]any{"handle": "neighbour_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12], "displayName": "A chosen public name", "bio": "A short public bio."}
	mustStatus(t, owner.request("PATCH", "me/profile", body, 0, ""), 428)
	mustStatus(t, owner.request("PATCH", "me/profile", body, 1, ""), 200)
	mustStatus(t, owner.request("PATCH", "me/profile", body, 1, ""), 412)
	me := owner.request("GET", "me", nil, 0, "")
	if !strings.Contains(me.Body.String(), "A chosen public name") {
		t.Fatal("profile update not persisted")
	}
	body["handle"] = "ananya"
	mustStatus(t, owner.request("PATCH", "me/profile", body, 2, ""), 409)
	body["handle"] = "bad handle"
	mustStatus(t, owner.request("PATCH", "me/profile", body, 2, ""), 422)
	body["handle"] = "valid_name"
	body["roles"] = []string{"COORDINATOR"}
	mustStatus(t, owner.request("PATCH", "me/profile", body, 2, ""), 400)
}

func TestSessionLimitRevokesAndAuditsOldest(t *testing.T) {
	base := testApp(t)
	cfg := base.Config
	cfg.AuthMode = "oidc"
	cfg.OIDCIssuer = "https://identity.example.test"
	a := cloneTestApp(t, cfg)
	identity := authn.Identity{Issuer: cfg.OIDCIssuer, Subject: uuid.NewString()}
	var oldest, newest string
	for i := 0; i < 25; i++ {
		token, err := randomSecret()
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			oldest = token
		}
		newest = token
		if err = a.provisionSession(context.Background(), identity, token, nil); err != nil {
			t.Fatal(err)
		}
	}
	old := client{app: a, cookie: &http.Cookie{Name: "jansetu_session", Value: oldest}}
	current := client{app: a, cookie: &http.Cookie{Name: "jansetu_session", Value: newest}}
	mustStatus(t, old.request("GET", "me", nil, 0, ""), 401)
	mustStatus(t, current.request("GET", "me", nil, 0, ""), 200)
	listed := current.request("GET", "me/sessions", nil, 0, "")
	mustStatus(t, listed, 200)
	if len(parsed[struct{ Items []accountSession }](t, listed).Items) != 20 {
		t.Fatal("session cap not enforced")
	}
	var audits int
	err := integrationAdmin.QueryRow(context.Background(), `SELECT count(*) FROM infra.audit_event e JOIN identity.account_binding b ON b.principal_id=e.actor_ref WHERE b.provider=$1 AND b.provider_subject=$2 AND e.action='SESSION_LIMIT_REVOKED'`, identity.Issuer, identity.Subject).Scan(&audits)
	if err != nil || audits != 5 {
		t.Fatal("session limit revocations were not audited", err, audits)
	}
}

// Package authn verifies provider identity. Application permissions never come
// from provider roles, email, names, or other mutable claims.
package authn

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/lavkushry/JanSetu-AI/services/backend/internal/platform"
	"golang.org/x/oauth2"
)

type Provider struct {
	issuer   string
	oauth    oauth2.Config
	verifier *oidc.IDTokenVerifier
	client   *http.Client
}

// This transport only substitutes the configured issuer's origin for local
// container routing. Discovery and JWT issuer verification still use the public URL.
type localTransport struct {
	base         http.RoundTripper
	origin, back *url.URL
}

func (t localTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Scheme != t.origin.Scheme || r.URL.Host != t.origin.Host {
		return nil, errors.New("OIDC endpoint outside configured origin")
	}
	c := r.Clone(r.Context())
	u := *r.URL
	u.Scheme, u.Host = t.back.Scheme, t.back.Host
	c.URL = &u
	c.Host = t.origin.Host
	return t.base.RoundTrip(c)
}

func Discover(ctx context.Context, c platform.Config) (*Provider, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	client := &http.Client{Timeout: 8 * time.Second, Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	origin, _ := url.Parse(c.OIDCIssuer)
	if c.OIDCBackchannel != "" {
		back, _ := url.Parse(c.OIDCBackchannel)
		client.Transport = localTransport{base: transport, origin: origin, back: back}
	}
	p, err := oidc.NewProvider(oidc.ClientContext(ctx, client), c.OIDCIssuer)
	if err != nil {
		return nil, err
	}
	var discovery struct {
		JWKS string `json:"jwks_uri"`
	}
	if err = p.Claims(&discovery); err != nil {
		return nil, err
	}
	e := p.Endpoint()
	for _, endpoint := range []string{e.AuthURL, e.TokenURL, discovery.JWKS} {
		u, err := url.Parse(endpoint)
		if err != nil || u.Scheme != origin.Scheme || u.Host != origin.Host || u.User != nil || u.Fragment != "" {
			return nil, errors.New("OIDC discovery endpoint outside configured origin")
		}
	}
	if c.OIDCClientSecret == "" {
		e.AuthStyle = oauth2.AuthStyleInParams
	}
	return &Provider{issuer: c.OIDCIssuer, client: client,
		oauth: oauth2.Config{ClientID: c.OIDCClientID, ClientSecret: c.OIDCClientSecret, Endpoint: e,
			RedirectURL: strings.TrimSuffix(c.WebOrigin, "/") + "/api/auth/callback", Scopes: []string{oidc.ScopeOpenID}},
		verifier: p.VerifierContext(oidc.ClientContext(context.Background(), client), &oidc.Config{ClientID: c.OIDCClientID, SupportedSigningAlgs: []string{oidc.RS256}})}, nil
}

func (p *Provider) AuthorizationURL(state, nonce, verifier string) string {
	return p.oauth.AuthCodeURL(state, oidc.Nonce(nonce), oauth2.S256ChallengeOption(verifier),
		oauth2.SetAuthURLParam("prompt", "login"), oauth2.SetAuthURLParam("response_mode", "query"))
}

type Identity struct{ Issuer, Subject string }

func (p *Provider) Exchange(ctx context.Context, code, verifier string, nonceHash []byte) (Identity, error) {
	token, err := p.oauth.Exchange(oidc.ClientContext(ctx, p.client), code, oauth2.VerifierOption(verifier))
	if err != nil {
		return Identity{}, err
	}
	raw, ok := token.Extra("id_token").(string)
	if !ok {
		return Identity{}, errors.New("missing ID token")
	}
	id, err := p.verifier.Verify(ctx, raw)
	if err != nil {
		return Identity{}, err
	}
	hash := sha256.Sum256([]byte(id.Nonce))
	if id.Subject == "" || len(id.Subject) > 255 || strings.TrimSpace(id.Subject) != id.Subject ||
		id.Nonce == "" || subtle.ConstantTimeCompare(hash[:], nonceHash) != 1 {
		return Identity{}, errors.New("invalid OIDC subject or nonce")
	}
	var claims struct {
		AuthorizedParty string `json:"azp"`
	}
	if err = id.Claims(&claims); err != nil {
		return Identity{}, err
	}
	if (len(id.Audience) > 1 && claims.AuthorizedParty != p.oauth.ClientID) ||
		(claims.AuthorizedParty != "" && claims.AuthorizedParty != p.oauth.ClientID) {
		return Identity{}, errors.New("invalid authorized party")
	}
	// Tokens and claims are discarded. The browser receives an opaque app session.
	return Identity{Issuer: p.issuer, Subject: id.Subject}, nil
}

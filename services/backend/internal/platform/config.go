package platform

import (
	"context"
	"errors"
	"net/url"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Config struct {
	VisionBinary                                                                                                         string
	MediaURL, MediaWorkerURL, MediaDir, OCRBinary                                                                        string
	Environment, DatabaseURL, SocialURL, OperationsURL, PublicationURL, WorkerURL, VaultURL, VaultToken, Addr, WebOrigin string
	AuthMode, OIDCIssuer, OIDCClientID, OIDCClientSecret, OIDCBackchannel                                                string
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func Load() (Config, error) {
	c := Config{
		VisionBinary:     os.Getenv("JANSETU_VISION_BINARY"),
		MediaURL:         env("JANSETU_MEDIA_DATABASE_URL", "postgres://js_media:js_media-local@localhost:5438/jansetu?sslmode=disable"),
		MediaWorkerURL:   env("JANSETU_MEDIA_WORKER_DATABASE_URL", "postgres://js_media_worker:js_media_worker-local@localhost:5438/jansetu?sslmode=disable"),
		MediaDir:         env("JANSETU_MEDIA_DIR", "/tmp/jansetu-media"),
		OCRBinary:        env("JANSETU_OCR_BINARY", "tesseract"),
		Environment:      env("JANSETU_ENV", "local"),
		DatabaseURL:      env("JANSETU_DATABASE_URL", "postgres://js_auth:js_auth-local@localhost:5438/jansetu?sslmode=disable"),
		SocialURL:        env("JANSETU_SOCIAL_DATABASE_URL", "postgres://js_social:js_social-local@localhost:5438/jansetu?sslmode=disable"),
		OperationsURL:    env("JANSETU_OPERATIONS_DATABASE_URL", "postgres://js_ops:js_ops-local@localhost:5438/jansetu?sslmode=disable"),
		PublicationURL:   env("JANSETU_PUBLICATION_DATABASE_URL", "postgres://js_publication:js_publication-local@localhost:5438/jansetu?sslmode=disable"),
		WorkerURL:        env("JANSETU_WORKER_DATABASE_URL", "postgres://js_worker:js_worker-local@localhost:5438/jansetu?sslmode=disable"),
		VaultURL:         env("JANSETU_VAULT_SERVICE_URL", "http://127.0.0.1:8082"),
		VaultToken:       env("JANSETU_VAULT_SERVICE_TOKEN", "local-vault-service-token-fictional-2026"),
		Addr:             env("JANSETU_HTTP_ADDR", "127.0.0.1:8081"),
		WebOrigin:        env("JANSETU_WEB_ORIGIN", "http://localhost:3100"),
		AuthMode:         env("JANSETU_AUTH_MODE", "oidc"),
		OIDCIssuer:       env("JANSETU_OIDC_ISSUER", "http://localhost:8180/realms/jansetu"),
		OIDCClientID:     env("JANSETU_OIDC_CLIENT_ID", "jansetu-web"),
		OIDCClientSecret: os.Getenv("JANSETU_OIDC_CLIENT_SECRET"),
		OIDCBackchannel:  os.Getenv("JANSETU_OIDC_BACKCHANNEL"),
	}
	return c, c.Validate()
}

func (c Config) Validate() error {
	// OIDC is implemented; privacy isolation and launch gates still constrain this release.
	if c.Environment != "local" && c.Environment != "test" {
		return errors.New("this release requires local/test mode; complete privacy isolation and launch gates before enabling real intake")
	}
	if c.AuthMode != "demo" && c.AuthMode != "oidc" {
		return errors.New("JANSETU_AUTH_MODE must be oidc or explicitly demo in local/test mode")
	}
	u, err := url.Parse(c.WebOrigin)
	if err != nil || !validOrigin(u) {
		return errors.New("JANSETU_WEB_ORIGIN must be an exact HTTP(S) origin")
	}
	if c.AuthMode == "oidc" {
		issuer, err := url.Parse(c.OIDCIssuer)
		if err != nil || issuer.Host == "" || issuer.User != nil || issuer.RawQuery != "" || issuer.Fragment != "" ||
			(issuer.Scheme != "https" && !(issuer.Scheme == "http" && loopback(issuer.Hostname()))) ||
			c.OIDCClientID == "" {
			return errors.New("OIDC requires an exact HTTPS issuer (loopback HTTP for local tests) and client ID")
		}
		if c.OIDCBackchannel != "" {
			back, err := url.Parse(c.OIDCBackchannel)
			if err != nil || !validOrigin(back) || back.Scheme != "http" || issuer.Scheme != "http" {
				return errors.New("OIDC backchannel override is only for local HTTP container networking")
			}
		}
	}
	return nil
}

func loopback(host string) bool { return host == "localhost" || host == "127.0.0.1" || host == "::1" }
func validOrigin(u *url.URL) bool {
	return u != nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" && u.User == nil &&
		u.Path == "" && u.RawQuery == "" && u.Fragment == ""
}

func (c Config) SecureCookies() bool {
	u, err := url.Parse(c.WebOrigin)
	return err == nil && u.Scheme == "https"
}

func Pool(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	c, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	c.MaxConns = 15
	c.MinConns = 1
	c.MaxConnIdleTime = 5 * time.Minute
	p, err := pgxpool.NewWithConfig(ctx, c)
	if err != nil {
		return nil, err
	}
	if err = p.Ping(ctx); err != nil {
		p.Close()
		return nil, err
	}
	return p, nil
}

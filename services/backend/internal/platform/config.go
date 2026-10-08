package platform

import (
	"context"
	"errors"
	"net/url"
	"os"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lavkushry/JanSetu-AI/services/backend/internal/recommendation"
	"github.com/redis/go-redis/v9"
)

type Config struct {
	RecommendationTarget                string
	RecommendationTLS                   recommendation.TLSConfig
	RecommendationMode                  string
	RecommendationRollout               int
	RecommendationSnapshotRedisURL      string
	RecommendationFeatureShadowRedisURL string

	VisionBinary, PotholeBinary                                                                                          string
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
		RecommendationTarget: env("JANSETU_RECOMMENDATION_TARGET", "127.0.0.1:50051"),
		RecommendationTLS: recommendation.TLSConfig{
			CAFile:     os.Getenv("JANSETU_RECOMMENDATION_TLS_CA_FILE"),
			CertFile:   os.Getenv("JANSETU_RECOMMENDATION_TLS_CERT_FILE"),
			KeyFile:    os.Getenv("JANSETU_RECOMMENDATION_TLS_KEY_FILE"),
			ServerName: os.Getenv("JANSETU_RECOMMENDATION_TLS_SERVER_NAME"),
		},
		RecommendationMode:                  env("JANSETU_RECOMMENDATION_MODE", "shadow"),
		RecommendationSnapshotRedisURL:      os.Getenv("JANSETU_RECOMMENDATION_SNAPSHOT_REDIS_URL"),
		RecommendationFeatureShadowRedisURL: os.Getenv("JANSETU_RECOMMENDATION_FEATURE_SHADOW_REDIS_URL"),
		VisionBinary:                        os.Getenv("JANSETU_VISION_BINARY"),
		PotholeBinary:                       os.Getenv("JANSETU_POTHOLE_BINARY"),
		MediaURL:                            env("JANSETU_MEDIA_DATABASE_URL", "postgres://js_media:js_media-local@localhost:5438/jansetu?sslmode=disable"),
		MediaWorkerURL:                      env("JANSETU_MEDIA_WORKER_DATABASE_URL", "postgres://js_media_worker:js_media_worker-local@localhost:5438/jansetu?sslmode=disable"),
		MediaDir:                            env("JANSETU_MEDIA_DIR", "/tmp/jansetu-media"),
		OCRBinary:                           env("JANSETU_OCR_BINARY", "tesseract"),
		Environment:                         env("JANSETU_ENV", "local"),
		DatabaseURL:                         env("JANSETU_DATABASE_URL", "postgres://js_auth:js_auth-local@localhost:5438/jansetu?sslmode=disable"),
		SocialURL:                           env("JANSETU_SOCIAL_DATABASE_URL", "postgres://js_social:js_social-local@localhost:5438/jansetu?sslmode=disable"),
		OperationsURL:                       env("JANSETU_OPERATIONS_DATABASE_URL", "postgres://js_ops:js_ops-local@localhost:5438/jansetu?sslmode=disable"),
		PublicationURL:                      env("JANSETU_PUBLICATION_DATABASE_URL", "postgres://js_publication:js_publication-local@localhost:5438/jansetu?sslmode=disable"),
		WorkerURL:                           env("JANSETU_WORKER_DATABASE_URL", "postgres://js_worker:js_worker-local@localhost:5438/jansetu?sslmode=disable"),
		VaultURL:                            env("JANSETU_VAULT_SERVICE_URL", "http://127.0.0.1:8082"),
		VaultToken:                          env("JANSETU_VAULT_SERVICE_TOKEN", "local-vault-service-token-fictional-2026"),
		Addr:                                env("JANSETU_HTTP_ADDR", "127.0.0.1:8081"),
		WebOrigin:                           env("JANSETU_WEB_ORIGIN", "http://localhost:3100"),
		AuthMode:                            env("JANSETU_AUTH_MODE", "oidc"),
		OIDCIssuer:                          env("JANSETU_OIDC_ISSUER", "http://localhost:8180/realms/jansetu"),
		OIDCClientID:                        env("JANSETU_OIDC_CLIENT_ID", "jansetu-web"),
		OIDCClientSecret:                    os.Getenv("JANSETU_OIDC_CLIENT_SECRET"),
		OIDCBackchannel:                     os.Getenv("JANSETU_OIDC_BACKCHANNEL"),
	}
	rollout, err := strconv.Atoi(env("JANSETU_RECOMMENDATION_ROLLOUT", "0"))
	if err != nil {
		return c, errors.New("invalid recommendation rollout")
	}
	c.RecommendationRollout = rollout
	return c, c.Validate()
}

func (c Config) Validate() error {
	if c.RecommendationFeatureShadowRedisURL != "" {
		if _, err := redis.ParseURL(c.RecommendationFeatureShadowRedisURL); err != nil {
			return errors.New("invalid recommendation feature shadow Redis URL")
		}
	}
	if err := c.RecommendationTLS.Validate(); err != nil {
		return err
	}
	if c.RecommendationSnapshotRedisURL != "" {
		if _, err := redis.ParseURL(c.RecommendationSnapshotRedisURL); err != nil {
			return errors.New("invalid recommendation snapshot Redis URL")
		}
	}
	if c.RecommendationMode != "" && c.RecommendationMode != "off" && c.RecommendationMode != "shadow" && c.RecommendationMode != "serve" {
		return errors.New("recommendation mode must be off, shadow, or serve")
	}
	if c.RecommendationRollout < 0 || c.RecommendationRollout > 100 {
		return errors.New("recommendation rollout must be 0..100")
	}
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

package platform

import (
	"context"
	"errors"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Config struct {
	Environment, DatabaseURL, VaultURL, Addr, WebOrigin string
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func Load() (Config, error) {
	c := Config{
		Environment: env("JANSETU_ENV", "local"),
		DatabaseURL: env("JANSETU_DATABASE_URL", "postgres://jansetu:jansetu-local@localhost:5438/jansetu?sslmode=disable"),
		VaultURL:    env("JANSETU_VAULT_URL", "postgres://jansetu:jansetu-local@localhost:5438/jansetu_vault?sslmode=disable"),
		Addr:        env("JANSETU_HTTP_ADDR", "127.0.0.1:8081"),
		WebOrigin:   env("JANSETU_WEB_ORIGIN", "http://localhost:3100"),
	}
	// The first milestone intentionally requires synthetic, local accounts.
	// A deployment cannot silently enable development identity in production.
	if c.Environment != "local" && c.Environment != "test" {
		return c, errors.New("this release requires local/test mode; configure production OIDC and launch gates before enabling real intake")
	}
	return c, nil
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

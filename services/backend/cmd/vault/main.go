package main

import (
	"context"
	"flag"
	"github.com/lavkushry/JanSetu-AI/services/backend/internal/platform"
	"github.com/lavkushry/JanSetu-AI/services/backend/internal/vault"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func setting(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
func main() {
	health := flag.Bool("healthcheck", false, "probe readiness")
	flag.Parse()
	c, err := platform.Load()
	if err != nil {
		slog.Error("Vault configuration unavailable")
		os.Exit(1)
	}
	if *health {
		client, err := vault.NewClient("http://127.0.0.1:8082", c.VaultToken)
		if err != nil {
			os.Exit(1)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if client.Ready(ctx) != nil {
			os.Exit(1)
		}
		return
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	keys, err := vault.LoadKeys(setting("JANSETU_VAULT_KEYS_FILE", "../../infra/vault/local-keys.json"))
	if err != nil {
		slog.Error("Vault key file unavailable")
		os.Exit(1)
	}
	db, err := platform.RuntimePool(ctx, setting("JANSETU_VAULT_DATABASE_URL", "postgres://js_vault:js_vault-local@localhost:5438/jansetu_vault?sslmode=disable"), "js_vault")
	if err != nil {
		slog.Error("Restricted vault database unavailable")
		os.Exit(1)
	}
	defer db.Close()
	if err = vault.VerifyKeys(ctx, db, keys); err != nil {
		slog.Error("Vault key binding unavailable; reviewed rotation required")
		os.Exit(1)
	}
	auth, err := platform.RuntimePool(ctx, setting("JANSETU_VAULT_AUTH_DATABASE_URL", "postgres://js_vault_auth:js_vault_auth-local@localhost:5438/jansetu?sslmode=disable"), "js_vault_auth")
	if err != nil {
		slog.Error("Vault session validation unavailable")
		os.Exit(1)
	}
	defer auth.Close()
	service := vault.Service{DB: db, Auth: auth, Keys: keys, Token: c.VaultToken, Mode: c.AuthMode, Issuer: c.OIDCIssuer}
	server := http.Server{Addr: setting("JANSETU_VAULT_HTTP_ADDR", "127.0.0.1:8082"), Handler: service.Handler(), ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 6 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 8192}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	slog.Info("Internal self-scope vault ready")
	if err = server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		slog.Error("Vault server unavailable")
		os.Exit(1)
	}
}

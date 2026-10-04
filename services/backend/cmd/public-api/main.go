package main

import (
	"context"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/lavkushry/JanSetu-AI/services/backend/internal/app"
	"github.com/lavkushry/JanSetu-AI/services/backend/internal/authn"
	"github.com/lavkushry/JanSetu-AI/services/backend/internal/platform"
)

func main() {
	health := flag.Bool("healthcheck", false, "probe readiness")
	flag.Parse()
	c, e := platform.Load()
	if e != nil {
		slog.Error(e.Error())
		os.Exit(1)
	}
	if *health {
		address := c.Addr
		if address[0] == ':' {
			address = "127.0.0.1" + address
		}
		client := http.Client{Timeout: 3 * time.Second}
		r, e := client.Get("http://" + address + "/health/ready")
		if e != nil {
			os.Exit(1)
		}
		defer r.Body.Close()
		if r.StatusCode != 200 {
			os.Exit(1)
		}
		return
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	db, e := platform.Pool(ctx, c.DatabaseURL)
	if e != nil {
		slog.Error("Application database unavailable")
		os.Exit(1)
	}
	defer db.Close()
	vault, e := platform.Pool(ctx, c.VaultURL)
	if e != nil {
		slog.Error("Vault database unavailable")
		os.Exit(1)
	}
	defer vault.Close()
	application := app.New(db, vault, c)
	if c.AuthMode == "oidc" {
		discoveryCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		application.Identity, e = authn.Discover(discoveryCtx, c)
		cancel()
		if e != nil {
			slog.Error("OIDC discovery failed; check the configured issuer and local provider")
			os.Exit(1)
		}
	}
	server := http.Server{Addr: c.Addr, Handler: application.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 20 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32768}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	slog.Info("JanSetu synthetic local API ready", "address", c.Addr, "authMode", c.AuthMode)
	if e = server.ListenAndServe(); e != nil && e != http.ErrServerClosed {
		slog.Error("API server failed")
		os.Exit(1)
	}
}

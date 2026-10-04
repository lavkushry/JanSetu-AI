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
	"github.com/lavkushry/JanSetu-AI/services/backend/internal/vault"
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
	social, e := platform.RuntimePool(ctx, c.SocialURL, "js_social")
	if e != nil {
		slog.Error("Restricted social database unavailable")
		os.Exit(1)
	}
	defer social.Close()
	auth, e := platform.RuntimePool(ctx, c.DatabaseURL, "js_auth")
	if e != nil {
		slog.Error("Restricted account database unavailable")
		os.Exit(1)
	}
	defer auth.Close()
	operations, e := platform.RuntimePool(ctx, c.OperationsURL, "js_ops")
	if e != nil {
		slog.Error("Restricted operations database unavailable")
		os.Exit(1)
	}
	defer operations.Close()
	publication, e := platform.RuntimePool(ctx, c.PublicationURL, "js_publication")
	if e != nil {
		slog.Error("Restricted publication database unavailable")
		os.Exit(1)
	}
	defer publication.Close()
	vaultClient, e := vault.NewClient(c.VaultURL, c.VaultToken)
	if e != nil {
		slog.Error("Configure internal vault service")
		os.Exit(1)
	}
	application := app.New(social, vaultClient, c)
	application.Auth = auth
	application.Operations = operations
	application.Publication = publication
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

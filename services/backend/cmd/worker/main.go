package main

import (
	"context"
	"github.com/lavkushry/JanSetu-AI/services/backend/internal/app"
	"github.com/lavkushry/JanSetu-AI/services/backend/internal/platform"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	c, e := platform.Load()
	if e != nil {
		slog.Error(e.Error())
		os.Exit(1)
	}
	db, e := platform.RuntimePool(ctx, c.WorkerURL, "js_worker")
	if e != nil {
		slog.Error("Application database unavailable")
		os.Exit(1)
	}
	defer db.Close()
	a := app.New(db, nil, c)
	slog.Info("Local projection worker ready")
	for ctx.Err() == nil {
		if e = a.RunWorker(ctx); e != nil {
			slog.Error("Projection failed; retrying with bounded event attempts")
		}
		select {
		case <-ctx.Done():
		case <-time.After(time.Second):
		}
	}
}

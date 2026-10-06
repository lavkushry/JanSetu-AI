package main

import (
	"context"
	"github.com/lavkushry/JanSetu-AI/services/backend/internal/media"
	"github.com/lavkushry/JanSetu-AI/services/backend/internal/platform"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	cfg, e := platform.Load()
	if e != nil {
		slog.Error(e.Error())
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	db, e := platform.RuntimePool(ctx, cfg.MediaWorkerURL, "js_media_worker")
	if e != nil {
		slog.Error("Restricted media database unavailable")
		os.Exit(1)
	}
	defer db.Close()
	files, e := media.NewStorage(cfg.MediaDir)
	if e != nil {
		slog.Error("Private media directory unavailable")
		os.Exit(1)
	}
	engine := media.OCR{Binary: cfg.OCRBinary}
	model, e := engine.Version(ctx)
	if e != nil {
		slog.Error("Tesseract OCR engine unavailable")
		os.Exit(1)
	}
	worker := &media.Worker{DB: db, Files: files, Engine: engine, Model: model}
	if cfg.VisionBinary != "" {
		worker.Vision = &media.Detector{Binary: cfg.VisionBinary}
		worker.VisionModel, e = worker.Vision.Version(ctx)
		if e != nil {
			slog.Error("Pinned image recognition engine unavailable")
			os.Exit(1)
		}
	}
	if cfg.PotholeBinary != "" {
		worker.Pothole = &media.Detector{Binary: cfg.PotholeBinary, Kind: "POTHOLE_DETECTION"}
		worker.PotholeModel, e = worker.Pothole.Version(ctx)
		if e != nil {
			slog.Error("Pinned pothole engine unavailable")
			os.Exit(1)
		}
	}
	slog.Info("Local private image worker ready", "potholeModel", worker.PotholeModel, "ocrModel", model, "visionModel", worker.VisionModel)
	worker.Run(ctx, func(e error) { slog.Error("Media processing failed") })
}

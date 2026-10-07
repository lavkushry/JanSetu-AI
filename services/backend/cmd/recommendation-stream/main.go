package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/lavkushry/JanSetu-AI/services/backend/internal/platform"
	"github.com/lavkushry/JanSetu-AI/services/backend/internal/recommendation/stream"
	"github.com/redis/go-redis/v9"
	"github.com/twmb/franz-go/pkg/kgo"
)

func setting(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func main() {
	mode := flag.String("mode", "publish", "publish or project")
	flag.Parse()
	if *mode != "publish" && *mode != "project" {
		slog.Error("mode must be publish or project")
		os.Exit(1)
	}
	if env := setting("JANSETU_ENV", "local"); env != "local" && env != "test" {
		slog.Error("stream release requires local/test mode")
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	db, err := platform.RuntimePool(ctx, setting("JANSETU_RECOMMENDATION_STREAM_DATABASE_URL", "postgres://js_recommendation_stream:js_recommendation_stream-local@localhost:5438/jansetu?sslmode=disable"), "js_recommendation_stream")
	if err != nil {
		slog.Error("Recommendation stream database unavailable")
		os.Exit(1)
	}
	defer db.Close()
	opts := []kgo.Opt{kgo.SeedBrokers(strings.Split(setting("JANSETU_RECOMMENDATION_KAFKA_BROKERS", "127.0.0.1:19092"), ",")...), kgo.ClientID("jansetu-recommendation-" + *mode)}
	if *mode == "project" {
		opts = append(opts, kgo.ConsumeTopics(stream.Topic), kgo.ConsumerGroup(setting("JANSETU_RECOMMENDATION_CONSUMER_GROUP", "recommendation-features-v1")), kgo.DisableAutoCommit(), kgo.BlockRebalanceOnPoll(), kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()))
	} else {
		opts = append(opts, kgo.RequiredAcks(kgo.AllISRAcks()), kgo.RecordDeliveryTimeout(time.Second), kgo.MaxBufferedRecords(100))
	}
	client, err := kgo.NewClient(opts...)
	if err != nil {
		slog.Error("Recommendation Kafka configuration invalid")
		os.Exit(1)
	}
	defer client.Close()
	if *mode == "project" {
		redisOpts, err := redis.ParseURL(setting("JANSETU_RECOMMENDATION_REDIS_URL", "redis://127.0.0.1:16379/0"))
		if err != nil {
			slog.Error("Recommendation Redis configuration invalid")
			os.Exit(1)
		}
		cache := redis.NewClient(redisOpts)
		defer cache.Close()
		err = stream.Consume(ctx, client, db, stream.Projection{Redis: cache, Namespace: "jansetu:rec:v1"})
		if err != nil && ctx.Err() == nil {
			slog.Error("Recommendation projection stopped; offsets retained for replay")
			os.Exit(1)
		}
		return
	}
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	cleanup := time.NewTicker(time.Minute)
	defer cleanup.Stop()
	for ctx.Err() == nil {
		n, err := stream.PublishBatch(ctx, db, stream.KafkaProducer{Client: client})
		if err != nil && ctx.Err() == nil {
			slog.Error("Recommendation publish failed; retry scheduled")
		}
		if n > 0 {
			slog.Info("Recommendation events delivered", "count", n)
		}
		select {
		case <-ctx.Done():
		case <-ticker.C:
		case <-cleanup.C:
			if _, err := db.Exec(ctx, "SELECT rec_stream.expire()"); err != nil && ctx.Err() == nil {
				slog.Error("Recommendation stream cleanup failed")
			}
		}
	}
}

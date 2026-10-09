package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// This tracer is installed only on the opt-in benchmark's own social pool.
// Labels are fixed: SQL, arguments, identities and error messages are never kept.
type benchmarkQueryTracer struct {
	mu         sync.Mutex
	recording  bool
	generation int
	samples    map[string][]float64
	failures   map[string]int
}

type benchmarkQueryKey struct{}
type benchmarkQueryStart struct {
	label      string
	started    time.Time
	generation int
}

func benchmarkQueryLabel(sql string) string {
	switch {
	case sql == recommendationCandidates:
		return "candidateRetrieval"
	case strings.HasPrefix(sql, "-- name: RecommendationPosts :many\n"):
		return "postHydration"
	case sql == "SELECT authz.lock_principal($1)":
		return "principalLock"
	case sql == "SELECT state FROM social.profile WHERE id=$1 AND id=authz.current_profile() FOR NO KEY UPDATE":
		return "profileLock"
	case sql == "SELECT personalization_enabled,interests,languages,locality,generation,version FROM social.recommendation_preference WHERE profile_id=$1":
		return "preferenceRead"
	case sql == "SELECT disabled,version FROM rec_serving.current_control()":
		return "servingControl"
	case strings.HasPrefix(sql, "SELECT payload,expires_at,generation FROM social.recommendation_snapshot WHERE "):
		return "snapshotRead"
	case strings.HasPrefix(sql, "INSERT INTO social.recommendation_snapshot("):
		return "snapshotWrite"
	case strings.HasPrefix(sql, "INSERT INTO social.recommendation_exposure("):
		return "exposureWrite"
	default:
		return "other"
	}
}

func (q *benchmarkQueryTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	q.mu.Lock()
	active, generation := q.recording, q.generation
	q.mu.Unlock()
	if !active {
		return ctx
	}
	return context.WithValue(ctx, benchmarkQueryKey{}, benchmarkQueryStart{
		label: benchmarkQueryLabel(data.SQL), started: time.Now(), generation: generation,
	})
}

func (q *benchmarkQueryTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	start, ok := ctx.Value(benchmarkQueryKey{}).(benchmarkQueryStart)
	if !ok {
		return
	}
	elapsed := float64(time.Since(start.started).Nanoseconds()) / 1e6
	q.mu.Lock()
	defer q.mu.Unlock()
	if !q.recording || start.generation != q.generation {
		return
	}
	q.samples[start.label] = append(q.samples[start.label], elapsed)
	if data.Err != nil {
		q.failures[start.label]++
	}
}

func (q *benchmarkQueryTracer) start() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.generation++
	q.samples, q.failures = map[string][]float64{}, map[string]int{}
	q.recording = true
}

func (q *benchmarkQueryTracer) finish() map[string]any {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.recording = false
	groups := map[string]any{}
	for label, samples := range q.samples {
		total := 0.0
		for _, value := range samples {
			total += value
		}
		groups[label] = map[string]any{
			"calls": len(samples), "failedCalls": q.failures[label], "totalMs": total,
			"latency": benchmarkPercentiles(samples),
		}
	}
	return map[string]any{
		"enabled": true, "pool": "social", "scope": "measured HTTP workers only; pgx query start through Exec completion or Rows close, including row iteration; excludes pool acquisition",
		"groups": groups,
	}
}

func benchmarkTracedPool(t *testing.T, source *pgxpool.Pool, tracer *benchmarkQueryTracer) *pgxpool.Pool {
	t.Helper()
	cfg := source.Config().Copy()
	if cfg.ConnConfig.Tracer != nil {
		t.Fatal("benchmark cannot replace an existing query tracer")
	}
	cfg.ConnConfig.Tracer = tracer
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func TestBenchmarkQueryTracerPrivacyAndIntervals(t *testing.T) {
	q := &benchmarkQueryTracer{}
	ctx := context.Background()
	query := pgx.TraceQueryStartData{SQL: "SELECT 'private sql sentinel'", Args: []any{"private argument sentinel"}}
	off := q.TraceQueryStart(ctx, nil, query)
	q.TraceQueryEnd(off, nil, pgx.TraceQueryEndData{})
	q.start()
	stale := q.TraceQueryStart(ctx, nil, query)
	q.start() // A previous interval must never leak into the current one.
	q.TraceQueryEnd(stale, nil, pgx.TraceQueryEndData{})
	current := q.TraceQueryStart(ctx, nil, query)
	q.TraceQueryEnd(current, nil, pgx.TraceQueryEndData{Err: errors.New("private error sentinel")})
	hydration := q.TraceQueryStart(ctx, nil, pgx.TraceQueryStartData{SQL: "-- name: RecommendationPosts :many\nSELECT 'private body sentinel'"})
	q.TraceQueryEnd(hydration, nil, pgx.TraceQueryEndData{})
	result := q.finish()
	groups := result["groups"].(map[string]any)
	other := groups["other"].(map[string]any)
	if len(groups) != 2 || other["calls"] != 1 || other["failedCalls"] != 1 || groups["postHydration"].(map[string]any)["calls"] != 1 {
		t.Fatalf("wrong interval or classification: %v", groups)
	}
	data, err := json.Marshal(result)
	if err != nil || strings.Contains(string(data), "private") || strings.Contains(string(data), "SELECT") {
		t.Fatalf("diagnostic leaked query data: %s (%v)", data, err)
	}
	q.TraceQueryEnd(current, nil, pgx.TraceQueryEndData{})
	if len(q.samples["other"]) != 1 {
		t.Fatal("query completed after measurement was included")
	}
}

func TestBenchmarkQueryTracerConcurrent(t *testing.T) {
	q := &benchmarkQueryTracer{}
	q.start()
	var workers sync.WaitGroup
	for range 8 {
		workers.Go(func() {
			for range 100 {
				ctx := q.TraceQueryStart(context.Background(), nil, pgx.TraceQueryStartData{SQL: recommendationCandidates})
				q.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{})
			}
		})
	}
	workers.Wait()
	groups := q.finish()["groups"].(map[string]any)
	if groups["candidateRetrieval"].(map[string]any)["calls"] != 800 {
		t.Fatal("concurrent queries were lost")
	}
}

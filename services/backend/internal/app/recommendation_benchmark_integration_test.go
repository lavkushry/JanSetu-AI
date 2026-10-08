package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"runtime"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/lavkushry/JanSetu-AI/services/backend/internal/recommendation"
	"github.com/lavkushry/JanSetu-AI/services/backend/internal/recommendation/pb"
)

type measuredRanker struct {
	recommendation.Ranker
	mu         sync.Mutex
	latencies  []float64
	errors     int
	candidates int
}

func (r *measuredRanker) Recommend(ctx context.Context, request *pb.RecommendRequest) (*pb.RecommendResponse, error) {
	start := time.Now()
	response, err := r.Ranker.Recommend(ctx, request)
	r.mu.Lock()
	r.latencies = append(r.latencies, float64(time.Since(start).Microseconds())/1000)
	r.candidates = max(r.candidates, len(request.Candidates))
	if err != nil {
		r.errors++
	}
	r.mu.Unlock()
	return response, err
}

func benchmarkNumber(t *testing.T, name string, defaultValue, maximum int) int {
	t.Helper()
	raw := os.Getenv(name)
	if raw == "" {
		return defaultValue
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || n > maximum {
		t.Fatalf("%s must be between 1 and %d", name, maximum)
	}
	return n
}

func benchmarkPercentiles(values []float64) map[string]float64 {
	result := map[string]float64{}
	if len(values) == 0 {
		return result
	}
	ordered := append([]float64{}, values...)
	sort.Float64s(ordered)
	for _, p := range []int{50, 95, 99} {
		result[fmt.Sprintf("p%dMs", p)] = ordered[int(math.Ceil(float64(p)*float64(len(ordered))/100))-1]
	}
	return result
}

// An opt-in HTTP smoke workload. TestMain creates and removes fresh application
// and vault databases; this harness cannot point requests at a deployed API.
func TestRecommendationAPIBenchmark(t *testing.T) {
	output := os.Getenv("JANSETU_RECOMMENDATION_API_BENCHMARK_OUTPUT")
	if output == "" {
		t.Skip("run make recommendation-api-benchmark for isolated complete-feed smoke")
	}
	base := testApp(t)
	target := os.Getenv("JANSETU_RECOMMENDATION_TEST_TARGET")
	if target == "" {
		t.Fatal("isolated Rust target required")
	}
	requests := benchmarkNumber(t, "JANSETU_BENCHMARK_REQUESTS", 500, 100000)
	concurrency := benchmarkNumber(t, "JANSETU_BENCHMARK_CONCURRENCY", 8, 128)
	authors := benchmarkNumber(t, "JANSETU_BENCHMARK_AUTHORS", 128, 1024)
	perAuthor := benchmarkNumber(t, "JANSETU_BENCHMARK_POSTS_PER_AUTHOR", 8, 128)
	cfg := base.Config
	cfg.RecommendationTarget = ""
	cfg.RecommendationMode, cfg.RecommendationRollout = "serve", 100
	cfg.RecommendationSnapshotRedisURL, cfg.RecommendationFeatureShadowRedisURL = "", ""
	a := cloneTestApp(t, cfg)
	rpc, err := recommendation.New(target, recommendation.TLSConfig{})
	if err != nil {
		t.Fatal(err)
	}
	defer rpc.Close()
	ranker := &measuredRanker{Ranker: rpc}
	a.Ranker = ranker
	clients := []client{login(t, a, 0), login(t, a, 1), login(t, a, 2)}
	viewers := []uuid.UUID{myProfileID(t, clients[0]), myProfileID(t, clients[1]), myProfileID(t, clients[2])}
	for i, c := range clients {
		consentRecommendation(t, c, i != 2)
	}
	denied, digest, published := benchmarkFixture(t, authors, perAuthor, viewers)
	server := httptest.NewServer(a.Handler())
	defer server.Close()
	transport := &http.Transport{MaxIdleConns: concurrency, MaxIdleConnsPerHost: concurrency, MaxConnsPerHost: concurrency}
	defer transport.CloseIdleConnections()
	httpClient := &http.Client{Transport: transport, Timeout: 10 * time.Second}
	logger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer slog.SetDefault(logger)
	type sample struct {
		Latency      float64
		Status       int
		Bytes        int
		Mode         string
		Error        string
		Cursor       string
		Continuation bool
		Consenting   bool
	}
	fetch := func(viewer int, cursor string) sample {
		path := server.URL + "/v1/feed?sort=recommended"
		if cursor != "" {
			path += "&cursor=" + url.QueryEscape(cursor)
		}
		request, _ := http.NewRequest("GET", path, nil)
		request.AddCookie(clients[viewer].cookie)
		start := time.Now()
		response, err := httpClient.Do(request)
		s := sample{Continuation: cursor != "", Consenting: viewer != 2}
		if err != nil {
			s.Latency, s.Error = float64(time.Since(start).Microseconds())/1000, "transport"
			return s
		}
		body, readErr := io.ReadAll(io.LimitReader(response.Body, 1024*1024+1))
		response.Body.Close()
		s.Latency, s.Status, s.Bytes = float64(time.Since(start).Microseconds())/1000, response.StatusCode, len(body)
		if readErr != nil || len(body) > 1024*1024 {
			s.Error = "response_read"
			return s
		}
		if s.Status != 200 {
			s.Error = "http_status"
			return s
		}
		var page recommendationPage
		if json.Unmarshal(body, &page) != nil || len(page.Items) == 0 || len(page.Items) > 20 {
			s.Error = "invalid_page"
			return s
		}
		s.Mode = page.RecommendationMode
		seen := map[uuid.UUID]bool{}
		authorCount := map[uuid.UUID]int{}
		for _, item := range page.Items {
			if item.Type != "POST" {
				continue
			}
			if item.Post.Author == nil || item.Post.ID == uuid.Nil || denied[item.Post.ID] || seen[item.Post.ID] {
				s.Error = "unauthorized_or_duplicate_post"
				return s
			}
			seen[item.Post.ID] = true
			authorCount[item.Post.Author.ID]++
			if authorCount[item.Post.Author.ID] > 2 {
				s.Error = "author_cap"
				return s
			}
			if (item.Recommendation.ExposureID != uuid.Nil) != s.Consenting {
				s.Error = "exposure_consent"
				return s
			}
		}
		if page.NextCursor != nil {
			s.Cursor = *page.NextCursor
		}
		return s
	}
	initial := make([]string, len(clients))
	for viewer := range clients {
		s := fetch(viewer, "")
		if s.Error != "" || s.Mode != "ranked" || s.Cursor == "" {
			t.Fatal("warmup failed", s.Error, s.Status, s.Mode)
		}
		initial[viewer] = s.Cursor
	}
	ranker.mu.Lock()
	ranker.latencies, ranker.errors, ranker.candidates = nil, 0, 0
	ranker.mu.Unlock()
	results := make([]sample, requests)
	var next atomic.Int64
	var workers sync.WaitGroup
	gate := make(chan struct{})
	for worker := 0; worker < concurrency; worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			cursors := append([]string{}, initial...)
			<-gate
			for {
				i := int(next.Add(1) - 1)
				if i >= requests {
					return
				}
				// Deterministic 30% nonconsenting mix, spread across request types.
				viewer := i % 2
				if (i+i/10)%10 < 3 {
					viewer = 2
				}
				cursor := ""
				if i%5 == 4 {
					cursor = cursors[viewer]
				}
				s := fetch(viewer, cursor)
				results[i] = s
				if cursor == "" && s.Error == "" {
					cursors[viewer] = s.Cursor
				}
			}
		}()
	}
	started := time.Now()
	close(gate)
	workers.Wait()
	elapsed := time.Since(started).Seconds()
	latencies := make([]float64, 0, requests)
	firstLatency, continuationLatency, consentedLatency, coldLatency := []float64{}, []float64{}, []float64{}, []float64{}
	errors, modes, statuses := map[string]int{}, map[string]int{}, map[string]int{}
	success, continuations, nonconsenting, bytes := 0, 0, 0, 0
	for _, s := range results {
		latencies = append(latencies, s.Latency)
		statuses[strconv.Itoa(s.Status)]++
		bytes += s.Bytes
		if s.Continuation {
			continuations++
			continuationLatency = append(continuationLatency, s.Latency)
		} else {
			firstLatency = append(firstLatency, s.Latency)
		}
		if !s.Consenting {
			nonconsenting++
			coldLatency = append(coldLatency, s.Latency)
		} else {
			consentedLatency = append(consentedLatency, s.Latency)
		}
		if s.Error != "" {
			errors[s.Error]++
		} else {
			success++
			modes[s.Mode]++
		}
	}
	var pgVersion string
	if err := integrationAdmin.QueryRow(context.Background(), "SELECT version()").Scan(&pgVersion); err != nil {
		t.Fatal(err)
	}
	stats := map[string]any{
		"fixture": "api-smoke-v1", "datasetSHA256": digest, "authors": authors, "postsPerAuthor": perAuthor,
		"fixtureEligiblePosts": published, "fixtureExcludedPosts": len(denied), "seededViewers": 3,
		"requests": requests, "concurrency": concurrency, "warmupRequests": 3, "successfulFeeds": success,
		"errors": requests - success, "errorCounts": errors, "statusCounts": statuses, "modeCounts": modes,
		"continuations": continuations, "nonconsentingRequests": nonconsenting, "responseBytes": bytes,
		"elapsedSeconds": elapsed, "successfulRequestsPerSecond": float64(success) / elapsed,
		"allResponseLatency": benchmarkPercentiles(latencies),
		"firstPageLatency":   benchmarkPercentiles(firstLatency), "continuationLatency": benchmarkPercentiles(continuationLatency),
		"consentedLatency": benchmarkPercentiles(consentedLatency), "nonconsentingLatency": benchmarkPercentiles(coldLatency),
		"recommendationRPC": map[string]any{"calls": len(ranker.latencies), "errors": ranker.errors, "maximumCandidates": ranker.candidates, "latency": benchmarkPercentiles(ranker.latencies)},
		"measuredAt":        started.UTC().Format(time.RFC3339Nano), "goVersion": runtime.Version(), "goMaxProcs": runtime.GOMAXPROCS(0),
		"postgresVersion": pgVersion, "runtimeRole": "js_social", "socialPoolMaxConns": a.DB.Config().MaxConns,
		"apiReplicas": 1, "rustReplicas": 1, "tls": false, "snapshotCache": false, "behavioralShadow": false,
		"costPerThousandFeeds": nil, "costPerMillionEvents": nil,
		"completeFeedCapacityValidated": false, "qualityValidated": false,
		"scope": "closed-loop loopback HTTP Go API / restricted PostgreSQL / Rust RPC, synthetic three-viewer smoke; excludes BFF, production TLS, cache and background event workers",
	}
	data, err := json.MarshalIndent(stats, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(output, append(data, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	if len(errors) != 0 {
		t.Fatal("benchmark contained errors", errors)
	}
	t.Logf("%d successful complete feeds, %.2f requests/s; all-response latency %v", success, float64(success)/elapsed, stats["allResponseLatency"])
}

// Identity, language, body, state and age offsets are reproducible. Published
// times use a run anchor; that relative-time policy is part of the dataset hash.
func benchmarkFixture(t *testing.T, authors, perAuthor int, viewers []uuid.UUID) (map[uuid.UUID]bool, string, int) {
	t.Helper()
	denied := map[uuid.UUID]bool{}
	hash := sha256.New()
	published := 0
	ctx := context.Background()
	err := pgx.BeginFunc(ctx, integrationAdmin, func(tx pgx.Tx) error {
		for i := 0; i < authors; i++ {
			author := uuid.NewSHA1(uuid.NameSpaceOID, []byte(fmt.Sprintf("api-smoke-v1:author:%d", i)))
			if _, err := tx.Exec(ctx, `INSERT INTO social.profile(id,handle,display_name,state) VALUES($1,$2,'API benchmark fixture','ACTIVE')`, author, "bench_"+author.String()[:12]); err != nil {
				return err
			}
			for _, viewer := range viewers {
				if i == authors-1 {
					if _, err := tx.Exec(ctx, `INSERT INTO social.profile_block(blocker_id,blocked_id) VALUES($1,$2)`, viewer, author); err != nil {
						return err
					}
				} else if i%8 == 0 {
					if _, err := tx.Exec(ctx, `INSERT INTO social.profile_follow(follower_id,followed_id) VALUES($1,$2)`, viewer, author); err != nil {
						return err
					}
				}
			}
			for j := 0; j < perAuthor; j++ {
				ordinal := i*perAuthor + j
				post := uuid.NewSHA1(uuid.NameSpaceOID, []byte(fmt.Sprintf("api-smoke-v1:post:%d", ordinal)))
				state, language := "PUBLISHED", "en-IN"
				if ordinal%17 == 0 {
					state = "HIDDEN"
				}
				if ordinal%3 == 0 {
					language = "hi-IN"
				}
				body := fmt.Sprintf("Synthetic local discovery workload item %d by creator %d. Useful discussion for the API smoke fixture.", ordinal, i)
				fmt.Fprintf(hash, "%s|%s|%s|%s|%s|ageSeconds=%d\n", author, post, state, language, body, ordinal)
				if _, err := tx.Exec(ctx, `INSERT INTO social.post(id,author_id,kind,state,published_revision,published_at) VALUES($1,$2,'SHORT',$3,1,transaction_timestamp()-$4*interval '1 second')`, post, author, state, ordinal); err != nil {
					return err
				}
				if _, err := tx.Exec(ctx, `INSERT INTO social.post_revision(post_id,revision,body,language_tag,review_state) VALUES($1,1,$2,$3,'APPROVED')`, post, body, language); err != nil {
					return err
				}
				if state == "HIDDEN" || i == authors-1 {
					denied[post] = true
				} else {
					published++
				}
			}
		}
		_, err := tx.Exec(ctx, "ANALYZE social.post; ANALYZE social.post_revision; ANALYZE social.profile; ANALYZE social.profile_follow; ANALYZE social.profile_block")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return denied, hex.EncodeToString(hash.Sum(nil)), published
}

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
	"github.com/jackc/pgx/v5/pgxpool"
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

// Seed distinct accounts only inside TestMain's disposable databases. Requests
// still use the normal session authentication and restricted application roles.
func benchmarkViewers(t *testing.T, a *App, count int) ([]client, []uuid.UUID) {
	t.Helper()
	clients, profiles := make([]client, count), make([]uuid.UUID, count)
	ctx := context.Background()
	err := pgx.BeginFunc(ctx, integrationAdmin, func(tx pgx.Tx) error {
		for i := range clients {
			profile := uuid.NewSHA1(uuid.NameSpaceOID, []byte(fmt.Sprintf("api-smoke-v2:viewer:%d", i)))
			principal := uuid.NewSHA1(uuid.NameSpaceOID, []byte(fmt.Sprintf("api-smoke-v2:principal:%d", i)))
			token, err := randomSecret()
			if err != nil {
				return err
			}
			if _, err = tx.Exec(ctx, `INSERT INTO social.profile(id,handle,display_name,state) VALUES($1,$2,'API benchmark viewer','ACTIVE')`, profile, "viewer_"+profile.String()[:12]); err != nil {
				return err
			}
			if _, err = tx.Exec(ctx, `INSERT INTO identity.principal(id,profile_id,state) VALUES($1,$2,'ACTIVE')`, principal, profile); err != nil {
				return err
			}
			if _, err = tx.Exec(ctx, `INSERT INTO identity.session(id,principal_id,token_hash,expires_at) VALUES($1,$2,$3,now()+interval '12 hours')`, uuid.New(), principal, tokenHash(token)); err != nil {
				return err
			}
			clients[i] = client{app: a, cookie: &http.Cookie{Name: "jansetu_session", Value: token}}
			profiles[i] = profile
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for i, c := range clients {
		if myProfileID(t, c) != profiles[i] {
			t.Fatal("benchmark session authenticated as the wrong viewer")
		}
		consentRecommendation(t, c, i%3 != 2)
	}
	return clients, profiles
}

// Balance requests within each consent group without tying viewer selection to
// worker scheduling. Preserve the 70/30 mix across first and continuation pages.
func benchmarkViewerSchedule(viewers, requests int) []int {
	pools := [2][]int{}
	for viewer := 0; viewer < viewers; viewer++ {
		group := 0
		if viewer%3 == 2 {
			group = 1
		}
		pools[group] = append(pools[group], viewer)
	}
	schedule, next := make([]int, requests), [2]int{}
	for i := range schedule {
		group := 0
		if (i+i/10)%10 < 3 {
			group = 1
		}
		schedule[i] = pools[group][next[group]%len(pools[group])]
		next[group]++
	}
	return schedule
}

func TestBenchmarkViewerSchedule(t *testing.T) {
	for _, viewers := range []int{3, 4, 48, 96, 1024} {
		schedule := benchmarkViewerSchedule(viewers, 10000)
		counts, cold, coldContinuations := make([]int, viewers), 0, 0
		for i, viewer := range schedule {
			counts[viewer]++
			if viewer%3 == 2 {
				cold++
				if i%5 == 4 {
					coldContinuations++
				}
			}
		}
		if cold != 3000 || coldContinuations != 600 {
			t.Fatalf("%d viewers: consent/page mix changed: %d, %d", viewers, cold, coldContinuations)
		}
		for group := 0; group < 2; group++ {
			low, high := len(schedule), 0
			for viewer, count := range counts {
				if (viewer%3 == 2) == (group == 1) {
					low, high = min(low, count), max(high, count)
				}
			}
			if low == 0 || high-low > 1 {
				t.Fatalf("%d viewers: unbalanced group %d, min %d max %d", viewers, group, low, high)
			}
		}
	}
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
	viewerCount := benchmarkNumber(t, "JANSETU_BENCHMARK_VIEWERS", 3, 1024)
	if viewerCount < 3 || authors < 32 {
		t.Fatal("at least three viewers and 32 authors are required")
	}
	cfg := base.Config
	cfg.RecommendationTarget = ""
	cfg.RecommendationMode, cfg.RecommendationRollout = "serve", 100
	cfg.RecommendationSnapshotRedisURL, cfg.RecommendationFeatureShadowRedisURL = "", ""
	a := cloneTestApp(t, cfg)
	var queryTracer *benchmarkQueryTracer
	if os.Getenv("JANSETU_BENCHMARK_QUERY_TIMINGS") == "1" {
		queryTracer = &benchmarkQueryTracer{}
		a.DB = benchmarkTracedPool(t, base.DB, queryTracer)
	}
	rpc, err := recommendation.New(target, recommendation.TLSConfig{})
	if err != nil {
		t.Fatal(err)
	}
	defer rpc.Close()
	ranker := &measuredRanker{Ranker: rpc}
	a.Ranker = ranker
	clients, viewers := benchmarkViewers(t, a, viewerCount)
	schedule := benchmarkViewerSchedule(viewerCount, requests)
	denied, digest, inventoryDigest, published := benchmarkFixture(t, authors, perAuthor, viewers)
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
		Viewer       int
		PostIDs      []uuid.UUID
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
		s := sample{Continuation: cursor != "", Consenting: viewer%3 != 2, Viewer: viewer}
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
			s.PostIDs = append(s.PostIDs, item.Post.ID)
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
		if cursor != "" && s.Cursor == cursor {
			s.Error = "cursor_not_advanced"
		}
		return s
	}
	initial := make([]string, len(clients))
	warmup := make([]sample, len(clients))
	var warmupNext atomic.Int64
	var warmupWorkers sync.WaitGroup
	for worker := 0; worker < min(concurrency, viewerCount); worker++ {
		warmupWorkers.Add(1)
		go func() {
			defer warmupWorkers.Done()
			for {
				viewer := int(warmupNext.Add(1) - 1)
				if viewer >= viewerCount {
					return
				}
				warmup[viewer] = fetch(viewer, "")
			}
		}()
	}
	warmupWorkers.Wait()
	for viewer, s := range warmup {
		if s.Error != "" || s.Mode != "ranked" || s.Cursor == "" {
			t.Fatal("warmup failed", s.Error, s.Status, s.Mode)
		}
		initial[viewer] = s.Cursor
	}
	// Cursor ownership must hold for the independently authenticated fixture.
	if s := fetch(1, initial[0]); s.Status != http.StatusGone {
		t.Fatal("another viewer's snapshot cursor was accepted", s.Status)
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
			histories := map[int]*benchmarkPostHistory{}
			<-gate
			for {
				i := int(next.Add(1) - 1)
				if i >= requests {
					return
				}
				viewer := schedule[i]
				cursor := ""
				if i%5 == 4 {
					cursor = cursors[viewer]
				}
				s := fetch(viewer, cursor)
				if s.Error == "" {
					history := histories[viewer]
					if history == nil {
						history = &benchmarkPostHistory{}
						history.accept(false, warmup[viewer].PostIDs)
						histories[viewer] = history
					}
					if !history.accept(cursor != "", s.PostIDs) {
						s.Error = "duplicate_across_pages"
					} else {
						cursors[viewer] = s.Cursor
					}
				}
				// IDs are validation-only and never retained in result artifacts.
				s.PostIDs = nil
				results[i] = s
			}
		}()
	}
	// Baselines exclude authentication, fixture construction and warmup.
	pools := map[string]*pgxpool.Pool{
		"social": a.DB, "auth": a.Auth, "operations": a.Operations,
		"publication": a.Publication, "vault": integrationVaultRuntime, "vaultAuth": integrationVaultAuth,
	}
	poolBefore := make(map[string]*pgxpool.Stat, len(pools))
	for name, pool := range pools {
		poolBefore[name] = pool.Stat()
	}
	started := time.Now()
	if queryTracer != nil {
		queryTracer.start()
	}
	close(gate)
	workers.Wait()
	elapsed := time.Since(started).Seconds()
	queryTimings := map[string]any{"enabled": false}
	if queryTracer != nil {
		queryTimings = queryTracer.finish()
	}
	poolMeasurements := make(map[string]any, len(pools))
	for name, pool := range pools {
		before, after := poolBefore[name], pool.Stat()
		poolMeasurements[name] = map[string]any{
			"successfulAcquires":     after.AcquireCount() - before.AcquireCount(),
			"successfulAcquireMs":    float64(after.AcquireDuration()-before.AcquireDuration()) / float64(time.Millisecond),
			"emptyAcquires":          after.EmptyAcquireCount() - before.EmptyAcquireCount(),
			"emptyAcquireWaitMs":     float64(after.EmptyAcquireWaitTime()-before.EmptyAcquireWaitTime()) / float64(time.Millisecond),
			"canceledAcquires":       after.CanceledAcquireCount() - before.CanceledAcquireCount(),
			"newConnections":         after.NewConnsCount() - before.NewConnsCount(),
			"maxConnections":         after.MaxConns(),
			"totalConnectionsBefore": before.TotalConns(), "totalConnectionsAfter": after.TotalConns(),
			"acquiredConnectionsBefore": before.AcquiredConns(), "acquiredConnectionsAfter": after.AcquiredConns(),
		}
	}
	latencies := make([]float64, 0, requests)
	firstLatency, continuationLatency, consentedLatency, coldLatency := []float64{}, []float64{}, []float64{}, []float64{}
	errors, modes, statuses := map[string]int{}, map[string]int{}, map[string]int{}
	success, continuations, nonconsenting, bytes := 0, 0, 0, 0
	viewerRequests := make([]int, viewerCount)
	for _, s := range results {
		viewerRequests[s.Viewer]++
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
	measuredViewers, maximumPerViewer := 0, 0
	for _, count := range viewerRequests {
		if count > 0 {
			measuredViewers++
		}
		maximumPerViewer = max(maximumPerViewer, count)
	}
	var pgVersion string
	if err := integrationAdmin.QueryRow(context.Background(), "SELECT version()").Scan(&pgVersion); err != nil {
		t.Fatal(err)
	}
	stats := map[string]any{
		"fixture": "api-smoke-v2", "datasetSHA256": digest, "fixtureContentSHA256": inventoryDigest, "authors": authors, "postsPerAuthor": perAuthor,
		"fixtureEligiblePosts": published, "fixtureExcludedPosts": len(denied), "seededViewers": viewerCount,
		"consentingViewers": viewerCount - viewerCount/3, "nonconsentingViewers": viewerCount / 3,
		"measuredViewers": measuredViewers, "maximumRequestsPerViewer": maximumPerViewer,
		"requests": requests, "concurrency": concurrency, "warmupRequests": viewerCount, "cursorOwnershipChecks": 1, "successfulFeeds": success,
		"paginationPolicy": "advance-on-success-v2", "crossPagePostValidation": true,
		"errors": requests - success, "errorCounts": errors, "statusCounts": statuses, "modeCounts": modes,
		"continuations": continuations, "nonconsentingRequests": nonconsenting, "responseBytes": bytes,
		"databasePools":        poolMeasurements,
		"databaseQueryTimings": queryTimings,
		"elapsedSeconds":       elapsed, "successfulRequestsPerSecond": float64(success) / elapsed,
		"allResponseLatency": benchmarkPercentiles(latencies),
		"firstPageLatency":   benchmarkPercentiles(firstLatency), "continuationLatency": benchmarkPercentiles(continuationLatency),
		"consentedLatency": benchmarkPercentiles(consentedLatency), "nonconsentingLatency": benchmarkPercentiles(coldLatency),
		"recommendationRPC": map[string]any{"calls": len(ranker.latencies), "errors": ranker.errors, "maximumCandidates": ranker.candidates, "latency": benchmarkPercentiles(ranker.latencies)},
		"measuredAt":        started.UTC().Format(time.RFC3339Nano), "goVersion": runtime.Version(), "goMaxProcs": runtime.GOMAXPROCS(0),
		"postgresVersion": pgVersion, "runtimeRole": "js_social", "socialPoolMaxConns": a.DB.Config().MaxConns,
		"apiReplicas": 1, "rustReplicas": 1, "tls": false, "snapshotCache": false, "behavioralShadow": false,
		"costPerThousandFeeds": nil, "costPerMillionEvents": nil,
		"completeFeedCapacityValidated": false, "qualityValidated": false,
		"scope": "closed-loop loopback HTTP Go API / restricted PostgreSQL / Rust RPC, configurable independent synthetic viewers; excludes BFF, production TLS, cache and background event workers",
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
	if queryTracer != nil {
		groups := queryTimings["groups"].(map[string]any)
		for label, expected := range map[string]int{
			"candidateRetrieval": requests - continuations,
			"postHydration":      requests, "principalLock": requests, "profileLock": requests,
			"preferenceRead": requests * 2, "snapshotRead": continuations,
		} {
			calls := 0
			if group, ok := groups[label]; ok {
				calls = group.(map[string]any)["calls"].(int)
			}
			if calls != expected {
				t.Fatalf("query tracer %s: got %d calls, expected %d", label, calls, expected)
			}
		}
	}
	t.Logf("%d successful complete feeds, %.2f requests/s; all-response latency %v", success, float64(success)/elapsed, stats["allResponseLatency"])
}

// Identity, language, body, state and age offsets are reproducible. Published
// times use a run anchor; that relative-time policy is part of the dataset hash.
func benchmarkFixture(t *testing.T, authors, perAuthor int, viewers []uuid.UUID) (map[uuid.UUID]bool, string, string, int) {
	t.Helper()
	denied := map[uuid.UUID]bool{}
	hash := sha256.New()
	inventoryHash := sha256.New()
	contentHash := io.MultiWriter(hash, inventoryHash)
	seed, err := os.ReadFile("../../../../db/seed/local.sql")
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprintf(hash, "seedSHA256=%x\n", sha256.Sum256(seed))
	fmt.Fprintln(hash, "api-smoke-v2; seed=db/seed/local.sql; follows=every-eighth-author; blocks=last-author; consenting=viewer-index-modulo-three-not-two")
	for i, viewer := range viewers {
		fmt.Fprintf(hash, "viewer=%d|profile=%s|consenting=%t\n", i, viewer, i%3 != 2)
	}
	published := 0
	ctx := context.Background()
	err = pgx.BeginFunc(ctx, integrationAdmin, func(tx pgx.Tx) error {
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
				fmt.Fprintf(contentHash, "%s|%s|%s|%s|%s|ageSeconds=%d\n", author, post, state, language, body, ordinal)
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
		_, err := tx.Exec(ctx, "ANALYZE social.post; ANALYZE social.post_revision; ANALYZE social.profile; ANALYZE social.profile_follow; ANALYZE social.profile_block; ANALYZE identity.principal; ANALYZE identity.session; ANALYZE social.recommendation_preference")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return denied, hex.EncodeToString(hash.Sum(nil)), hex.EncodeToString(inventoryHash.Sum(nil)), published
}

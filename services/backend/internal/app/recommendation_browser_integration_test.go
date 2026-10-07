package app

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/lavkushry/JanSetu-AI/services/backend/internal/recommendation"
)

// Opt-in fixture harness: keeps isolated TestMain databases alive for a real
// browser/BFF/API/Rust proof without touching the running pilot database.
func TestRecommendationBrowserFixture(t *testing.T) {
	path := os.Getenv("JANSETU_RECOMMENDATION_BROWSER_FIXTURE")
	if path == "" {
		t.Skip("opt-in browser fixture")
	}
	a := testApp(t)
	cfg := a.Config
	cfg.RecommendationTarget = ""
	cfg.RecommendationMode = "serve"
	cfg.RecommendationRollout = 100
	cfg.WebOrigin = "http://127.0.0.1:13100"
	a = cloneTestApp(t, cfg)
	target := os.Getenv("JANSETU_RECOMMENDATION_TEST_TARGET")
	if target == "" {
		target = "127.0.0.1:50051"
	}
	rpc, err := recommendation.New(target, recommendation.TLSConfig{})
	if err != nil {
		t.Fatal(err)
	}
	defer rpc.Close()
	a.Ranker = rpc
	recommendationFixture(t, a)
	server := httptest.NewServer(a.Handler())
	defer server.Close()
	data, _ := json.Marshal(map[string]string{"apiUrl": server.URL, "stopFile": path + ".stop"})
	if err = os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(path)
	defer os.Remove(path + ".stop")
	timer := time.NewTimer(5 * time.Minute)
	defer timer.Stop()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-timer.C:
			t.Fatal("browser fixture timed out")
		case <-ticker.C:
			if _, err = os.Stat(path + ".stop"); err == nil {
				return
			}
		}
	}
}

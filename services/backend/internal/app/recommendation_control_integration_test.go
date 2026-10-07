package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lavkushry/JanSetu-AI/services/backend/internal/platform"
	"github.com/lavkushry/JanSetu-AI/services/backend/internal/recommendation/control"
	"github.com/lavkushry/JanSetu-AI/services/backend/internal/recommendation/pb"
)

func servingControlPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool, err := platform.RuntimePool(context.Background(), roleURL(integrationAdmin.Config().ConnString(), "js_recommendation_control"), "js_recommendation_control")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	t.Cleanup(func() {
		state, err := control.Load(context.Background(), pool)
		if err == nil && state.Disabled {
			_, err = control.Set(context.Background(), pool, false, state.Version)
		}
		if err != nil {
			t.Error("restore serving control", err)
		}
	})
	return pool
}

func TestRecommendationServingControlRestrictedOperatorAndReplicas(t *testing.T) {
	base := testApp(t)
	ctx := context.Background()
	operator := servingControlPool(t)
	state, err := control.Load(ctx, operator)
	if err != nil || state.Disabled {
		t.Fatal("initial shared control", state, err)
	}
	// The operator can only inspect/set the switch, never user or exported data.
	for _, table := range []string{"identity.principal", "ops.report", "social.post_revision", "social.recommendation_preference", "rec_stream.outbox", "rec_serving.control", "rec_serving.control_audit"} {
		if _, err := operator.Exec(ctx, "SELECT * FROM "+table+" LIMIT 1"); err == nil {
			t.Fatal("operator can read", table)
		}
	}
	if _, err := base.DB.Exec(ctx, "SELECT rec_serving.set_disabled(true,$1)", state.Version); err == nil {
		t.Fatal("API login can change shared control")
	}
	var vaultAccess bool
	if err := operator.QueryRow(ctx, "SELECT has_database_privilege(current_user,$1,'CONNECT')", integrationVaultAdmin.Config().ConnConfig.Database).Scan(&vaultAccess); err != nil || vaultAccess {
		t.Fatal("operator can access vault", err)
	}
	if _, err := operator.Exec(ctx, "SELECT rec_stream.claim($1,1)", "00000000-0000-0000-0000-000000000001"); err == nil {
		t.Fatal("operator can claim recommendation events")
	}
	// Build and execute the actual operator command against the isolated database.
	binary := filepath.Join(t.TempDir(), "recommendation-control")
	if output, err := exec.Command("go", "build", "-o", binary, "../../cmd/recommendation-control").CombinedOutput(); err != nil {
		t.Fatal("build operator command", err, string(output))
	}
	run := func(mode string, version int64) (control.State, error) {
		args := []string{"-mode", mode}
		if version > 0 {
			args = append(args, "-if-version", strconv.FormatInt(version, 10))
		}
		cmd := exec.Command(binary, args...)
		for _, setting := range os.Environ() {
			if !strings.HasPrefix(setting, "JANSETU_ENV=") && !strings.HasPrefix(setting, "JANSETU_RECOMMENDATION_CONTROL_DATABASE_URL=") {
				cmd.Env = append(cmd.Env, setting)
			}
		}
		cmd.Env = append(cmd.Env, "JANSETU_ENV=test", "JANSETU_RECOMMENDATION_CONTROL_DATABASE_URL="+roleURL(integrationAdmin.Config().ConnString(), "js_recommendation_control"))
		data, err := cmd.Output()
		var result control.State
		if err == nil {
			err = json.Unmarshal(data, &result)
		}
		return result, err
	}
	if actual, err := run("get", 0); err != nil || actual != state {
		t.Fatal("operator get", actual, err)
	}
	if _, err := run("disable", 0); err == nil {
		t.Fatal("operator changed state without expected version")
	}
	cfg := base.Config
	cfg.RecommendationTarget = ""
	cfg.RecommendationMode = "serve"
	cfg.RecommendationRollout = 100
	a, replica := cloneTestApp(t, cfg), cloneTestApp(t, cfg)
	calls := 0
	a.Ranker = testRanker(func(ctx context.Context, request *pb.RecommendRequest) (*pb.RecommendResponse, error) {
		calls++
		return goldenRanker(ctx, request)
	})
	replica.Ranker = a.Ranker
	owner, replicaOwner := login(t, a, 0), login(t, replica, 0)
	recommendationFixture(t, a)
	first := recommendedPage(t, owner, "")
	if first.RecommendationMode != "ranked" || first.NextCursor == nil {
		t.Fatal("missing initial ranked snapshot")
	}
	state, err = run("disable", state.Version)
	if err != nil || !state.Disabled {
		t.Fatal("operator disable", state, err)
	}
	var auditActor string
	if err := integrationAdmin.QueryRow(ctx, "SELECT database_actor FROM rec_serving.control_audit WHERE version=$1 AND disabled", state.Version).Scan(&auditActor); err != nil || auditActor != "js_recommendation_control" {
		t.Fatal("missing authenticated control audit", auditActor, err)
	}
	for _, viewer := range []client{owner, replicaOwner} {
		mustStatus(t, viewer.request("GET", "feed?sort=recommended&cursor="+*first.NextCursor, nil, 0, ""), 410)
		if page := recommendedPage(t, viewer, ""); page.RecommendationMode != "fallback" {
			t.Fatal("replica ignored shared rollback")
		}
	}
	if calls != 1 {
		t.Fatal("disabled shared control still invoked ranker", calls)
	}
	fallback := recommendedPage(t, owner, "")
	if fallback.NextCursor == nil {
		t.Fatal("missing fallback cursor")
	}
	fallbackPage := recommendedPage(t, owner, *fallback.NextCursor)
	state, err = run("enable", state.Version)
	if err != nil || state.Disabled {
		t.Fatal("operator enable", state, err)
	}
	mustStatus(t, owner.request("GET", "feed?sort=recommended&cursor="+*first.NextCursor, nil, 0, ""), 410)
	if !reflect.DeepEqual(fallbackPage, recommendedPage(t, replicaOwner, *fallback.NextCursor)) {
		t.Fatal("enable reordered chronological snapshot")
	}
	if page := recommendedPage(t, owner, ""); page.RecommendationMode != "ranked" {
		t.Fatal("new snapshots did not resume ranking")
	}
	if _, err := run("disable", state.Version-1); err == nil {
		t.Fatal("stale operator version changed serving")
	}
	if unchanged, err := control.Set(ctx, operator, false, state.Version); err != nil || unchanged != state {
		t.Fatal("no-op control update changed epoch", unchanged, err)
	}
	// Competing changes with the same expected version serialize; only one wins.
	var wg sync.WaitGroup
	errorsByWriter := make([]error, 2)
	for writer := range errorsByWriter {
		wg.Add(1)
		go func(writer int) {
			defer wg.Done()
			_, errorsByWriter[writer] = control.Set(ctx, operator, true, state.Version)
		}(writer)
	}
	wg.Wait()
	winners := 0
	for _, err := range errorsByWriter {
		if err == nil {
			winners++
		} else if !errors.Is(err, pgx.ErrNoRows) {
			t.Fatal("competing control write", err)
		}
	}
	if winners != 1 {
		t.Fatal("compare-and-set allowed conflicting writers", winners)
	}
}

func TestRecommendationServingControlFailureAndInFlightDisable(t *testing.T) {
	base := testApp(t)
	ctx := context.Background()
	operator := servingControlPool(t)
	cfg := base.Config
	cfg.RecommendationTarget = ""
	cfg.RecommendationMode = "serve"
	cfg.RecommendationRollout = 100
	a := cloneTestApp(t, cfg)
	owner := login(t, a, 0)
	recommendationFixture(t, a)
	a.Ranker = testRanker(goldenRanker)
	first := recommendedPage(t, owner, "")
	if first.NextCursor == nil {
		t.Fatal("missing ranked cursor")
	}
	if _, err := integrationAdmin.Exec(ctx, "REVOKE EXECUTE ON FUNCTION rec_serving.current_control() FROM js_social"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		integrationAdmin.Exec(ctx, "GRANT EXECUTE ON FUNCTION rec_serving.current_control() TO js_social")
	})
	if recommendedPage(t, owner, "").RecommendationMode != "fallback" {
		t.Fatal("control outage did not disable ranking")
	}
	mustStatus(t, owner.request("GET", "feed?sort=recommended&cursor="+*first.NextCursor, nil, 0, ""), 410)
	if _, err := integrationAdmin.Exec(ctx, "GRANT EXECUTE ON FUNCTION rec_serving.current_control() TO js_social"); err != nil {
		t.Fatal(err)
	}
	state, err := control.Load(ctx, operator)
	if err != nil {
		t.Fatal(err)
	}
	a.Ranker = testRanker(func(ctx context.Context, request *pb.RecommendRequest) (*pb.RecommendResponse, error) {
		if _, err := control.Set(ctx, operator, true, state.Version); err != nil {
			t.Fatal(err)
		}
		return goldenRanker(ctx, request)
	})
	// The control changed after ranking began; final hydration must reject it.
	mustStatus(t, owner.request("GET", "feed?sort=recommended", nil, 0, ""), 410)
	if recommendedPage(t, owner, "").RecommendationMode != "fallback" {
		t.Fatal("retry did not use chronological fallback")
	}
	if _, err := control.Set(ctx, operator, false, state.Version); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal("stale compare-and-set accepted", err)
	}
}

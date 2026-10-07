package app

import (
	"context"
	"fmt"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lavkushry/JanSetu-AI/services/backend/internal/store/dbgen"
)

func TestReplyProjectionAndCommandDoNotDeadlock(t *testing.T) {
	a := testApp(t)
	owner, writer, mod := login(t, a, 0), login(t, a, 1), login(t, a, 2)
	p := published(t, a, owner, mod, "Concurrent reply projection fixture")
	drainActivity(t, a)
	cid := approvedReply(t, writer, mod, p.ID, nil)
	req := httptest.NewRequest("POST", "/v1/posts/"+p.ID.String()+"/comments", nil)
	req.AddCookie(owner.cookie)
	req = a.requestScope(req, uuid.NewString())
	actor, err := a.actor(req)
	if err != nil || actor == nil {
		t.Fatal("owner session unavailable", err)
	}

	// Pause the real notification insertion after the worker locked the post,
	// immediately before its foreign key acquires a lock on the recipient profile.
	const gateID int64 = 17052026
	gate, err := integrationAdmin.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer gate.Release()
	if _, err = gate.Exec(context.Background(), "SELECT pg_advisory_lock($1)", gateID); err != nil {
		t.Fatal(err)
	}
	defer gate.Exec(context.Background(), "SELECT pg_advisory_unlock($1)", gateID)
	ddl := fmt.Sprintf(`CREATE FUNCTION infra.test_notification_gate() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN PERFORM pg_advisory_xact_lock(%d); RETURN NEW; END $$;
CREATE TRIGGER test_notification_gate BEFORE INSERT ON social.notification
FOR EACH ROW WHEN (NEW.comment_id='%s'::uuid) EXECUTE FUNCTION infra.test_notification_gate();`, gateID, cid)
	if _, err = integrationAdmin.Exec(context.Background(), ddl); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		integrationAdmin.Exec(context.Background(), "DROP TRIGGER test_notification_gate ON social.notification; DROP FUNCTION infra.test_notification_gate()")
	})
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	workerDone := make(chan error, 1)
	go func() {
		for {
			worked, err := a.ProjectOnce(ctx, "concurrent-projection-test")
			if err != nil || !worked {
				workerDone <- err
				return
			}
		}
	}()
	deadline := time.Now().Add(3 * time.Second)
	for {
		var waiting bool
		if err := integrationAdmin.QueryRow(ctx, "SELECT EXISTS(SELECT FROM pg_locks WHERE locktype='advisory' AND objid::bigint=$1 AND NOT granted AND database=(SELECT oid FROM pg_database WHERE datname=current_database()))", gateID).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("worker did not reach notification insertion")
		}
		time.Sleep(10 * time.Millisecond)
	}
	commandCtx, commandCancel := context.WithTimeout(req.Context(), 8*time.Second)
	defer commandCancel()
	profileLocked := make(chan struct{})
	commandDone := make(chan error, 1)
	go func() {
		commandDone <- a.transaction(commandCtx, actor, func(q *dbgen.Queries) error {
			close(profileLocked)
			_, err := q.LockPost(commandCtx, p.ID)
			return err
		})
	}()
	select {
	case <-profileLocked:
		// Before the fix, this command holds the profile and waits for the post.
	case <-time.After(250 * time.Millisecond):
		// With ordered pilot serialization it waits before taking any row locks.
	}
	if _, err := gate.Exec(ctx, "SELECT pg_advisory_unlock($1)", gateID); err != nil {
		t.Fatal(err)
	}
	workerErr, commandErr := <-workerDone, <-commandDone
	if workerErr != nil || commandErr != nil {
		t.Fatalf("projection/command lock cycle: worker=%v command=%v", workerErr, commandErr)
	}
	var notifications int
	if err := integrationAdmin.QueryRow(ctx, "SELECT count(*) FROM social.notification WHERE comment_id=$1", cid).Scan(&notifications); err != nil || notifications != 1 {
		t.Fatal("reply notification not delivered exactly once", notifications, err)
	}
}

func TestIndependentPostStatsProjectWithoutPilotSerialization(t *testing.T) {
	a := testApp(t)
	owner, writer, mod := login(t, a, 0), login(t, a, 1), login(t, a, 2)
	first := published(t, a, owner, mod, "First independent stats fixture")
	second := published(t, a, owner, mod, "Second independent stats fixture")
	drainActivity(t, a)
	mustStatus(t, writer.request("PUT", "posts/"+first.ID.String()+"/vote", map[string]any{"value": 1}, 0, ""), 200)
	mustStatus(t, writer.request("PUT", "posts/"+second.ID.String()+"/repost", map[string]any{"enabled": true}, 0, ""), 200)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	gate, err := integrationAdmin.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer gate.Release()
	const statsGate int64 = 17052027
	if _, err := gate.Exec(ctx, "SELECT pg_advisory_lock($1),pg_advisory_lock($2)", statsGate, pilotMutationLock); err != nil {
		t.Fatal(err)
	}
	defer gate.Exec(context.Background(), "SELECT pg_advisory_unlock($1),pg_advisory_unlock($2)", statsGate, pilotMutationLock)
	ddl := fmt.Sprintf(`CREATE FUNCTION infra.test_stats_gate() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN PERFORM pg_advisory_xact_lock(%d); RETURN NEW; END $$;
CREATE TRIGGER test_stats_gate BEFORE INSERT ON social.post_stats
FOR EACH ROW WHEN (NEW.post_id='%s'::uuid) EXECUTE FUNCTION infra.test_stats_gate();`, statsGate, first.ID)
	if _, err := integrationAdmin.Exec(ctx, ddl); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		integrationAdmin.Exec(context.Background(), "DROP TRIGGER test_stats_gate ON social.post_stats; DROP FUNCTION infra.test_stats_gate()")
	})
	firstDone := make(chan error, 1)
	go func() { _, err := a.ProjectOnce(ctx, "first-stat-projector"); firstDone <- err }()
	deadline := time.Now().Add(3 * time.Second)
	for {
		var waiting bool
		if err := integrationAdmin.QueryRow(ctx, "SELECT EXISTS(SELECT FROM pg_locks WHERE locktype='advisory' AND objid::bigint=$1 AND NOT granted AND database=(SELECT oid FROM pg_database WHERE datname=current_database()))", statsGate).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("stats projection blocked on pilot lock")
		}
		time.Sleep(10 * time.Millisecond)
	}
	// The first aggregate and pilot lock are both held. A second aggregate still
	// commits its actual count projection and acknowledgement independently.
	otherCtx, otherCancel := context.WithTimeout(ctx, time.Second)
	defer otherCancel()
	worked, err := a.ProjectOnce(otherCtx, "second-stat-projector")
	if err != nil || !worked {
		t.Fatal("independent stats serialized", worked, err)
	}
	var up, reposts int64
	if err := integrationAdmin.QueryRow(ctx, "SELECT up_count,repost_count FROM social.post_stats WHERE post_id=$1", second.ID).Scan(&up, &reposts); err != nil || up != 0 || reposts != 1 {
		t.Fatal("second counts incorrect", up, reposts, err)
	}
	if _, err := gate.Exec(ctx, "SELECT pg_advisory_unlock($1)", statsGate); err != nil {
		t.Fatal(err)
	}
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	if err := integrationAdmin.QueryRow(ctx, "SELECT up_count FROM social.post_stats WHERE post_id=$1", first.ID).Scan(&up); err != nil || up != 1 {
		t.Fatal("first count incorrect", up, err)
	}
}

func TestRecommendedExposureAndReplyProjectionDoNotDeadlock(t *testing.T) {
	a := testApp(t)
	owner, writer, mod := login(t, a, 0), login(t, a, 1), login(t, a, 2)
	post := published(t, a, owner, mod, "Recommendation exposure lock fixture")
	drainActivity(t, a)
	consentRecommendation(t, owner, true)
	t.Cleanup(func() { consentRecommendation(t, owner, false) })
	comment := approvedReply(t, writer, mod, post.ID, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	gate, err := integrationAdmin.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer gate.Release()
	const notificationGate int64 = 17052028
	const exposureGate int64 = 17052029
	if _, err := gate.Exec(ctx, "SELECT pg_advisory_lock($1),pg_advisory_lock($2)", notificationGate, exposureGate); err != nil {
		t.Fatal(err)
	}
	defer gate.Exec(context.Background(), "SELECT pg_advisory_unlock($1),pg_advisory_unlock($2)", notificationGate, exposureGate)
	ddl := fmt.Sprintf(`CREATE FUNCTION infra.test_exposure_notification_gate() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN PERFORM pg_advisory_xact_lock(%d); RETURN NEW; END $$;
CREATE TRIGGER test_exposure_notification_gate BEFORE INSERT ON social.notification
FOR EACH ROW WHEN (NEW.comment_id='%s'::uuid) EXECUTE FUNCTION infra.test_exposure_notification_gate();
CREATE FUNCTION infra.test_recommendation_exposure_gate() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN PERFORM pg_advisory_xact_lock(%d); RETURN NEW; END $$;
CREATE TRIGGER test_recommendation_exposure_gate BEFORE INSERT ON social.recommendation_exposure
FOR EACH ROW WHEN (NEW.post_id='%s'::uuid) EXECUTE FUNCTION infra.test_recommendation_exposure_gate();`, notificationGate, comment, exposureGate, post.ID)
	if _, err := integrationAdmin.Exec(ctx, ddl); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		integrationAdmin.Exec(context.Background(), `DROP TRIGGER test_exposure_notification_gate ON social.notification;
DROP FUNCTION infra.test_exposure_notification_gate();
DROP TRIGGER test_recommendation_exposure_gate ON social.recommendation_exposure;
DROP FUNCTION infra.test_recommendation_exposure_gate()`)
	})
	var wg sync.WaitGroup
	workerDone := make(chan error, 1)
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			worked, err := a.ProjectOnce(ctx, "exposure-notification-projector")
			if err != nil || !worked {
				workerDone <- err
				return
			}
		}
	}()
	defer func() {
		cancel()
		gate.Exec(context.Background(), "SELECT pg_advisory_unlock($1),pg_advisory_unlock($2)", notificationGate, exposureGate)
		wg.Wait()
	}()
	waitGate := func(key int64) {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for {
			var waiting bool
			if err := integrationAdmin.QueryRow(ctx, "SELECT EXISTS(SELECT FROM pg_locks WHERE locktype='advisory' AND objid::bigint=$1 AND NOT granted AND database=(SELECT oid FROM pg_database WHERE datname=current_database()))", key).Scan(&waiting); err != nil {
				t.Fatal(err)
			}
			if waiting {
				return
			}
			if time.Now().After(deadline) {
				t.Fatal("request did not reach lock gate", key)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	waitGate(notificationGate) // worker holds the post before notification FK checks.
	request := httptest.NewRequest("GET", "/v1/feed?sort=recommended", nil).WithContext(ctx)
	request.AddCookie(owner.cookie)
	response := httptest.NewRecorder()
	wg.Add(1)
	feedDone := make(chan struct{})
	go func() { defer wg.Done(); a.Handler().ServeHTTP(response, request); close(feedDone) }()
	waitGate(exposureGate) // feed holds its profile before the post FK check.
	if _, err := gate.Exec(ctx, "SELECT pg_advisory_unlock($1),pg_advisory_unlock($2)", notificationGate, exposureGate); err != nil {
		t.Fatal(err)
	}
	if err := <-workerDone; err != nil {
		t.Fatal("notification/exposure lock cycle", err)
	}
	<-feedDone
	mustStatus(t, response, 200)
	var notifications, exposures int
	if err := integrationAdmin.QueryRow(ctx, "SELECT count(*) FROM social.notification WHERE comment_id=$1", comment).Scan(&notifications); err != nil || notifications != 1 {
		t.Fatal("notification not delivered once", notifications, err)
	}
	if err := integrationAdmin.QueryRow(ctx, "SELECT count(*) FROM social.recommendation_exposure WHERE post_id=$1 AND profile_id=$2", post.ID, myProfileID(t, owner)).Scan(&exposures); err != nil || exposures != 1 {
		t.Fatal("served exposure not recorded once", exposures, err)
	}
}

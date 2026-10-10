package app

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestRecommendationFeedbackSummary(t *testing.T) {
	base := testApp(t)
	cfg := base.Config
	cfg.RecommendationTarget = ""
	cfg.RecommendationMode, cfg.RecommendationRollout = "serve", 100
	a := cloneTestApp(t, cfg)
	a.Ranker = testRanker(goldenRanker)
	owner, other := login(t, a, 0), login(t, a, 1)
	viewer := myProfileID(t, owner)
	t.Cleanup(func() {
		integrationAdmin.Exec(context.Background(), `DELETE FROM social.recommendation_snapshot WHERE viewer_id=$1`, viewer)
		integrationAdmin.Exec(context.Background(), `DELETE FROM social.recommendation_exposure WHERE profile_id=$1`, viewer)
		integrationAdmin.Exec(context.Background(), `DELETE FROM social.recommendation_preference WHERE profile_id=$1`, viewer)
	})
	recommendationFixture(t, a)
	const path = "me/recommendation-feedback-summary"
	read := func(c client) recommendationFeedbackSummary {
		t.Helper()
		w := c.request("GET", path, nil, 0, "")
		mustStatus(t, w, 200)
		return parsed[recommendationFeedbackSummary](t, w)
	}
	mustStatus(t, client{app: a}.request("GET", path, nil, 0, ""), 401)
	p := consentRecommendation(t, owner, true)
	page := recommendedPage(t, owner, "")
	exposures := []uuid.UUID{}
	for _, item := range page.Items {
		if item.Recommendation.ExposureID != uuid.Nil {
			exposures = append(exposures, item.Recommendation.ExposureID)
		}
	}
	if len(exposures) < 4 {
		t.Fatal("insufficient fixture exposures")
	}
	ids := []uuid.UUID{}
	for i, kind := range []string{"MORE", "SATISFIED", "DISSATISFIED", "LESS"} {
		id := uuid.New()
		ids = append(ids, id)
		event := map[string]any{"eventId": id, "exposureId": exposures[i], "kind": kind}
		mustStatus(t, owner.request("POST", "me/recommendation-events", event, 0, ""), 200)
		mustStatus(t, owner.request("POST", "me/recommendation-events", event, 0, ""), 200)
	}
	mustStatus(t, owner.request("POST", "me/recommendation-events", map[string]any{
		"eventId": uuid.New(), "exposureId": exposures[0], "kind": "READ", "activeMilliseconds": 0,
	}, 0, ""), 200)
	if got := read(owner); got != (recommendationFeedbackSummary{Generation: p.Generation, More: 1, Less: 1, Helpful: 1, NotHelpful: 1}) {
		t.Fatal("incorrect or duplicated counts", got)
	}
	if got := read(other); got.More+got.Less+got.Helpful+got.NotHelpful != 0 {
		t.Fatal("another viewer saw feedback", got)
	}
	ctx := context.Background()
	if _, err := integrationAdmin.Exec(ctx, `UPDATE social.recommendation_event SET created_at=now()-interval '31 days' WHERE id=$1`, ids[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := integrationAdmin.Exec(ctx, `UPDATE social.recommendation_event SET generation=generation+1 WHERE id=$1`, ids[1]); err != nil {
		t.Fatal(err)
	}
	if got := read(owner); got.More != 0 || got.Helpful != 0 || got.Less != 1 || got.NotHelpful != 1 {
		t.Fatal("time or generation fence failed", got)
	}
	w := owner.request("POST", "me/recommendation-history/reset", nil, p.Version, "")
	mustStatus(t, w, 200)
	reset := parsed[recommendationPreference](t, w)
	if got := read(owner); got != (recommendationFeedbackSummary{Generation: reset.Generation}) {
		t.Fatal("reset did not clear summary", got)
	}
	fresh := recommendedPage(t, owner, "")
	for _, item := range fresh.Items {
		if item.Recommendation.ExposureID != uuid.Nil {
			mustStatus(t, owner.request("POST", "me/recommendation-events", map[string]any{
				"eventId": uuid.New(), "exposureId": item.Recommendation.ExposureID, "kind": "SATISFIED",
			}, 0, ""), 200)
			break
		}
	}
	if got := read(owner); got.Helpful != 1 {
		t.Fatal("fresh generation feedback missing", got)
	}
	off := consentRecommendation(t, owner, false)
	if got := read(owner); got != (recommendationFeedbackSummary{Generation: off.Generation}) {
		t.Fatal("disabled consent summary", got)
	}
}

func TestRecommendationFeedbackRetentionSkipsLockedExposure(t *testing.T) {
	base := testApp(t)
	cfg := base.Config
	cfg.RecommendationTarget = ""
	cfg.RecommendationMode, cfg.RecommendationRollout = "serve", 100
	a := cloneTestApp(t, cfg)
	a.Ranker = testRanker(goldenRanker)
	owner := login(t, a, 0)
	viewer := myProfileID(t, owner)
	t.Cleanup(func() {
		integrationAdmin.Exec(context.Background(), `DELETE FROM social.recommendation_snapshot WHERE viewer_id=$1`, viewer)
		integrationAdmin.Exec(context.Background(), `DELETE FROM social.recommendation_exposure WHERE profile_id=$1`, viewer)
		integrationAdmin.Exec(context.Background(), `DELETE FROM social.recommendation_preference WHERE profile_id=$1`, viewer)
	})
	recommendationFixture(t, a)
	p := consentRecommendation(t, owner, true)
	var exposures []uuid.UUID
	for _, item := range recommendedPage(t, owner, "").Items {
		if item.Recommendation.ExposureID != uuid.Nil {
			exposures = append(exposures, item.Recommendation.ExposureID)
		}
	}
	if len(exposures) < 2 {
		t.Fatal("insufficient fixture exposures")
	}
	exposures = exposures[:2]
	for _, id := range exposures {
		mustStatus(t, owner.request("POST", "me/recommendation-events", map[string]any{
			"eventId": uuid.New(), "exposureId": id, "kind": "MORE",
		}, 0, ""), 200)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, sql := range []string{
		`UPDATE social.recommendation_exposure SET created_at=now()-interval '31 days',expires_at=now()-interval '31 days'+interval '5 minutes' WHERE id=ANY($1)`,
		`UPDATE social.recommendation_event SET created_at=now()-interval '31 days'+interval '1 minute' WHERE exposure_id=ANY($1)`,
	} {
		if _, err := integrationAdmin.Exec(ctx, sql, exposures); err != nil {
			t.Fatal(err)
		}
	}
	// Hold the parent lock that an owner reset/deactivation takes before its cascade.
	gate, err := integrationAdmin.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer gate.Rollback(context.Background())
	var locked uuid.UUID
	if err = gate.QueryRow(ctx, `SELECT id FROM social.recommendation_exposure WHERE id=$1 FOR UPDATE`, exposures[0]).Scan(&locked); err != nil {
		t.Fatal(err)
	}
	janitor, err := a.Worker.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer janitor.Rollback(context.Background())
	if _, err = janitor.Exec(ctx, `SET LOCAL lock_timeout='500ms'`); err != nil {
		t.Fatal(err)
	}
	if _, err = janitor.Exec(ctx, `SELECT social.expire_recommendations()`); err != nil {
		t.Fatal("janitor waited for the owner's parent lock", err)
	}
	if err = janitor.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	for i, id := range exposures {
		var parents, children int
		if err = integrationAdmin.QueryRow(ctx, `SELECT
          (SELECT count(*) FROM social.recommendation_exposure WHERE id=$1),
          (SELECT count(*) FROM social.recommendation_event WHERE exposure_id=$1)`, id).Scan(&parents, &children); err != nil {
			t.Fatal(err)
		}
		want := 1 - i
		if parents != want || children != want {
			t.Fatal("cleanup must skip the locked parent and its child, but expire the unlocked pair", i, parents, children)
		}
	}
	if err = gate.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	mustStatus(t, owner.request("POST", "me/recommendation-history/reset", nil, p.Version, ""), 200)
	if _, err = a.Worker.Exec(ctx, `SELECT social.expire_recommendations()`); err != nil {
		t.Fatal(err)
	}
}

func TestRecommendationFeedbackRetentionUsesEventAge(t *testing.T) {
	base := testApp(t)
	cfg := base.Config
	cfg.RecommendationTarget = ""
	cfg.RecommendationMode, cfg.RecommendationRollout = "serve", 100
	a := cloneTestApp(t, cfg)
	a.Ranker = testRanker(goldenRanker)
	owner := login(t, a, 0)
	viewer := myProfileID(t, owner)
	t.Cleanup(func() {
		integrationAdmin.Exec(context.Background(), `DELETE FROM social.recommendation_snapshot WHERE viewer_id=$1`, viewer)
		integrationAdmin.Exec(context.Background(), `DELETE FROM social.recommendation_exposure WHERE profile_id=$1`, viewer)
		integrationAdmin.Exec(context.Background(), `DELETE FROM social.recommendation_preference WHERE profile_id=$1`, viewer)
	})
	recommendationFixture(t, a)
	consentRecommendation(t, owner, true)
	var exposure uuid.UUID
	for _, item := range recommendedPage(t, owner, "").Items {
		if item.Recommendation.ExposureID != uuid.Nil {
			exposure = item.Recommendation.ExposureID
			break
		}
	}
	if exposure == uuid.Nil {
		t.Fatal("missing exposure")
	}
	fresh, old := uuid.New(), uuid.New()
	for _, event := range []map[string]any{
		{"eventId": fresh, "exposureId": exposure, "kind": "MORE"},
		{"eventId": old, "exposureId": exposure, "kind": "SATISFIED"},
	} {
		mustStatus(t, owner.request("POST", "me/recommendation-events", event, 0, ""), 200)
	}
	ctx := context.Background()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := integrationAdmin.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`UPDATE social.recommendation_exposure SET created_at=now()-interval '30 days 2 minutes',expires_at=now()-interval '30 days'+interval '3 minutes' WHERE id=$1`, exposure)
	exec(`UPDATE social.recommendation_event SET created_at=now()-interval '30 days'+interval '2 minutes' WHERE id=$1`, fresh)
	exec(`UPDATE social.recommendation_event SET created_at=now()-interval '30 days 1 minute' WHERE id=$1`, old)
	expire := func() {
		t.Helper()
		if _, err := a.Worker.Exec(ctx, `SELECT social.expire_recommendations()`); err != nil {
			t.Fatal(err)
		}
	}
	count := func(table string, id uuid.UUID) int {
		t.Helper()
		var n int
		if err := integrationAdmin.QueryRow(ctx, "SELECT count(*) FROM social."+table+" WHERE id=$1", id).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	expire()
	if count("recommendation_event", fresh) != 1 || count("recommendation_exposure", exposure) != 1 || count("recommendation_event", old) != 0 {
		t.Fatal("retention did not use individual event ages")
	}
	w := owner.request("GET", "me/recommendation-feedback-summary", nil, 0, "")
	mustStatus(t, w, 200)
	summary := parsed[recommendationFeedbackSummary](t, w)
	if summary.More != 1 || summary.Helpful != 0 {
		t.Fatal("retained summary incorrect", summary)
	}
	exec(`UPDATE social.recommendation_event SET created_at=now()-interval '30 days 1 second' WHERE id=$1`, fresh)
	expire()
	expire()
	if count("recommendation_event", fresh) != 0 || count("recommendation_exposure", exposure) != 0 {
		t.Fatal("expired feedback or empty exposure retained")
	}
}

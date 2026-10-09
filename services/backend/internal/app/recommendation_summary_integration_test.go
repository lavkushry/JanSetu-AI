package app

import (
	"context"
	"testing"

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

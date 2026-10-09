package app

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

func TestRecommendationUsefulnessOneAnswerAcrossSessions(t *testing.T) {
	base := testApp(t)
	cfg := base.Config
	cfg.RecommendationTarget = ""
	cfg.RecommendationMode, cfg.RecommendationRollout = "serve", 100
	a := cloneTestApp(t, cfg)
	a.Ranker = testRanker(goldenRanker)
	owner, secondSession := login(t, a, 0), login(t, a, 0)
	viewer := myProfileID(t, owner)
	t.Cleanup(func() {
		integrationAdmin.Exec(context.Background(), `DELETE FROM social.recommendation_snapshot WHERE viewer_id=$1`, viewer)
		integrationAdmin.Exec(context.Background(), `DELETE FROM social.recommendation_exposure WHERE profile_id=$1`, viewer)
		integrationAdmin.Exec(context.Background(), `DELETE FROM social.recommendation_preference WHERE profile_id=$1`, viewer)
	})
	recommendationFixture(t, a)
	consentRecommendation(t, owner, true)
	page := recommendedPage(t, owner, "")
	exposures := []uuid.UUID{}
	for _, item := range page.Items {
		if item.Recommendation.ExposureID != uuid.Nil {
			exposures = append(exposures, item.Recommendation.ExposureID)
		}
	}
	if len(exposures) < 3 {
		t.Fatal("insufficient fixture exposures")
	}
	const path = "me/recommendation-events"
	event := func(exposure uuid.UUID, kind string) map[string]any {
		return map[string]any{"eventId": uuid.New(), "exposureId": exposure, "kind": kind}
	}
	for i, kind := range []string{"SATISFIED", "DISSATISFIED"} {
		original := event(exposures[i], kind)
		mustStatus(t, owner.request("POST", path, original, 0, ""), 200)
		opposite := "SATISFIED"
		if kind == opposite {
			opposite = "DISSATISFIED"
		}
		mustStatus(t, secondSession.request("POST", path, event(exposures[i], opposite), 0, ""), 409)
		mustStatus(t, secondSession.request("POST", path, event(exposures[i], kind), 0, ""), 409)
		mustStatus(t, secondSession.request("POST", path, original, 0, ""), 200)
		mustStatus(t, owner.request("POST", path, event(exposures[i], "MORE"), 0, ""), 200)
		if _, err := integrationAdmin.Exec(context.Background(), `UPDATE social.recommendation_exposure SET expires_at=now()-interval '1 second' WHERE id=$1`, exposures[i]); err != nil {
			t.Fatal(err)
		}
		mustStatus(t, secondSession.request("POST", path, original, 0, ""), 200)
	}
	type outcome struct {
		code int
		body map[string]any
	}
	results := make(chan outcome, 2)
	gate := make(chan struct{})
	for i, kind := range []string{"SATISFIED", "DISSATISFIED"} {
		c := []client{owner, secondSession}[i]
		body := event(exposures[2], kind)
		go func() { <-gate; results <- outcome{c.request("POST", path, body, 0, "").Code, body} }()
	}
	close(gate)
	codes := map[int]int{}
	var winner map[string]any
	for i := 0; i < 2; i++ {
		result := <-results
		codes[result.code]++
		if result.code == 200 {
			winner = result.body
		}
	}
	if codes[200] != 1 || codes[409] != 1 {
		t.Fatal("opposite concurrent answers were not serialized", codes)
	}
	var count int
	if err := integrationAdmin.QueryRow(context.Background(), `SELECT count(*) FROM social.recommendation_event WHERE exposure_id=$1 AND kind IN ('SATISFIED','DISSATISFIED')`, exposures[2]).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatal("contradictory feedback was persisted", count)
	}
	mustStatus(t, owner.request("POST", path, winner, 0, ""), 200)
}

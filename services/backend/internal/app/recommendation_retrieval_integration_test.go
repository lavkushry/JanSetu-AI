package app

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/lavkushry/JanSetu-AI/services/backend/internal/recommendation/pb"
	"github.com/lavkushry/JanSetu-AI/services/backend/internal/vault"
)

// Exercise the actual restricted retrieval query, including inputs which an
// accepted-event happy path does not produce (expired, stale and old history).
func TestRecommendationRetrievalViewerHistoryAndMutes(t *testing.T) {
	a := testApp(t)
	owner, other := login(t, a, 0), login(t, a, 1)
	viewer, foreign := myProfileID(t, owner), myProfileID(t, other)
	p := consentRecommendation(t, owner, true)
	foreignPrefs := consentRecommendation(t, other, true)
	ctx := context.Background()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := integrationAdmin.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	communities := []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}
	// This cleanup runs after the fixture has removed its posts.
	t.Cleanup(func() { exec(`DELETE FROM social.community WHERE id=ANY($1)`, communities) })
	ids := recommendationFixture(t, a)
	t.Cleanup(func() {
		exec(`DELETE FROM social.mute WHERE profile_id=ANY($1)`, []uuid.UUID{viewer, foreign})
		exec(`DELETE FROM social.recommendation_snapshot WHERE viewer_id=ANY($1)`, []uuid.UUID{viewer, foreign})
		exec(`DELETE FROM social.recommendation_exposure WHERE profile_id=ANY($1)`, []uuid.UUID{viewer, foreign})
		exec(`DELETE FROM social.recommendation_preference WHERE profile_id=ANY($1)`, []uuid.UUID{viewer, foreign})
	})
	for _, id := range communities {
		exec(`INSERT INTO social.community(id,slug,title,scope_kind,visibility,rules_body,state)
 VALUES($1,$2,'Retrieval fixture','TOPIC','PUBLIC','Fixture rules','ACTIVE')`, id, "retrieval-"+id.String())
	}
	exec(`UPDATE social.post SET community_id=$1 WHERE id=ANY($2)`, communities[0], []uuid.UUID{ids[2], ids[4]})
	exec(`UPDATE social.post SET community_id=$1 WHERE id=$2`, communities[1], ids[18])
	exec(`UPDATE social.post SET community_id=$1 WHERE id=ANY($2)`, communities[2], []uuid.UUID{ids[24], ids[26]})
	exec(`UPDATE social.post SET state='HIDDEN' WHERE id=$1`, ids[12])
	var authors [30]uuid.UUID
	for i, id := range ids {
		if err := integrationAdmin.QueryRow(ctx, `SELECT author_id FROM social.post WHERE id=$1`, id).Scan(&authors[i]); err != nil {
			t.Fatal(err)
		}
	}
	w := owner.request("PUT", "me/recommendation-preferences", map[string]any{
		"personalizationEnabled": true, "interests": []string{"retrieval-" + communities[1].String()}, "languages": []string{}, "locality": "",
	}, p.Version, "")
	mustStatus(t, w, 200)
	p = parsed[recommendationPreference](t, w)
	event := func(profile, post uuid.UUID, generation int64, revision int, kind string, ageDays int) {
		t.Helper()
		exposure := uuid.New()
		exec(`INSERT INTO social.recommendation_exposure(id,profile_id,post_id,revision,generation,model_version,policy_version,expires_at)
 VALUES($1,$2,$3,$4,$5,'fixture','fixture',statement_timestamp()+interval '1 hour')`, exposure, profile, post, revision, generation)
		exec(`INSERT INTO social.recommendation_event(id,profile_id,exposure_id,generation,kind,request_hash,created_at)
 VALUES($1,$2,$3,$4,$5,'\x00',statement_timestamp()-$6*interval '1 day')`, uuid.New(), profile, exposure, generation, kind, ageDays)
	}
	event(viewer, ids[0], p.Generation, 1, "MORE", 0)
	event(viewer, ids[2], p.Generation, 1, "MORE", 0)
	event(viewer, ids[4], p.Generation, 1, "MORE", 0) // repeated community signal
	event(viewer, ids[6], p.Generation, 1, "MORE", 31)
	event(viewer, ids[8], p.Generation, 2, "MORE", 0) // stale exposed revision
	event(viewer, ids[10], p.Generation-1, 1, "MORE", 0)
	event(viewer, ids[12], p.Generation, 1, "MORE", 0) // publication revoked
	event(foreign, ids[14], foreignPrefs.Generation, 1, "MORE", 0)
	event(viewer, ids[16], p.Generation, 1, "READ", 0)
	exec(`INSERT INTO social.mute(id,profile_id,muted_profile_id) VALUES($1,$2,$3)`, uuid.New(), viewer, authors[20])
	exec(`INSERT INTO social.mute(id,profile_id,muted_profile_id,expires_at)
 VALUES($1,$2,$3,statement_timestamp()-interval '1 day')`, uuid.New(), viewer, authors[22])
	exec(`INSERT INTO social.mute(id,profile_id,muted_community_id) VALUES($1,$2,$3)`, uuid.New(), viewer, communities[2])
	exec(`INSERT INTO social.mute(id,profile_id,muted_profile_id) VALUES($1,$2,$3)`, uuid.New(), foreign, authors[28])

	read := func(c client, pref recommendationPreference, requestedViewer uuid.UUID) map[uuid.UUID]float64 {
		t.Helper()
		scope := scopedContext(c, a.DB, vault.Grant{})
		rows, err := a.store(scope).Query(scope, recommendationCandidates, requestedViewer, pref.Interests, pref.Locality, uuid.Nil, pref.Languages, pref.Generation, pref.PersonalizationEnabled)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		result := map[uuid.UUID]float64{}
		for rows.Next() {
			var id, author uuid.UUID
			var candidate pb.Candidate
			if err := rows.Scan(&id, &candidate.Revision, &author, &candidate.DedupKey, &candidate.ConversationKey, &candidate.ExplicitInterest, &candidate.Locality, &candidate.Relationship, &candidate.Freshness, &candidate.BoundedUsefulness); err != nil {
				t.Fatal(err)
			}
			result[id] = candidate.ExplicitInterest
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return result
	}
	check := func(result map[uuid.UUID]float64, boosted []int, excluded []int) {
		t.Helper()
		wantBoost, wantExcluded := map[int]bool{}, map[int]bool{}
		for _, i := range boosted {
			wantBoost[i] = true
		}
		for _, i := range excluded {
			wantExcluded[i] = true
		}
		for i, id := range ids {
			interest, present := result[id]
			if present != !wantExcluded[i] {
				t.Fatalf("fixture %d eligibility: present=%v", i, present)
			}
			wantInterest := 0.0
			if wantBoost[i] {
				wantInterest = 1
			}
			if present && interest != wantInterest {
				t.Fatalf("fixture %d interest: got %v", i, interest)
			}
		}
	}
	check(read(owner, p, viewer), []int{0, 1, 2, 3, 4, 5, 18}, []int{12, 20, 21, 24, 26})
	// Consent gates behavioral interest, while chosen interests and mutes remain.
	disabled := p
	disabled.PersonalizationEnabled = false
	check(read(owner, disabled, viewer), []int{18}, []int{12, 20, 21, 24, 26})
	// Supplying another user's ID cannot transfer RLS-protected history or mutes.
	check(read(other, p, viewer), []int{18}, []int{12})
	check(read(client{app: a}, p, viewer), []int{18}, []int{12})
	// A later generation cannot use old events, even before asynchronous deletion.
	next := p
	next.Generation++
	check(read(owner, next, viewer), []int{18}, []int{12, 20, 21, 24, 26})
	// Live resets preserve explicit controls and stop old behavioral interests.
	mustStatus(t, owner.request("POST", "me/recommendation-history/reset", nil, p.Version, ""), 200)
	next = recommendationPrefs(t, owner)
	check(read(owner, next, viewer), []int{18}, []int{12, 20, 21, 24, 26})
}

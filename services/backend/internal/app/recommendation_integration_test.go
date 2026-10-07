package app

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"sort"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/lavkushry/JanSetu-AI/services/backend/internal/platform"
	"github.com/lavkushry/JanSetu-AI/services/backend/internal/recommendation"
	"github.com/lavkushry/JanSetu-AI/services/backend/internal/recommendation/pb"
	"github.com/lavkushry/JanSetu-AI/services/backend/internal/vault"
)

type testRanker func(context.Context, *pb.RecommendRequest) (*pb.RecommendResponse, error)

func (f testRanker) Recommend(c context.Context, r *pb.RecommendRequest) (*pb.RecommendResponse, error) {
	return f(c, r)
}
func goldenRanker(_ context.Context, r *pb.RecommendRequest) (*pb.RecommendResponse, error) {
	candidates := append([]*pb.Candidate{}, r.Candidates...)
	score := func(c *pb.Candidate) float64 {
		return .35*c.ExplicitInterest + .25*c.Locality + .20*c.Relationship + .10*c.Freshness + .10*c.BoundedUsefulness
	}
	sort.Slice(candidates, func(i, j int) bool {
		a, b := score(candidates[i]), score(candidates[j])
		if a == b {
			return candidates[i].Id < candidates[j].Id
		}
		return a > b
	})
	result := &pb.RecommendResponse{ModelVersion: "rules-v1", PolicyVersion: "explicit-relevance-v1"}
	seen := map[string]bool{}
	conversations := map[string]bool{}
	for _, c := range candidates {
		if seen[c.DedupKey] || conversations[c.ConversationKey] {
			continue
		}
		seen[c.DedupKey] = true
		if c.ConversationKey != "" {
			conversations[c.ConversationKey] = true
		}
		reason := "RECENT_PUBLIC_POST"
		if c.ExplicitInterest > 0 {
			reason = "EXPLICIT_INTEREST"
		} else if c.Locality > 0 {
			reason = "CHOSEN_LOCALITY"
		} else if c.Relationship > 0 {
			reason = "FOLLOWING"
		}
		result.Items = append(result.Items, &pb.RankedReference{Id: c.Id, Revision: c.Revision, Explanation: reason})
		if len(result.Items) == int(r.Limit) {
			break
		}
	}
	return result, nil
}
func recommendationFixture(t *testing.T, a *App) []uuid.UUID {
	t.Helper()
	ctx := context.Background()
	ids := []uuid.UUID{}
	authors := []uuid.UUID{}
	err := pgx.BeginFunc(ctx, integrationAdmin, func(tx pgx.Tx) error {
		for i := 0; i < 15; i++ {
			author := uuid.New()
			authors = append(authors, author)
			if _, e := tx.Exec(ctx, `INSERT INTO social.profile(id,handle,display_name,state) VALUES($1,$2,'Recommendation fixture','ACTIVE')`, author, "rec_"+author.String()[:12]); e != nil {
				return e
			}
			for j := 0; j < 2; j++ {
				id := uuid.New()
				ids = append(ids, id)
				if _, e := tx.Exec(ctx, `INSERT INTO social.post(id,author_id,kind,state,published_revision,published_at) VALUES($1,$2,'SHORT','PUBLISHED',1,statement_timestamp()+interval '1 second')`, id, author); e != nil {
					return e
				}
				if _, e := tx.Exec(ctx, `INSERT INTO social.post_revision(post_id,revision,body,language_tag,review_state) VALUES($1,1,$2,'en-IN','APPROVED')`, id, "Safe published fixture "+id.String()); e != nil {
					return e
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		e := pgx.BeginFunc(ctx, integrationAdmin, func(tx pgx.Tx) error {
			tx.Exec(ctx, `DELETE FROM social.recommendation_exposure WHERE post_id=ANY($1)`, ids)
			tx.Exec(ctx, `DELETE FROM social.post_revision WHERE post_id=ANY($1)`, ids)
			tx.Exec(ctx, `DELETE FROM social.post WHERE id=ANY($1)`, ids)
			_, e := tx.Exec(ctx, `DELETE FROM social.profile WHERE id=ANY($1)`, authors)
			return e
		})
		if e != nil {
			t.Error(e)
		}
	})
	return ids
}

type recommendationPage struct {
	Items []struct {
		Type string
		Post struct {
			ID     uuid.UUID
			Author *struct{ ID uuid.UUID }
		}
		Receipt        struct{ ID uuid.UUID }
		Recommendation struct {
			Explanation string
			ExposureID  uuid.UUID
		}
		Section string
	}
	NextCursor         *string
	RecommendationMode string
}

func recommendedPage(t *testing.T, c client, cursor string) recommendationPage {
	t.Helper()
	path := "feed?sort=recommended"
	if cursor != "" {
		path += "&cursor=" + url.QueryEscape(cursor)
	}
	w := c.request("GET", path, nil, 0, "")
	mustStatus(t, w, 200)
	return parsed[recommendationPage](t, w)
}
func recommendationPrefs(t *testing.T, c client) recommendationPreference {
	t.Helper()
	w := c.request("GET", "me/recommendation-preferences", nil, 0, "")
	mustStatus(t, w, 200)
	return parsed[recommendationPreference](t, w)
}
func consentRecommendation(t *testing.T, c client, enabled bool) recommendationPreference {
	t.Helper()
	p := recommendationPrefs(t, c)
	w := c.request("PUT", "me/recommendation-preferences", map[string]any{"personalizationEnabled": enabled, "interests": []string{}, "languages": []string{}, "locality": ""}, p.Version, "")
	mustStatus(t, w, 200)
	return parsed[recommendationPreference](t, w)
}
func TestRecommendationSnapshotConsentVisibilityAndEvents(t *testing.T) {
	base := testApp(t)
	cfg := base.Config
	cfg.RecommendationMode = "serve"
	cfg.RecommendationRollout = 100
	cfg.RecommendationTarget = ""
	a := cloneTestApp(t, cfg)
	calls := 0
	a.Ranker = testRanker(func(ctx context.Context, r *pb.RecommendRequest) (*pb.RecommendResponse, error) {
		calls++
		return goldenRanker(ctx, r)
	})
	owner, other := login(t, a, 0), login(t, a, 1)
	viewer := myProfileID(t, owner)
	t.Cleanup(func() {
		integrationAdmin.Exec(context.Background(), `DELETE FROM social.recommendation_snapshot WHERE viewer_id=$1`, viewer)
		integrationAdmin.Exec(context.Background(), `DELETE FROM social.recommendation_exposure WHERE profile_id=$1`, viewer)
		integrationAdmin.Exec(context.Background(), `DELETE FROM social.recommendation_preference WHERE profile_id=$1`, viewer)
	})
	recommendationFixture(t, a)
	p := recommendationPrefs(t, owner)
	if p.PersonalizationEnabled {
		t.Fatal("consent enabled by default")
	}
	mustStatus(t, client{app: a}.request("GET", "me/recommendation-preferences", nil, 0, ""), 401)
	mustStatus(t, owner.request("PUT", "me/recommendation-preferences", map[string]any{}, p.Version, ""), 422)
	mustStatus(t, owner.request("PUT", "me/recommendation-preferences", map[string]any{"personalizationEnabled": true, "interests": []string{}, "languages": []string{}, "locality": ""}, 0, ""), 428)
	first := recommendedPage(t, owner, "")
	if first.RecommendationMode != "ranked" || len(first.Items) != 20 || first.NextCursor == nil {
		t.Fatal("missing ranked page", first)
	}
	for _, item := range first.Items {
		if item.Recommendation.ExposureID != uuid.Nil {
			t.Fatal("nonconsenting viewer received tracking exposure")
		}
	}
	p = consentRecommendation(t, owner, true)
	mustStatus(t, owner.request("GET", "feed?sort=recommended&cursor="+*first.NextCursor, nil, 0, ""), 410)
	first = recommendedPage(t, owner, "")
	if first.NextCursor == nil {
		t.Fatal("missing cursor")
	}
	token := *first.NextCursor
	mustStatus(t, other.request("GET", "feed?sort=recommended&cursor="+token, nil, 0, ""), 410)
	mustStatus(t, owner.request("GET", "feed?sort=recommended&communityId="+uuid.NewString()+"&cursor="+token, nil, 0, ""), 410)
	counts := map[uuid.UUID]int{}
	var exposure uuid.UUID
	var post uuid.UUID
	for _, v := range first.Items {
		if v.Type == "POST" && v.Post.Author != nil {
			counts[v.Post.Author.ID]++
			exposure = v.Recommendation.ExposureID
			post = v.Post.ID
		}
	}
	for _, n := range counts {
		if n > 2 {
			t.Fatal("author cap violated")
		}
	}
	if exposure == uuid.Nil {
		t.Fatal("missing consented exposure")
	}
	event := map[string]any{"eventId": uuid.NewString(), "exposureId": exposure, "kind": "MORE"}
	mustStatus(t, other.request("POST", "me/recommendation-events", event, 0, ""), 403)
	mustStatus(t, owner.request("POST", "me/recommendation-events", event, 0, ""), 200)
	mustStatus(t, owner.request("POST", "me/recommendation-events", event, 0, ""), 200)
	event["kind"] = "LESS"
	mustStatus(t, owner.request("POST", "me/recommendation-events", event, 0, ""), 409)
	event["eventId"] = uuid.NewString()
	mustStatus(t, owner.request("POST", "me/recommendation-events", event, 0, ""), 200)
	for _, v := range recommendedPage(t, owner, "").Items {
		if v.Post.ID == post {
			t.Fatal("Less preference ignored")
		}
	}
	// Changes to the ranker do not alter a snapshot or call the service again.
	before := calls
	a.Ranker = testRanker(func(context.Context, *pb.RecommendRequest) (*pb.RecommendResponse, error) {
		t.Fatal("ranker called for existing snapshot")
		return nil, nil
	})
	second := recommendedPage(t, owner, token)
	if calls != before {
		t.Fatal("snapshot reordered")
	}
	seen := map[uuid.UUID]bool{}
	for _, v := range first.Items {
		seen[v.Post.ID] = true
	}
	for _, v := range second.Items {
		if v.Type == "POST" && seen[v.Post.ID] {
			t.Fatal("post repeated across pages")
		}
	}
	mustStatus(t, owner.request("POST", "me/recommendation-history/reset", nil, p.Version, ""), 200)
	mustStatus(t, owner.request("POST", "me/recommendation-history/reset", nil, p.Version, ""), 412)
	mustStatus(t, owner.request("GET", "feed?sort=recommended&cursor="+token, nil, 0, ""), 410)
	mustStatus(t, owner.request("POST", "me/recommendation-events", map[string]any{"eventId": uuid.NewString(), "exposureId": exposure, "kind": "READ"}, 0, ""), 404)
	ctx := scopedContext(other, a.DB, vault.Grant{})
	var count int
	if err := a.store(ctx).QueryRow(ctx, `SELECT count(*) FROM social.recommendation_preference WHERE profile_id=$1`, viewer).Scan(&count); err != nil || count != 0 {
		t.Fatal("foreign preference leaked", err)
	}
	a.Ranker = testRanker(goldenRanker)
	// Revoke an unseen snapshot post and block another author before paging.
	first = recommendedPage(t, owner, "")
	if first.NextCursor == nil {
		t.Fatal("missing fresh cursor")
	}
	var payload []byte
	if err := integrationAdmin.QueryRow(context.Background(), `SELECT payload FROM social.recommendation_snapshot WHERE id=$1`, *first.NextCursor).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	var snapshot recommendationSnapshot
	if err := json.Unmarshal(payload, &snapshot); err != nil {
		t.Fatal(err)
	}
	unseen := snapshot.Posts[snapshot.PostOffset:]
	if len(unseen) < 2 {
		t.Fatal("not enough unseen candidates")
	}
	revoked := unseen[0].ID
	blocked := unseen[1].ID
	var blockedAuthor uuid.UUID
	integrationAdmin.Exec(context.Background(), `UPDATE social.post SET state='HIDDEN' WHERE id=$1`, revoked)
	integrationAdmin.QueryRow(context.Background(), `SELECT author_id FROM social.post WHERE id=$1`, blocked).Scan(&blockedAuthor)
	flag(t, owner, "blocks", blockedAuthor, true)
	t.Cleanup(func() {
		integrationAdmin.Exec(context.Background(), `DELETE FROM social.profile_block WHERE blocker_id=$1 AND blocked_id=$2`, viewer, blockedAuthor)
	})
	for _, v := range recommendedPage(t, owner, *first.NextCursor).Items {
		if v.Type == "POST" && (v.Post.ID == revoked || v.Post.ID == blocked) {
			t.Fatal("stale snapshot bypassed visibility")
		}
	}
	p = consentRecommendation(t, owner, false)
	if p.PersonalizationEnabled {
		t.Fatal("consent did not withdraw")
	}
	mustStatus(t, owner.request("POST", "me/recommendation-events", event, 0, ""), 403)
}
func TestRecommendationCivicAllocationGuestBindingAndRetention(t *testing.T) {
	a := testApp(t)
	owner := login(t, a, 0)
	recommendationFixture(t, a)
	ids := []uuid.UUID{}
	for i := 0; i < 8; i++ {
		id := uuid.New()
		ids = append(ids, id)
		_, err := integrationAdmin.Exec(context.Background(), `INSERT INTO social.case_receipt(id,title,safe_summary,area_label,public_state,urgency_tier,first_reported_at,projection_version,publication_version,publication_state,published_at,updated_at,policy_version) VALUES($1,'Civic fixture','Reviewed public fixture','Fixture locality','UNRESOLVED',$2,now()-interval '1 day',1,1,'PUBLISHED',now(),now(),'fixture')`, id, i%4)
		if err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		integrationAdmin.Exec(context.Background(), `DELETE FROM social.case_receipt WHERE id=ANY($1)`, ids)
	})
	page := recommendedPage(t, owner, "")
	civic := 0
	for _, v := range page.Items {
		if v.Type == "CASE_RECEIPT" {
			civic++
			if v.Section != "CIVIC_UPDATES" || v.Recommendation.Explanation != "CIVIC_URGENCY" {
				t.Fatal("unlabeled civic allocation")
			}
		}
	}
	if civic < 6 {
		t.Fatal("civic minimum not preserved", civic)
	}
	// A guest snapshot binds to a browser cookie, never only the nil viewer UUID.
	handler := a.Handler()
	request := httptest.NewRequest("GET", "/v1/feed?sort=recommended", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	mustStatus(t, response, 200)
	guest := parsed[recommendationPage](t, response)
	if guest.NextCursor == nil {
		t.Fatal("missing guest cursor")
	}
	cookies := response.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("guest binding cookie absent")
	}
	request = httptest.NewRequest("GET", "/v1/feed?sort=recommended&cursor="+*guest.NextCursor, nil)
	request.AddCookie(cookies[0])
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	mustStatus(t, response, 200)
	second := parsed[recommendationPage](t, response)
	request = httptest.NewRequest("GET", "/v1/feed?sort=recommended&cursor="+*guest.NextCursor, nil)
	request.AddCookie(cookies[0])
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	mustStatus(t, response, 200)
	replay := parsed[recommendationPage](t, response)
	if !reflect.DeepEqual(second, replay) {
		t.Fatal("cursor replay changed page")
	}
	request = httptest.NewRequest("GET", "/v1/feed?sort=recommended&cursor="+*guest.NextCursor, nil)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	mustStatus(t, response, 410)
	_, err := integrationAdmin.Exec(context.Background(), `UPDATE social.recommendation_snapshot SET expires_at=now()-interval '1 second' WHERE id=$1`, *guest.NextCursor)
	if err != nil {
		t.Fatal(err)
	}
	request = httptest.NewRequest("GET", "/v1/feed?sort=recommended&cursor="+*guest.NextCursor, nil)
	request.AddCookie(cookies[0])
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	mustStatus(t, response, 410)
	if _, err = a.Worker.Exec(context.Background(), `SELECT social.expire_recommendations()`); err != nil {
		t.Fatal(err)
	}
	var remaining int
	err = integrationAdmin.QueryRow(context.Background(), `SELECT count(*) FROM social.recommendation_snapshot WHERE id=$1`, *guest.NextCursor).Scan(&remaining)
	if err != nil || remaining != 0 {
		t.Fatal("expired snapshot retained", err)
	}
}

func TestRecommendationAcceptedRetryAfterExposureRevocation(t *testing.T) {
	a := testApp(t)
	owner := login(t, a, 0)
	recommendationFixture(t, a)
	consentRecommendation(t, owner, true)
	t.Cleanup(func() { consentRecommendation(t, owner, false) })
	page := recommendedPage(t, owner, "")
	for _, item := range page.Items {
		if item.Type != "POST" || item.Recommendation.ExposureID == uuid.Nil {
			continue
		}
		event := map[string]any{"eventId": uuid.NewString(), "exposureId": item.Recommendation.ExposureID, "kind": "MORE"}
		mustStatus(t, owner.request("POST", "me/recommendation-events", event, 0, ""), 200)
		if _, err := integrationAdmin.Exec(context.Background(), `UPDATE social.recommendation_exposure SET expires_at=statement_timestamp()-interval '1 second' WHERE id=$1`, item.Recommendation.ExposureID); err != nil {
			t.Fatal(err)
		}
		mustStatus(t, owner.request("POST", "me/recommendation-events", event, 0, ""), 200)
		event["kind"] = "LESS"
		mustStatus(t, owner.request("POST", "me/recommendation-events", event, 0, ""), 409)
		event["eventId"] = uuid.NewString()
		mustStatus(t, owner.request("POST", "me/recommendation-events", event, 0, ""), 404)
		return
	}
	t.Fatal("missing consenting post exposure")
}

func TestRecommendationRustWireAndFallback(t *testing.T) {
	base := testApp(t)
	cfg := base.Config
	cfg.RecommendationMode = "serve"
	cfg.RecommendationRollout = 100
	cfg.RecommendationTarget = ""
	a := cloneTestApp(t, cfg)
	owner := login(t, a, 0)
	recommendationFixture(t, a)
	if target := os.Getenv("JANSETU_RECOMMENDATION_TEST_TARGET"); target != "" {
		client, e := recommendation.New(target)
		if e != nil {
			t.Fatal(e)
		}
		defer client.Close()
		a.Ranker = client
		if recommendedPage(t, owner, "").RecommendationMode != "ranked" {
			t.Fatal("Rust gRPC boundary failed")
		}
	}
	// Unknown IDs/revisions must cause fallback, never a hydrated injected result.
	a.Ranker = testRanker(func(context.Context, *pb.RecommendRequest) (*pb.RecommendResponse, error) {
		return &pb.RecommendResponse{ModelVersion: "bad", PolicyVersion: "bad", Items: []*pb.RankedReference{{Id: uuid.NewString(), Revision: 1, Explanation: "FOLLOWING"}}}, nil
	})
	if recommendedPage(t, owner, "").RecommendationMode != "fallback" {
		t.Fatal("invalid response trusted")
	}
	a.Ranker = testRanker(func(ctx context.Context, _ *pb.RecommendRequest) (*pb.RecommendResponse, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})
	if recommendedPage(t, owner, "").RecommendationMode != "fallback" {
		t.Fatal("timeout did not fall back")
	}
	for _, role := range []string{"js_ops", "js_publication", "js_worker", "js_vault_auth", "js_media"} {
		pool, e := platform.RuntimePool(context.Background(), roleURL(integrationAdmin.Config().ConnString(), role), role)
		if e != nil {
			t.Fatal(e)
		}
		deniedSQL(t, pool, `SELECT * FROM social.recommendation_event`)
		pool.Close()
	}
}

func TestRecommendationRevisionDurationAndAccountDeactivation(t *testing.T) {
	a := testApp(t)
	owner := login(t, a, 0)
	viewer := myProfileID(t, owner)
	recommendationFixture(t, a)
	t.Cleanup(func() {
		integrationAdmin.Exec(context.Background(), `UPDATE social.profile SET state='ACTIVE' WHERE id=$1`, viewer)
		integrationAdmin.Exec(context.Background(), `DELETE FROM social.recommendation_exposure WHERE profile_id=$1`, viewer)
		integrationAdmin.Exec(context.Background(), `DELETE FROM social.recommendation_snapshot WHERE viewer_id=$1`, viewer)
		integrationAdmin.Exec(context.Background(), `DELETE FROM social.recommendation_preference WHERE profile_id=$1`, viewer)
	})
	p := consentRecommendation(t, owner, true)
	page := recommendedPage(t, owner, "")
	var exposure, post uuid.UUID
	for _, item := range page.Items {
		if item.Type == "POST" {
			exposure = item.Recommendation.ExposureID
			post = item.Post.ID
			break
		}
	}
	if exposure == uuid.Nil {
		t.Fatal("missing exposure")
	}
	event := map[string]any{"eventId": uuid.NewString(), "exposureId": exposure, "kind": "READ", "activeMilliseconds": 600000}
	mustStatus(t, owner.request("POST", "me/recommendation-events", event, 0, ""), 422)
	event["activeMilliseconds"] = 0
	mustStatus(t, owner.request("POST", "me/recommendation-events", event, 0, ""), 200)
	event["eventId"] = uuid.NewString()
	mustStatus(t, owner.request("POST", "me/recommendation-events", event, 0, ""), 409)
	_, err := integrationAdmin.Exec(context.Background(), `UPDATE social.recommendation_exposure SET revision=revision+1 WHERE id=$1`, exposure)
	if err != nil {
		t.Fatal(err)
	}
	event["kind"] = "MORE"
	mustStatus(t, owner.request("POST", "me/recommendation-events", event, 0, ""), 404)
	_, err = integrationAdmin.Exec(context.Background(), `UPDATE social.recommendation_exposure SET revision=revision-1 WHERE id=$1`, exposure)
	if err != nil {
		t.Fatal(err)
	}
	// Mutes and publication revocation prevent new events even with an issued exposure.
	var author uuid.UUID
	integrationAdmin.QueryRow(context.Background(), `SELECT author_id FROM social.post WHERE id=$1`, post).Scan(&author)
	muteTarget(t, owner, "PROFILE", author, true, nil)
	mustStatus(t, owner.request("POST", "me/recommendation-events", event, 0, ""), 404)
	muteTarget(t, owner, "PROFILE", author, false, nil)
	_, err = integrationAdmin.Exec(context.Background(), `UPDATE social.profile SET state='DEACTIVATED' WHERE id=$1`, viewer)
	if err != nil {
		t.Fatal(err)
	}
	var consent bool
	var generation int64
	err = integrationAdmin.QueryRow(context.Background(), `SELECT personalization_enabled,generation FROM social.recommendation_preference WHERE profile_id=$1`, viewer).Scan(&consent, &generation)
	if err != nil || consent || generation <= p.Generation {
		t.Fatal("deactivation did not revoke behavioral history", err)
	}
	var count int
	err = integrationAdmin.QueryRow(context.Background(), `SELECT count(*) FROM social.recommendation_exposure WHERE profile_id=$1`, viewer).Scan(&count)
	if err != nil || count != 0 {
		t.Fatal("deactivation retained exposures", err)
	}
	mustStatus(t, owner.request("POST", "me/recommendation-events", event, 0, ""), 401)
}

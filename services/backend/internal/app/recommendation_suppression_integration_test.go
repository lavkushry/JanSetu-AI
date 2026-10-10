package app

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/lavkushry/JanSetu-AI/services/backend/internal/recommendation"
	"github.com/lavkushry/JanSetu-AI/services/backend/internal/recommendation/pb"
	"github.com/lavkushry/JanSetu-AI/services/backend/internal/vault"
)

func TestRecommendationSuppressionBeforeCandidateBudgets(t *testing.T) {
	base := testApp(t)
	owner := login(t, base, 0)
	viewer := myProfileID(t, owner)
	p := consentRecommendation(t, owner, true)
	ctx := context.Background()
	community := uuid.New()
	slug := "suppression-" + community.String()
	w := owner.request("PUT", "me/recommendation-preferences", map[string]any{
		"personalizationEnabled": true, "interests": []string{slug}, "languages": []string{}, "locality": "",
	}, p.Version, "")
	mustStatus(t, w, 200)
	p = parsed[recommendationPreference](t, w)
	authors := make([]uuid.UUID, 25)
	posts := make([]uuid.UUID, 1050)
	for i := range authors {
		authors[i] = uuid.New()
	}
	for i := range posts {
		posts[i] = uuid.New()
	}
	t.Cleanup(func() {
		err := pgx.BeginFunc(ctx, integrationAdmin, func(tx pgx.Tx) error {
			for _, deletion := range []struct {
				sql string
				arg any
			}{
				{`DELETE FROM social.recommendation_snapshot WHERE viewer_id=$1`, viewer},
				{`DELETE FROM social.recommendation_exposure WHERE profile_id=$1`, viewer},
				{`DELETE FROM social.recommendation_preference WHERE profile_id=$1`, viewer},
				{`DELETE FROM social.profile_follow WHERE followed_id=ANY($1)`, authors},
				{`DELETE FROM social.post_revision WHERE post_id=ANY($1)`, posts},
				{`DELETE FROM social.post WHERE id=ANY($1)`, posts},
				{`DELETE FROM social.community WHERE id=$1`, community},
				{`DELETE FROM social.profile WHERE id=ANY($1)`, authors},
			} {
				if _, err := tx.Exec(ctx, deletion.sql, deletion.arg); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			t.Error(err)
		}
		// Content deletion envelopes are deferred until the transaction commits.
		if _, err := integrationAdmin.Exec(ctx, `DELETE FROM rec_stream.outbox
 WHERE payload->>'postId' IN (SELECT id::text FROM unnest($1::uuid[]) AS id)`, posts); err != nil {
			t.Error(err)
		}
	})
	err := pgx.BeginFunc(ctx, integrationAdmin, func(tx pgx.Tx) error {
		for _, statement := range []struct {
			sql  string
			args []any
		}{
			{`INSERT INTO social.profile(id,handle,display_name,state)
 SELECT id,'suppression_'||left(id::text,12),'Suppression fixture','ACTIVE' FROM unnest($1::uuid[]) AS id`, []any{authors}},
			{`INSERT INTO social.community(id,slug,title,scope_kind,visibility,rules_body,state)
 VALUES($1,$2,'Suppression fixture','TOPIC','PUBLIC','Fixture rules','ACTIVE')`, []any{community, slug}},
			{`INSERT INTO social.profile_follow(follower_id,followed_id)
 SELECT $1,id FROM unnest($2::uuid[]) AS id`, []any{viewer, authors}},
			{`INSERT INTO social.post(id,author_id,community_id,kind,state,published_revision,published_at)
 SELECT x.id,($2::uuid[])[((n-1)%25)+1],$3,'SHORT','PUBLISHED',1,
 transaction_timestamp()-n*interval '1 second'
 FROM unnest($1::uuid[]) WITH ORDINALITY x(id,n)`, []any{posts, authors, community}},
			{`INSERT INTO social.post_revision(post_id,revision,body,language_tag,review_state)
 SELECT id,1,'Published suppression fixture '||id::text,'en-IN','APPROVED' FROM unnest($1::uuid[]) AS id`, []any{posts}},
			// Simulate retained dismissals from prior sessions, not fresh exposure
			// submission. All three sources are dominated by previously hidden posts.
			{`INSERT INTO social.recommendation_exposure(id,profile_id,post_id,revision,generation,model_version,policy_version,created_at,expires_at)
 SELECT gen_random_uuid(),$2,id,1,$3,'history-fixture','history-fixture',
 transaction_timestamp()-interval '1 day',transaction_timestamp()-interval '1 day'+interval '5 minutes'
 FROM unnest($1::uuid[]) AS id`, []any{posts[:1000], viewer, p.Generation}},
			{`INSERT INTO social.recommendation_event(id,profile_id,exposure_id,generation,kind,request_hash)
 SELECT gen_random_uuid(),$2,x.id,$3,CASE WHEN n%2=0 THEN 'LESS' ELSE 'SKIP' END,'\x00'
 FROM unnest($1::uuid[]) WITH ORDINALITY history(post,n)
 JOIN social.recommendation_exposure x ON x.post_id=history.post AND x.profile_id=$2 AND x.generation=$3`, []any{posts[:1000], viewer, p.Generation}},
		} {
			if _, err := tx.Exec(ctx, statement.sql, statement.args...); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	allowed := map[uuid.UUID]bool{}
	for _, id := range posts[1000:] {
		allowed[id] = true
	}
	var wire recommendation.Ranker
	if target := os.Getenv("JANSETU_RECOMMENDATION_TEST_TARGET"); target != "" {
		client, err := recommendation.New(target, recommendation.TLSConfig{})
		if err != nil {
			t.Fatal(err)
		}
		defer client.Close()
		wire = client
	}
	for _, scenario := range []struct {
		name, mode, want string
		outage           bool
	}{
		{"ranked", "serve", "ranked", false},
		{"shadow", "shadow", "shadow", false},
		{"disabled", "off", "fallback", false},
		{"outage", "serve", "fallback", true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			cfg := base.Config
			cfg.RecommendationTarget = ""
			cfg.RecommendationMode, cfg.RecommendationRollout = scenario.mode, 100
			a := cloneTestApp(t, cfg)
			calls := 0
			a.Ranker = testRanker(func(ctx context.Context, request *pb.RecommendRequest) (*pb.RecommendResponse, error) {
				calls++
				if len(request.Candidates) != len(allowed) {
					t.Fatalf("hidden history consumed retrieval slots: got %d candidates, want %d", len(request.Candidates), len(allowed))
				}
				for _, candidate := range request.Candidates {
					if !allowed[uuid.MustParse(candidate.Id)] {
						t.Fatal("suppressed post reached ranking")
					}
				}
				if scenario.outage {
					return nil, errors.New("fixture ranker unavailable")
				}
				if wire != nil {
					return wire.Recommend(ctx, request)
				}
				return goldenRanker(ctx, request)
			})
			c := owner
			c.app = a
			path := "feed?sort=recommended&communityId=" + community.String()
			seen := map[uuid.UUID]bool{}
			for cursor, pageNumber := "", 0; ; pageNumber++ {
				if pageNumber > 3 {
					t.Fatal("pagination did not finish")
				}
				w := c.request("GET", path+cursor, nil, 0, "")
				mustStatus(t, w, 200)
				page := parsed[recommendationPage](t, w)
				if page.RecommendationMode != scenario.want || (pageNumber == 0 && len(page.Items) != 20) {
					t.Fatal("dismissals starved the fresh page", page.RecommendationMode, len(page.Items))
				}
				for _, item := range page.Items {
					if item.Type != "POST" || !allowed[item.Post.ID] || seen[item.Post.ID] || item.Recommendation.ExposureID == uuid.Nil {
						t.Fatal("suppressed, repeated or unexposed post returned", item.Post.ID)
					}
					seen[item.Post.ID] = true
				}
				if page.NextCursor == nil {
					break
				}
				cursor = "&cursor=" + *page.NextCursor
			}
			wantCalls := 1
			if scenario.mode == "off" {
				wantCalls = 0
			}
			if len(seen) != len(allowed) || calls != wantCalls {
				t.Fatal("eligible inventory was lost or cursor reranked", len(seen), calls)
			}
		})
	}
	var minted int
	if err := integrationAdmin.QueryRow(ctx, `SELECT count(*) FROM social.recommendation_exposure
 WHERE profile_id=$1 AND post_id=ANY($2) AND model_version<>'history-fixture'`, viewer, posts[:1000]).Scan(&minted); err != nil || minted != 0 {
		t.Fatal("hidden candidates received new exposures", minted, err)
	}
	mustStatus(t, owner.request("POST", "me/recommendation-history/reset", nil, p.Version, ""), 200)
	p = recommendationPrefs(t, owner)
	scope := scopedContext(owner, base.DB, vault.Grant{})
	var returned, restored int
	query := "WITH candidates AS (" + recommendationCandidates + `)
 SELECT count(*),count(*) FILTER (WHERE id=ANY($8::uuid[])) FROM candidates`
	if err := base.store(scope).QueryRow(scope, query, viewer, p.Interests, p.Locality, community, p.Languages, p.Generation, p.PersonalizationEnabled, posts[:1000]).Scan(&returned, &restored); err != nil || returned != 1000 || restored != 1000 {
		t.Fatal("history reset did not restore the original candidate inventory", returned, restored, err)
	}
}

func TestRecommendationSuppressionAfterRetrieval(t *testing.T) {
	base := testApp(t)
	cfg := base.Config
	cfg.RecommendationTarget = ""
	cfg.RecommendationMode, cfg.RecommendationRollout = "serve", 100
	a := cloneTestApp(t, cfg)
	a.Ranker = testRanker(goldenRanker)
	owner := login(t, a, 0)
	viewer := myProfileID(t, owner)
	recommendationFixture(t, a)
	t.Cleanup(func() {
		integrationAdmin.Exec(context.Background(), `DELETE FROM social.recommendation_snapshot WHERE viewer_id=$1`, viewer)
		integrationAdmin.Exec(context.Background(), `DELETE FROM social.recommendation_exposure WHERE profile_id=$1`, viewer)
		integrationAdmin.Exec(context.Background(), `DELETE FROM social.recommendation_preference WHERE profile_id=$1`, viewer)
	})
	consentRecommendation(t, owner, true)
	first := recommendedPage(t, owner, "")
	var post, exposure uuid.UUID
	for _, item := range first.Items {
		if item.Type == "POST" && item.Recommendation.ExposureID != uuid.Nil {
			post, exposure = item.Post.ID, item.Recommendation.ExposureID
			break
		}
	}
	if post == uuid.Nil {
		t.Fatal("missing fixture exposure")
	}
	// Complete Less in another session after candidate selection but before
	// final hydration. Early filtering cannot replace the live transaction check.
	otherSession := login(t, a, 0)
	a.Ranker = testRanker(func(ctx context.Context, request *pb.RecommendRequest) (*pb.RecommendResponse, error) {
		found := false
		for _, candidate := range request.Candidates {
			found = found || candidate.Id == post.String()
		}
		if !found {
			t.Fatal("race fixture was already excluded before ranking")
		}
		mustStatus(t, otherSession.request("POST", "me/recommendation-events", map[string]any{
			"eventId": uuid.New(), "exposureId": exposure, "kind": "LESS",
		}, 0, ""), 200)
		return goldenRanker(ctx, request)
	})
	page := recommendedPage(t, owner, "")
	if len(page.Items) == 0 || page.RecommendationMode != "ranked" {
		t.Fatal("race discarded the authorized feed")
	}
	for _, item := range page.Items {
		if item.Post.ID == post {
			t.Fatal("new Less feedback bypassed final hydration")
		}
	}
	var exposures int
	if err := integrationAdmin.QueryRow(context.Background(), `SELECT count(*) FROM social.recommendation_exposure WHERE profile_id=$1 AND post_id=$2`, viewer, post).Scan(&exposures); err != nil || exposures != 1 {
		t.Fatal("dismissed racing candidate received another exposure", exposures, err)
	}
}

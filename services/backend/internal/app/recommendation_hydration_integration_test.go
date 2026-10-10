package app

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lavkushry/JanSetu-AI/services/backend/internal/recommendation/pb"
	"github.com/lavkushry/JanSetu-AI/services/backend/internal/store/dbgen"
	"github.com/lavkushry/JanSetu-AI/services/backend/internal/vault"
)

type hydrationQueryKey struct{}
type hydrationQueryChange struct {
	change                   func() error
	changed                  bool
	err                      error
	metadataCalls            int
	payloadCalls             int
	maximumPayloadReferences int
}

func (h *hydrationQueryChange) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if !strings.HasPrefix(data.SQL, "-- name: RecommendationPosts :many\n") {
		return ctx
	}
	include := data.Args[0].(bool)
	if include {
		h.payloadCalls++
		h.maximumPayloadReferences = max(h.maximumPayloadReferences, len(data.Args[3].([]uuid.UUID)))
	} else {
		h.metadataCalls++
	}
	return context.WithValue(ctx, hydrationQueryKey{}, !include)
}

func (h *hydrationQueryChange) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryEndData) {
	metadata, _ := ctx.Value(hydrationQueryKey{}).(bool)
	if metadata && !h.changed {
		h.changed = true
		h.err = h.change()
	}
}

func TestRecommendationMetadataOmitsPayload(t *testing.T) {
	a := testApp(t)
	viewer := login(t, a, 0)
	ids := recommendationFixture(t, a)
	ctx := scopedContext(viewer, a.DB, vault.Grant{})
	query := dbgen.New(a.store(ctx))
	metadata, err := query.RecommendationPosts(ctx, dbgen.RecommendationPostsParams{ViewerID: myProfileID(t, viewer), PostIds: ids})
	if err != nil || len(metadata) != len(ids) {
		t.Fatal("missing eligible metadata", err)
	}
	for _, row := range metadata {
		if len(row.Data) != 0 || row.PublishedRevision != 1 || row.AuthorID == nil {
			t.Fatal("metadata read fetched content or lost selection fields")
		}
	}
	full, err := query.RecommendationPosts(ctx, dbgen.RecommendationPostsParams{ViewerID: myProfileID(t, viewer), PostIds: ids[:1], IncludeData: true})
	if err != nil || len(full) != 1 || len(full[0].Data) == 0 {
		t.Fatal("explicit payload read failed", err)
	}
}

// Change publication after metadata has been consumed and before payloads are
// requested. The real restricted-role payload query must recheck visibility and
// the handler must retain the snapshot's served revision.
func TestRecommendationHydrationRechecksPublication(t *testing.T) {
	for _, change := range []string{"withdraw", "revision", "author"} {
		t.Run(change, func(t *testing.T) {
			base := testApp(t)
			cfg := base.Config
			cfg.RecommendationMode, cfg.RecommendationRollout, cfg.RecommendationTarget = "serve", 100, ""
			cfg.RecommendationFeatureShadowRedisURL = ""
			a := cloneTestApp(t, cfg)
			ids := recommendationFixture(t, a)
			a.Ranker = testRanker(func(_ context.Context, request *pb.RecommendRequest) (*pb.RecommendResponse, error) {
				available := map[string]*pb.Candidate{}
				for _, candidate := range request.Candidates {
					available[candidate.Id] = candidate
				}
				result := &pb.RecommendResponse{ModelVersion: "rules-v1", PolicyVersion: "explicit-relevance-v1"}
				for _, id := range ids {
					if candidate := available[id.String()]; candidate != nil {
						result.Items = append(result.Items, &pb.RankedReference{Id: candidate.Id, Revision: candidate.Revision, Explanation: "RECENT_PUBLIC_POST"})
					}
				}
				return result, nil
			})
			hook := &hydrationQueryChange{change: func() error {
				if change == "author" {
					_, err := integrationAdmin.Exec(context.Background(), `UPDATE social.profile SET state='SUSPENDED' WHERE id=(SELECT author_id FROM social.post WHERE id=$1)`, ids[0])
					return err
				}
				if change == "withdraw" {
					_, err := integrationAdmin.Exec(context.Background(), `UPDATE social.post SET state='HIDDEN' WHERE id=$1`, ids[0])
					return err
				}
				if _, err := integrationAdmin.Exec(context.Background(), `INSERT INTO social.post_revision(post_id,revision,body,language_tag,review_state) VALUES($1,2,'Changed published body','en-IN','APPROVED')`, ids[0]); err != nil {
					return err
				}
				_, err := integrationAdmin.Exec(context.Background(), `UPDATE social.post SET current_revision=2,published_revision=2 WHERE id=$1`, ids[0])
				return err
			}}
			poolConfig := base.DB.Config()
			poolConfig.ConnConfig.Tracer = hook
			pool, err := pgxpool.NewWithConfig(context.Background(), poolConfig)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(pool.Close)
			a.DB = pool
			viewer := login(t, a, 0)
			profile := myProfileID(t, viewer)
			consentRecommendation(t, viewer, true)
			t.Cleanup(func() {
				integrationAdmin.Exec(context.Background(), `DELETE FROM social.recommendation_snapshot WHERE viewer_id=$1`, profile)
				integrationAdmin.Exec(context.Background(), `DELETE FROM social.recommendation_exposure WHERE profile_id=$1`, profile)
				integrationAdmin.Exec(context.Background(), `DELETE FROM social.recommendation_preference WHERE profile_id=$1`, profile)
			})
			page := recommendedPage(t, viewer, "")
			if !hook.changed || hook.err != nil || hook.metadataCalls != 1 || hook.payloadCalls != 1 || hook.maximumPayloadReferences > 20 {
				t.Fatal("hydration boundary was not exercised", fmt.Sprintf("%+v", hook))
			}
			posts := 0
			for _, item := range page.Items {
				if item.Type == "POST" {
					posts++
					if item.Post.ID == ids[0] || (change == "author" && item.Post.ID == ids[1]) {
						t.Fatal("revoked or different-revision content survived final hydration")
					}
				}
			}
			if posts == 0 {
				t.Fatal("unchanged eligible posts disappeared")
			}
			var exposures int
			unserved := []uuid.UUID{ids[0]}
			if change == "author" {
				unserved = append(unserved, ids[1])
			}
			if err := integrationAdmin.QueryRow(context.Background(), `SELECT count(*) FROM social.recommendation_exposure WHERE profile_id=$1 AND post_id=ANY($2)`, profile, unserved).Scan(&exposures); err != nil || exposures != 0 {
				t.Fatal("unserved revision received an exposure", err)
			}
		})
	}
}

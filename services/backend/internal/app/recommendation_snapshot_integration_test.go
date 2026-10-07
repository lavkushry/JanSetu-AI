package app

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lavkushry/JanSetu-AI/services/backend/internal/recommendation/pb"
	"github.com/lavkushry/JanSetu-AI/services/backend/internal/recommendation/snapshotcache"
	"github.com/redis/go-redis/v9"
)

type checkingSnapshotCache struct {
	snapshotcache.Store
	t      *testing.T
	viewer uuid.UUID
	scope  snapshotcache.Scope
	record snapshotcache.Record
	hits   int
}

func (c *checkingSnapshotCache) Load(ctx context.Context, scope snapshotcache.Scope) (snapshotcache.Record, error) {
	record, err := c.Store.Load(ctx, scope)
	if err == nil {
		c.hits++
	}
	return record, err
}

func (c *checkingSnapshotCache) Save(ctx context.Context, scope snapshotcache.Scope, record snapshotcache.Record) error {
	c.t.Helper()
	var committed bool
	if err := integrationAdmin.QueryRow(ctx, `SELECT EXISTS(SELECT FROM social.recommendation_snapshot WHERE id=$1 AND viewer_id=$2 AND generation=$3 AND query_key=$4)`, scope.Token, c.viewer, scope.Generation, scope.Query).Scan(&committed); err != nil || !committed {
		c.t.Fatal("snapshot cached before durable commit", committed, err)
	}
	c.scope, c.record = scope, record
	return c.Store.Save(ctx, scope, record)
}

func TestRecommendationRedisSnapshotReplayFallbackAndFencing(t *testing.T) {
	base := testApp(t)
	if os.Getenv("JANSETU_RECOMMENDATION_STREAM_PROOF") != "1" {
		t.Skip("run make recommendation-stream-proof for real Redis snapshot proof")
	}
	cfg := base.Config
	cfg.RecommendationTarget = ""
	cfg.RecommendationMode = "serve"
	cfg.RecommendationRollout = 100
	cfg.RecommendationSnapshotRedisURL = "redis://127.0.0.1:16379/0"
	a := cloneTestApp(t, cfg)
	t.Cleanup(func() { a.Snapshots.Close() })
	a.Ranker = testRanker(goldenRanker)
	owner := login(t, a, 0)
	viewer := parsed[struct{ Profile struct{ ID uuid.UUID } }](t, owner.request("GET", "me", nil, 0, "")).Profile.ID
	recommendationFixture(t, a)
	p := consentRecommendation(t, owner, true)
	t.Cleanup(func() { consentRecommendation(t, owner, false) })
	cache := &checkingSnapshotCache{Store: a.Snapshots, t: t, viewer: viewer}
	a.Snapshots = cache
	first := recommendedPage(t, owner, "")
	if first.NextCursor == nil {
		t.Fatal("missing snapshot cursor")
	}
	token := *first.NextCursor
	scope, record := cache.scope, cache.record
	ctx := context.Background()
	adminCache := redis.NewClient(&redis.Options{Addr: "127.0.0.1:16379"})
	defer adminCache.Close()
	keys, _, err := adminCache.Scan(ctx, 0, "jansetu:recommendation:snapshots:v1:*:"+token, 100).Result()
	if err != nil || len(keys) != 1 {
		t.Fatal("snapshot was not cached", keys, err)
	}
	key := keys[0]
	original, err := adminCache.Get(ctx, key).Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if deadline := adminCache.Do(ctx, "PEXPIRETIME", key).Val(); deadline != record.Expires.UnixMilli() {
		t.Fatal("cache did not preserve absolute snapshot expiry", deadline, record.Expires)
	}
	// A second API instance uses the same cache and frozen order, without ranking.
	replica := cloneTestApp(t, cfg)
	t.Cleanup(func() { replica.Snapshots.Close() })
	replica.Ranker = testRanker(func(context.Context, *pb.RecommendRequest) (*pb.RecommendResponse, error) {
		t.Fatal("cached cursor was reranked")
		return nil, nil
	})
	replicaOwner := login(t, replica, 0)
	second := recommendedPage(t, replicaOwner, token)
	if !reflect.DeepEqual(second, recommendedPage(t, owner, token)) || cache.hits == 0 {
		t.Fatal("cache replay changed references, explanations or exposures")
	}
	// Prove the hit avoids the PostgreSQL snapshot read, while still hydrating live content.
	if _, err := integrationAdmin.Exec(ctx, "DELETE FROM social.recommendation_snapshot WHERE id=$1", scope.Token); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(second, recommendedPage(t, replicaOwner, token)) {
		t.Fatal("cache hit depended on durable snapshot read")
	}
	if _, err := integrationAdmin.Exec(ctx, `INSERT INTO social.recommendation_snapshot(id,viewer_id,query_key,generation,expires_at,payload) VALUES($1,$2,$3,$4,$5,$6)`, scope.Token, viewer, scope.Query, scope.Generation, record.Expires, record.Payload); err != nil {
		t.Fatal(err)
	}
	// Loss of the cache reads the durable record and fills Redis after successful hydration.
	if err := adminCache.Del(ctx, key).Err(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(second, recommendedPage(t, owner, token)) || adminCache.Exists(ctx, key).Val() != 1 {
		t.Fatal("cache miss did not recover the frozen page")
	}
	// Scope/schema/expiry corruption is an optional-cache miss, never a broken feed.
	for name, corrupt := range map[string]func(map[string]any){
		"unknown field": func(e map[string]any) { e["privateEvidence"] = "forbidden" },
		"generation":    func(e map[string]any) { e["generation"] = p.Generation + 1 },
		"viewer query":  func(e map[string]any) { e["queryDigest"] = "another viewer" },
		"token":         func(e map[string]any) { e["token"] = uuid.NewString() },
		"expiry":        func(e map[string]any) { e["expires"] = time.Now().Add(-time.Minute).Format(time.RFC3339Nano) },
		"snapshot root": func(e map[string]any) { e["payload"].(map[string]any)["snapshotId"] = uuid.NewString() },
		"scan offset":   func(e map[string]any) { e["payload"].(map[string]any)["postOffset"] = -1 },
		"reference reason": func(e map[string]any) {
			e["payload"].(map[string]any)["posts"].([]any)[0].(map[string]any)["explanation"] = "PRIVATE_REPORT"
		},
		"duplicate exposure": func(e map[string]any) {
			payload := e["payload"].(map[string]any)
			posts := payload["posts"].([]any)
			offset := int(payload["postOffset"].(float64))
			posts[offset+1].(map[string]any)["exposure"] = posts[offset].(map[string]any)["exposure"]
		},
	} {
		t.Run(name, func(t *testing.T) {
			var envelope map[string]any
			if err := json.Unmarshal(original, &envelope); err != nil {
				t.Fatal(err)
			}
			corrupt(envelope)
			data, err := json.Marshal(envelope)
			if err != nil {
				t.Fatal(err)
			}
			if err := adminCache.SetArgs(ctx, key, data, redis.SetArgs{ExpireAt: record.Expires}).Err(); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(second, recommendedPage(t, owner, token)) {
				t.Fatal("corrupt cache changed feed")
			}
		})
	}
	if err := adminCache.SetArgs(ctx, key, original, redis.SetArgs{ExpireAt: record.Expires}).Err(); err != nil {
		t.Fatal(err)
	}
	// A cache outage preserves both the page and its opaque next cursor via PostgreSQL.
	failed, err := snapshotcache.New(cfg.RecommendationSnapshotRedisURL)
	if err != nil {
		t.Fatal(err)
	}
	failed.Close()
	a.Snapshots = failed
	if !reflect.DeepEqual(second, recommendedPage(t, owner, token)) {
		t.Fatal("cache outage changed frozen page")
	}
	outageFirst := recommendedPage(t, owner, "")
	if outageFirst.NextCursor == nil {
		t.Fatal("cache write outage lost durable cursor")
	}
	recommendedPage(t, owner, *outageFirst.NextCursor)
	a.Snapshots = cache
	// Authenticated viewer/query bindings cannot be bypassed by an existing cache entry.
	other := login(t, a, 1)
	mustStatus(t, other.request("GET", "feed?sort=recommended&cursor="+token, nil, 0, ""), 410)
	mustStatus(t, owner.request("GET", "feed?sort=recommended&communityId="+uuid.NewString()+"&cursor="+token, nil, 0, ""), 410)
	// Cached references still obey publication, blocks, revision changes and feedback.
	var snapshot recommendationSnapshot
	if err := json.Unmarshal(record.Payload, &snapshot); err != nil {
		t.Fatal(err)
	}
	unseen := snapshot.Posts[snapshot.PostOffset:]
	if len(unseen) < 5 {
		t.Fatal("insufficient cached candidates")
	}
	hidden, revised, blocked := unseen[0].ID, unseen[1].ID, unseen[2].ID
	if _, err := integrationAdmin.Exec(ctx, "UPDATE social.post SET state='HIDDEN' WHERE id=$1", hidden); err != nil {
		t.Fatal(err)
	}
	if _, err := integrationAdmin.Exec(ctx, `INSERT INTO social.post_revision(post_id,revision,body,language_tag,review_state) VALUES($1,2,'New approved revision','en-IN','APPROVED')`, revised); err != nil {
		t.Fatal(err)
	}
	if _, err := integrationAdmin.Exec(ctx, "UPDATE social.post SET published_revision=2 WHERE id=$1", revised); err != nil {
		t.Fatal(err)
	}
	var author uuid.UUID
	if err := integrationAdmin.QueryRow(ctx, "SELECT author_id FROM social.post WHERE id=$1", blocked).Scan(&author); err != nil {
		t.Fatal(err)
	}
	flag(t, owner, "blocks", author, true)
	t.Cleanup(func() {
		integrationAdmin.Exec(ctx, "DELETE FROM social.profile_block WHERE blocker_id=$1 AND blocked_id=$2", viewer, author)
	})
	visible := recommendedPage(t, owner, token)
	var less, muted uuid.UUID
	for _, item := range visible.Items {
		if item.Type == "POST" && (item.Post.ID == hidden || item.Post.ID == revised || item.Post.ID == blocked) {
			t.Fatal("cache bypassed live visibility or revision")
		}
		if item.Type == "POST" && less == uuid.Nil {
			less = item.Post.ID
			mustStatus(t, owner.request("POST", "me/recommendation-events", map[string]any{"eventId": uuid.New(), "exposureId": item.Recommendation.ExposureID, "kind": "LESS"}, 0, ""), 200)
		} else if item.Type == "POST" && item.Post.Author != nil && muted == uuid.Nil {
			muted = item.Post.Author.ID
			mustStatus(t, owner.request("PUT", "me/mutes", map[string]any{"targetType": "PROFILE", "targetId": muted, "active": true}, 0, ""), 200)
		}
	}
	if less == uuid.Nil || muted == uuid.Nil {
		t.Fatal("missing candidates for current controls proof")
	}
	t.Cleanup(func() {
		integrationAdmin.Exec(ctx, "DELETE FROM social.mute WHERE profile_id=$1 AND muted_profile_id=$2", viewer, muted)
	})
	for _, item := range recommendedPage(t, owner, token).Items {
		if item.Type == "POST" && (item.Post.ID == less || (item.Post.Author != nil && item.Post.Author.ID == muted)) {
			t.Fatal("cached page ignored immediate Less/mute controls")
		}
	}
	// Reset rejects the cursor while the unmodified old-generation cache entry remains.
	mustStatus(t, owner.request("POST", "me/recommendation-history/reset", nil, p.Version, ""), 200)
	if adminCache.Exists(ctx, key).Val() != 1 {
		t.Fatal("reset proof needs a stale cache entry")
	}
	mustStatus(t, owner.request("GET", "feed?sort=recommended&cursor="+token, nil, 0, ""), 410)
	refreshed := recommendedPage(t, owner, "")
	if refreshed.NextCursor == nil {
		t.Fatal("missing post-reset cursor")
	}
	withdrawalScope := cache.scope
	consentRecommendation(t, owner, false)
	if _, err := cache.Store.Load(ctx, withdrawalScope); err != nil {
		t.Fatal("withdrawal proof needs a stale cache entry", err)
	}
	mustStatus(t, owner.request("GET", "feed?sort=recommended&cursor="+*refreshed.NextCursor, nil, 0, ""), 410)
	// Anonymous cursors also bind to the browser cookie when fetched from Redis.
	request := httptest.NewRequest("GET", "/v1/feed?sort=recommended", nil)
	response := httptest.NewRecorder()
	replica.Ranker = nil
	replica.Handler().ServeHTTP(response, request)
	mustStatus(t, response, 200)
	guest := parsed[recommendationPage](t, response)
	if guest.NextCursor == nil {
		t.Fatal("missing anonymous cursor")
	}
	cookies := response.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("missing anonymous binding")
	}
	for _, bound := range []bool{true, false} {
		request = httptest.NewRequest("GET", "/v1/feed?sort=recommended&cursor="+*guest.NextCursor, nil)
		if bound {
			request.AddCookie(cookies[0])
		}
		response = httptest.NewRecorder()
		replica.Handler().ServeHTTP(response, request)
		status := 410
		if bound {
			status = 200
		}
		mustStatus(t, response, status)
	}
}

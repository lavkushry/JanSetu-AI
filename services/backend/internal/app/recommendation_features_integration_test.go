package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lavkushry/JanSetu-AI/services/backend/internal/recommendation/features"
	"github.com/lavkushry/JanSetu-AI/services/backend/internal/recommendation/stream"
	"github.com/lavkushry/JanSetu-AI/services/backend/internal/vault"
	"github.com/redis/go-redis/v9"
	"github.com/twmb/franz-go/pkg/kgo"
)

type featureReadHook struct {
	features.Reader
	load func(context.Context, features.Scope, []features.Reference) ([]features.Observation, error)
}

func (h featureReadHook) Load(ctx context.Context, s features.Scope, refs []features.Reference) ([]features.Observation, error) {
	return h.load(ctx, s, refs)
}

func featureProofApp(t *testing.T) (*App, *redis.Client) {
	t.Helper()
	base := testApp(t)
	rawURL := os.Getenv("JANSETU_FEATURE_PROOF_REDIS_URL")
	if rawURL == "" {
		t.Skip("run make recommendation-feature-proof for isolated Redis feature parity")
	}
	cfg := base.Config
	cfg.RecommendationTarget = ""
	cfg.RecommendationMode = "off"
	cfg.RecommendationFeatureShadowRedisURL = rawURL
	a := cloneTestApp(t, cfg)
	t.Cleanup(func() { a.FeatureShadow.Close() })
	opts, err := redis.ParseURL(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	cache := redis.NewClient(opts)
	t.Cleanup(func() { cache.Close() })
	return a, cache
}

func projectFeatureRecords(t *testing.T, db *pgxpool.Pool, cache *redis.Client, records []stream.Envelope) {
	t.Helper()
	for _, e := range records {
		data, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		err = stream.ProjectRecord(context.Background(), db, stream.Projection{Redis: cache, Namespace: features.Namespace}, &kgo.Record{Key: []byte(e.Key()), Value: data})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func requireFeatureParity(t *testing.T, r featureParityResult, count int) {
	t.Helper()
	if r.Status != "compared" || r.Expected != count || r.Actual != count || r.Report != (features.Report{Matched: count}) {
		t.Fatal("feature parity mismatch", r)
	}
}

func TestRecommendationFeatureLedgerParityReplayAndEligibility(t *testing.T) {
	a, cache := featureProofApp(t)
	owner := login(t, a, 0)
	viewer := myProfileID(t, owner)
	recommendationFixture(t, a)
	p := consentRecommendation(t, owner, true)
	t.Cleanup(func() { consentRecommendation(t, owner, false) })
	page := recommendedPage(t, owner, "")
	refs := []features.Reference{}
	var exposure, author uuid.UUID
	for _, item := range page.Items {
		if item.Type != "POST" || item.Recommendation.ExposureID == uuid.Nil {
			continue
		}
		refs = append(refs, features.Reference{PostID: item.Post.ID, Revision: 1})
		if len(refs) == 1 {
			exposure, author = item.Recommendation.ExposureID, item.Post.Author.ID
		}
		if len(refs) == 2 {
			break
		}
	}
	if len(refs) != 2 || page.NextCursor == nil {
		t.Fatal("missing consenting fixture page")
	}
	for _, action := range features.Actions {
		body := map[string]any{"eventId": uuid.New(), "exposureId": exposure, "kind": action}
		if action == "READ" {
			body["activeMilliseconds"] = 1000
		}
		mustStatus(t, owner.request("POST", "me/recommendation-events", body, 0, ""), 200)
	}
	ctx := scopedContext(owner, a.DB, vault.Grant{})
	if r := a.recommendationFeatureParity(ctx, viewer, p.Generation, refs); r.Status != "projection_unavailable" || r.Expected != 6 {
		t.Fatal("missing projection was treated as parity", r)
	}
	db := streamPool(t)
	producer := &recordingStreamProducer{}
	drainRecommendationStream(t, db, producer)
	// Bootstrap controls first, then simulate a scalar-only worker having already
	// seen an interaction. The new observation dedup set still allows replay.
	for _, e := range producer.items {
		if e.EventType == "CONTROL" {
			projectFeatureRecords(t, db, cache, []stream.Envelope{e})
		}
	}
	var read stream.Envelope
	for _, e := range producer.items {
		if e.EventType == "INTERACTION" && e.Action == "READ" && e.PostID == refs[0].PostID {
			read = e
		}
	}
	if read.EventID == uuid.Nil {
		t.Fatal("reading event not exported")
	}
	key := features.Namespace + ":{" + read.Key() + "}"
	if err := cache.HSet(context.Background(), key+":seen", read.EventID.String(), 1).Err(); err != nil {
		t.Fatal(err)
	}
	projectFeatureRecords(t, db, cache, producer.items)
	requireFeatureParity(t, a.recommendationFeatureParity(ctx, viewer, p.Generation, refs), 6)
	projectFeatureRecords(t, db, cache, producer.items)
	requireFeatureParity(t, a.recommendationFeatureParity(ctx, viewer, p.Generation, refs), 6)

	field := features.Field("READ", refs[0])
	original, err := cache.HGet(context.Background(), key+":observations", field).Bytes()
	if err != nil {
		t.Fatal(err)
	}
	observation, err := features.Decode(original)
	if err != nil {
		t.Fatal(err)
	}
	observation.NormalizedRead = .9
	modified, _ := json.Marshal(observation)
	cache.HSet(context.Background(), key+":observations", field, modified)
	if r := a.recommendationFeatureParity(ctx, viewer, p.Generation, refs); r.Report != (features.Report{Matched: 5, Different: 1}) {
		t.Fatal("changed reading feature was undetected", r)
	}
	cache.HDel(context.Background(), key+":observations", field)
	if r := a.recommendationFeatureParity(ctx, viewer, p.Generation, refs); r.Report != (features.Report{Matched: 5, Missing: 1}) {
		t.Fatal("missing reading feature was undetected", r)
	}
	cache.HSet(context.Background(), key+":observations", field, original)
	extra := observation
	extra.PostID, extra.EventID, extra.Action, extra.NormalizedRead = refs[1].PostID, uuid.New(), "MORE", 0
	extra.Order = extra.OrderKey()
	data, _ := json.Marshal(extra)
	cache.HSet(context.Background(), key+":observations", extra.Field(), data)
	if r := a.recommendationFeatureParity(ctx, viewer, p.Generation, refs); r.Report != (features.Report{Matched: 6, Extra: 1}) {
		t.Fatal("unexpected feature was undetected", r)
	}
	cache.HDel(context.Background(), key+":observations", extra.Field())
	// Optional shadow failures cannot affect frozen pagination or exposures.
	stable := recommendedPage(t, owner, *page.NextCursor)
	reader := a.FeatureShadow
	a.FeatureShadow = featureReadHook{Reader: reader, load: func(context.Context, features.Scope, []features.Reference) ([]features.Observation, error) {
		return nil, errors.New("synthetic Redis outage")
	}}
	if !reflect.DeepEqual(stable, recommendedPage(t, owner, *page.NextCursor)) {
		t.Fatal("feature outage changed snapshot feed")
	}
	a.FeatureShadow = reader
	cache.HSet(context.Background(), key+":observations", field, `{"privateOCR":"forbidden"}`)
	if r := a.recommendationFeatureParity(ctx, viewer, p.Generation, refs); r.Status != "projection_unavailable" {
		t.Fatal("malformed cache feature accepted", r)
	}
	cache.HSet(context.Background(), key+":observations", field, original)

	// Authorization is rechecked independently of the previously served exposure.
	flag(t, owner, "blocks", author, true)
	if r := a.recommendationFeatureParity(ctx, viewer, p.Generation, refs[:1]); r.Status != "skipped" || r.Expected != 0 {
		t.Fatal("blocked author entered parity sample", r)
	}
	flag(t, owner, "blocks", author, false)
	mustStatus(t, owner.request("PUT", "me/mutes", map[string]any{"targetType": "PROFILE", "targetId": author, "active": true}, 0, ""), 200)
	if r := a.recommendationFeatureParity(ctx, viewer, p.Generation, refs[:1]); r.Status != "skipped" {
		t.Fatal("muted author entered parity sample", r)
	}
	mustStatus(t, owner.request("PUT", "me/mutes", map[string]any{"targetType": "PROFILE", "targetId": author, "active": false}, 0, ""), 200)
	other := login(t, a, 1)
	if r := a.recommendationFeatureParity(scopedContext(other, a.DB, vault.Grant{}), viewer, p.Generation, refs); r.Status != "skipped" {
		t.Fatal("foreign session sampled owner ledger", r)
	}
	if _, err := integrationAdmin.Exec(context.Background(), `UPDATE social.post SET state='HIDDEN',version=version+1 WHERE id=$1`, refs[0].PostID); err != nil {
		t.Fatal(err)
	}
	if r := a.recommendationFeatureParity(ctx, viewer, p.Generation, refs[:1]); r.Status != "skipped" {
		t.Fatal("revoked publication entered parity sample", r)
	}
	if _, err := integrationAdmin.Exec(context.Background(), `INSERT INTO social.post_revision(post_id,revision,body,language_tag,review_state) VALUES($1,2,'Revised published fixture','en-IN','APPROVED')`, refs[0].PostID); err != nil {
		t.Fatal(err)
	}
	if _, err := integrationAdmin.Exec(context.Background(), `UPDATE social.post SET state='PUBLISHED',published_revision=2,version=version+1 WHERE id=$1`, refs[0].PostID); err != nil {
		t.Fatal(err)
	}
	if r := a.recommendationFeatureParity(ctx, viewer, p.Generation, refs[:1]); r.Status != "skipped" {
		t.Fatal("stale revision entered parity sample", r)
	}
	refs[0].Revision = 2
	requireFeatureParity(t, a.recommendationFeatureParity(ctx, viewer, p.Generation, refs[:1]), 0)
}

func TestRecommendationFeatureResetDuringReadAndReplay(t *testing.T) {
	a, cache := featureProofApp(t)
	owner := login(t, a, 0)
	viewer := myProfileID(t, owner)
	recommendationFixture(t, a)
	p := consentRecommendation(t, owner, true)
	t.Cleanup(func() { consentRecommendation(t, owner, false) })
	page := recommendedPage(t, owner, "")
	var ref features.Reference
	var exposure uuid.UUID
	for _, item := range page.Items {
		if item.Type == "POST" && item.Recommendation.ExposureID != uuid.Nil {
			ref = features.Reference{PostID: item.Post.ID, Revision: 1}
			exposure = item.Recommendation.ExposureID
			break
		}
	}
	mustStatus(t, owner.request("POST", "me/recommendation-events", map[string]any{"eventId": uuid.New(), "exposureId": exposure, "kind": "MORE"}, 0, ""), 200)
	db := streamPool(t)
	producer := &recordingStreamProducer{}
	drainRecommendationStream(t, db, producer)
	projectFeatureRecords(t, db, cache, producer.items)
	ctx := scopedContext(owner, a.DB, vault.Grant{})
	refs := []features.Reference{ref}
	requireFeatureParity(t, a.recommendationFeatureParity(ctx, viewer, p.Generation, refs), 1)
	reader := a.FeatureShadow
	var oldScope features.Scope
	a.FeatureShadow = featureReadHook{Reader: reader, load: func(ctx context.Context, s features.Scope, refs []features.Reference) ([]features.Observation, error) {
		oldScope = s
		observations, err := reader.Load(ctx, s, refs)
		mustStatus(t, owner.request("POST", "me/recommendation-history/reset", nil, p.Version, ""), 200)
		return observations, err
	}}
	if r := a.recommendationFeatureParity(ctx, viewer, p.Generation, refs); r.Status != "generation_changed" || r.Expected != 0 || r.Actual != 0 {
		t.Fatal("in-flight reset retained behavioral sample", r)
	}
	a.FeatureShadow = reader
	// A replayed pre-reset record consults live authority and clears old state,
	// even if the new reset control has not been consumed yet.
	projectFeatureRecords(t, db, cache, producer.items)
	if _, err := reader.Load(context.Background(), oldScope, refs); !errors.Is(err, features.ErrUnavailable) {
		t.Fatal("old generation readable after replay", err)
	}
	current := recommendationPrefs(t, owner)
	requireFeatureParity(t, a.recommendationFeatureParity(ctx, viewer, current.Generation, refs), 0)
	consentRecommendation(t, owner, false)
	a.FeatureShadow = featureReadHook{Reader: reader, load: func(context.Context, features.Scope, []features.Reference) ([]features.Observation, error) {
		t.Fatal("withdrawn consent read Redis features")
		return nil, nil
	}}
	if r := a.recommendationFeatureParity(ctx, viewer, current.Generation, refs); r.Status != "skipped" {
		t.Fatal("withdrawal entered shadow sample", r)
	}
	recommendedPage(t, client{app: a}, "")
	a.FeatureShadow = reader
}

func TestRecommendationFeatureOrderingRetentionAndBounds(t *testing.T) {
	a, cache := featureProofApp(t)
	reader := a.FeatureShadow
	ctx := context.Background()
	subject, post := uuid.New(), uuid.New()
	generation := int64(9007199254740993)
	key := features.Namespace + ":{viewer:" + subject.String() + "}"
	projection := stream.Projection{Redis: cache, Namespace: features.Namespace}
	authority := stream.Authority{Generation: generation, Enabled: true}
	now := time.Now().UTC().Truncate(time.Microsecond)
	event := stream.Envelope{SchemaVersion: 1, EventID: uuid.MustParse("00000000-0000-0000-0000-000000000002"), EventType: "INTERACTION", EntityVersion: 1, Subject: subject, Generation: generation, PostID: post, Revision: 1, Action: "READ", OccurredAt: now.Add(-time.Hour), NormalizedRead: .7}
	apply := func(e stream.Envelope) {
		t.Helper()
		if err := projection.Apply(ctx, e, authority); err != nil {
			t.Fatal(err)
		}
	}
	apply(event)
	older := event
	older.EventID = uuid.New()
	older.OccurredAt = event.OccurredAt.Add(-time.Second)
	older.NormalizedRead = .9
	apply(older)
	tie := event
	tie.EventID = uuid.MustParse("00000000-0000-0000-0000-000000000001")
	tie.NormalizedRead = .3
	apply(tie)
	scope := features.Scope{Subject: subject, Generation: generation, Enabled: true, AsOf: now}
	refs := []features.Reference{{PostID: post, Revision: 1}}
	items, err := reader.Load(ctx, scope, refs)
	if err != nil || len(items) != 1 || items[0].EventID != event.EventID || items[0].NormalizedRead != .7 {
		t.Fatal("out-of-order or tied event replaced latest", items, err)
	}
	field := items[0].Field()
	expiry, err := cache.HPExpireTime(ctx, key+":observations", field).Result()
	if err != nil || len(expiry) != 1 || expiry[0] != event.OccurredAt.Add(features.Retention).UnixMilli() {
		t.Fatal("observation lacks original absolute retention", expiry, err)
	}
	apply(event)
	other := event
	other.EventID, other.Action, other.NormalizedRead, other.OccurredAt = uuid.New(), "MORE", 0, now.Add(-time.Minute)
	apply(other)
	expiryAfter, err := cache.HPExpireTime(ctx, key+":observations", field).Result()
	if err != nil || !reflect.DeepEqual(expiry, expiryAfter) {
		t.Fatal("replay or unrelated activity extended retention", expiryAfter, err)
	}
	revision := event
	revision.EventID, revision.Revision, revision.NormalizedRead = uuid.New(), 2, .1
	apply(revision)
	items, err = reader.Load(ctx, scope, []features.Reference{{PostID: post, Revision: 2}})
	if err != nil || len(items) != 1 || items[0].EventID != revision.EventID {
		t.Fatal("feature read crossed revision boundary", items, err)
	}
	expired := event
	expired.EventID, expired.Action, expired.NormalizedRead = uuid.New(), "LESS", 0
	expired.OccurredAt = now.Add(-features.Retention - time.Second)
	apply(expired)
	if cache.HExists(ctx, key+":observations", features.Field("LESS", refs[0])).Val() {
		t.Fatal("expired event resurrected an observation")
	}
	scope.Generation++
	if _, err := reader.Load(ctx, scope, refs); !errors.Is(err, features.ErrUnavailable) {
		t.Fatal("bigint generation rounded to neighboring generation", err)
	}
	scope.Generation--
	scope.Enabled = false
	if _, err := reader.Load(ctx, scope, refs); !errors.Is(err, features.ErrUnavailable) {
		t.Fatal("reader accepted disabled consent", err)
	}
	scope.Enabled = true
	if _, err := reader.Load(ctx, scope, make([]features.Reference, features.MaxReferences+1)); err == nil {
		t.Fatal("unbounded feature read accepted")
	}
	cache.HDel(ctx, key+":state", "observationSchema")
	if _, err := reader.Load(ctx, scope, refs); !errors.Is(err, features.ErrUnavailable) {
		t.Fatal("legacy scalar state treated as revision-aware coverage", err)
	}
	apply(event)
	// A saturated dedup budget excludes new deltas rather than growing unbounded.
	remaining := 10000 - int(cache.HLen(ctx, key+":observation-seen").Val())
	values := make([]any, 0, 2*remaining)
	for i := 0; i < remaining; i++ {
		values = append(values, "fixture-"+strconv.Itoa(i), 1)
	}
	cache.HSet(ctx, key+":observation-seen", values...)
	bounded := event
	bounded.EventID, bounded.Action, bounded.NormalizedRead = uuid.New(), "SATISFIED", 0
	apply(bounded)
	if cache.HExists(ctx, key+":observations", features.Field("SATISFIED", refs[0])).Val() {
		t.Fatal("projection exceeded event budget")
	}
	// Metadata loss fails closed. A new writer rebuilds its fence and discards
	// observations whose generation cannot be established.
	cache.Del(ctx, key+":state")
	apply(bounded)
	items, err = reader.Load(ctx, scope, refs)
	if err != nil || len(items) != 1 || items[0].Action != "SATISFIED" {
		t.Fatal("missing generation metadata preserved unverified history", items, err)
	}
}

package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lavkushry/JanSetu-AI/services/backend/internal/platform"
	"github.com/lavkushry/JanSetu-AI/services/backend/internal/recommendation/stream"
	"github.com/redis/go-redis/v9"
	"github.com/twmb/franz-go/pkg/kgo"
)

type recordingStreamProducer struct {
	items []stream.Envelope
	fail  bool
}

func (p *recordingStreamProducer) Publish(_ context.Context, key string, data []byte) error {
	if p.fail {
		return errors.New("synthetic Kafka outage")
	}
	e, err := stream.Decode(data)
	if err != nil {
		return err
	}
	if e.Key() != key {
		return errors.New("wrong partition key")
	}
	p.items = append(p.items, e)
	return nil
}

func streamPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool, err := platform.RuntimePool(context.Background(), roleURL(integrationAdmin.Config().ConnString(), "js_recommendation_stream"), "js_recommendation_stream")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}
func drainRecommendationStream(t *testing.T, db *pgxpool.Pool, p stream.Producer) {
	t.Helper()
	for i := 0; i < 100; i++ {
		n, err := stream.PublishBatch(context.Background(), db, p)
		if err != nil {
			t.Fatal(err)
		}
		if n == 0 {
			return
		}
	}
	t.Fatal("stream did not drain")
}

func TestRecommendationStreamOutboxAndFencing(t *testing.T) {
	a := testApp(t)
	owner := login(t, a, 0)
	viewer := parsed[struct{ Profile struct{ ID uuid.UUID } }](t, owner.request("GET", "me", nil, 0, "")).Profile.ID
	recommendationFixture(t, a)
	p := consentRecommendation(t, owner, true)
	t.Cleanup(func() { consentRecommendation(t, owner, false) })
	db := streamPool(t)
	ctx := context.Background()
	// No operational outbox, vault, personal ledger or raw published text access.
	for _, table := range []string{"infra.outbox", "ops.report", "identity.principal", "social.recommendation_event", "social.post_revision", "rec_stream.outbox", "rec_stream.authority"} {
		if _, err := db.Exec(ctx, "SELECT * FROM "+table+" LIMIT 1"); err == nil {
			t.Fatal("stream role can read", table)
		}
	}
	var vaultAccess bool
	if err := db.QueryRow(ctx, "SELECT has_database_privilege(current_user,$1,'CONNECT')", integrationVaultAdmin.Config().ConnConfig.Database).Scan(&vaultAccess); err != nil || vaultAccess {
		t.Fatal("stream role can connect to vault", err)
	}
	var subject uuid.UUID
	if err := integrationAdmin.QueryRow(ctx, "SELECT stream_subject FROM social.recommendation_preference WHERE profile_id=$1", viewer).Scan(&subject); err != nil {
		t.Fatal(err)
	}
	authority, err := stream.CurrentAuthority(ctx, db, subject)
	if err != nil || !authority.Enabled || authority.Generation != p.Generation {
		t.Fatal("authority mismatch", authority, err)
	}
	producer := &recordingStreamProducer{fail: true}
	if _, err := stream.PublishBatch(ctx, db, producer); err == nil {
		t.Fatal("outage silently acknowledged")
	}
	var pending int
	if err := integrationAdmin.QueryRow(ctx, "SELECT count(*) FROM rec_stream.outbox WHERE delivered_at IS NULL AND attempts>0").Scan(&pending); err != nil || pending == 0 {
		t.Fatal("lost failed records", err)
	}
	integrationAdmin.Exec(ctx, "UPDATE rec_stream.outbox SET available_at=statement_timestamp() WHERE delivered_at IS NULL")
	producer.fail = false
	drainRecommendationStream(t, db, producer)
	page := recommendedPage(t, owner, "")
	var exposure, post uuid.UUID
	for _, item := range page.Items {
		if item.Type == "POST" && item.Recommendation.ExposureID != uuid.Nil {
			exposure = item.Recommendation.ExposureID
			post = item.Post.ID
			break
		}
	}
	if exposure == uuid.Nil {
		t.Fatal("no consenting exposure")
	}
	event := uuid.New()
	body := map[string]any{"eventId": event, "exposureId": exposure, "kind": "MORE"}
	mustStatus(t, owner.request("POST", "me/recommendation-events", body, 0, ""), 200)
	mustStatus(t, owner.request("POST", "me/recommendation-events", body, 0, ""), 200)
	var count int
	if err := integrationAdmin.QueryRow(ctx, "SELECT count(*) FROM rec_stream.outbox WHERE id=$1", event).Scan(&count); err != nil || count != 1 {
		t.Fatal("duplicate stream write", count, err)
	}
	// The API write and stream event roll back together; no partial export.
	tx, err := integrationAdmin.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rollbackEvent := uuid.New()
	_, err = tx.Exec(ctx, `INSERT INTO social.recommendation_event(id,profile_id,exposure_id,generation,kind,request_hash) VALUES($1,$2,$3,$4,'SATISFIED','\x00')`, rollbackEvent, viewer, exposure, p.Generation)
	if err != nil {
		t.Fatal(err)
	}
	tx.Rollback(ctx)
	integrationAdmin.QueryRow(ctx, "SELECT count(*) FROM rec_stream.outbox WHERE id=$1", rollbackEvent).Scan(&count)
	if count != 0 {
		t.Fatal("rolled back event exported")
	}
	// A lease cannot be acknowledged after expiry or by an old publisher token.
	first, second := uuid.New(), uuid.New()
	var id uuid.UUID
	if err := db.QueryRow(ctx, "SELECT id FROM rec_stream.claim($1,1)", first).Scan(&id); err != nil || id != event {
		t.Fatal("event lease", id, err)
	}
	integrationAdmin.Exec(ctx, "UPDATE rec_stream.outbox SET lease_until=statement_timestamp()-interval '1 second' WHERE id=$1", event)
	if err := db.QueryRow(ctx, "SELECT id FROM rec_stream.claim($1,1)", second).Scan(&id); err != nil || id != event {
		t.Fatal("expired lease not replayed", err)
	}
	var ack bool
	db.QueryRow(ctx, "SELECT rec_stream.ack($1,$2)", event, first).Scan(&ack)
	if ack {
		t.Fatal("old lease acknowledged")
	}
	db.Exec(ctx, "SELECT rec_stream.retry($1,$2)", event, second)
	// Revocation before delivery deletes pending behavior and emits a newer control.
	mustStatus(t, owner.request("POST", "me/recommendation-history/reset", nil, p.Version, ""), 200)
	integrationAdmin.QueryRow(ctx, "SELECT count(*) FROM rec_stream.outbox WHERE id=$1", event).Scan(&count)
	if count != 0 {
		t.Fatal("reset left pending behavior")
	}
	drainRecommendationStream(t, db, producer)
	for _, item := range producer.items {
		if item.EventID == event {
			t.Fatal("stale behavior exported")
		}
		if item.EventType == "CONTENT" && item.PostID == post && item.Eligible && item.Revision != 1 {
			t.Fatal("wrong published reference")
		}
	}
	page = recommendedPage(t, owner, "")
	for _, item := range page.Items {
		if item.Type == "POST" && item.Recommendation.ExposureID != uuid.Nil {
			exposure, post = item.Recommendation.ExposureID, item.Post.ID
			break
		}
	}
	revokedEvent := uuid.New()
	mustStatus(t, owner.request("POST", "me/recommendation-events", map[string]any{"eventId": revokedEvent, "exposureId": exposure, "kind": "MORE"}, 0, ""), 200)
	if _, err := integrationAdmin.Exec(ctx, "UPDATE social.post SET state='HIDDEN',version=version+1 WHERE id=$1", post); err != nil {
		t.Fatal(err)
	}
	drainRecommendationStream(t, db, producer)
	revocation := false
	for _, item := range producer.items {
		if item.EventID == revokedEvent {
			t.Fatal("withdrawn revision exported as behavior")
		}
		if item.EventType == "CONTENT" && item.PostID == post && !item.Eligible {
			revocation = true
		}
	}
	if !revocation {
		t.Fatal("missing content revocation")
	}
	// The fixed janitor removes undelivered expired behavior even with Kafka off.
	expiredEvent := uuid.New()
	if _, err := integrationAdmin.Exec(ctx, `INSERT INTO rec_stream.outbox(id,aggregate_key,event_type,payload,created_at) VALUES($1,'retention-proof','INTERACTION','{}',statement_timestamp()-interval '31 days')`, expiredEvent); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Worker.Exec(ctx, "SELECT rec_stream.expire()"); err != nil {
		t.Fatal(err)
	}
	if err := integrationAdmin.QueryRow(ctx, "SELECT count(*) FROM rec_stream.outbox WHERE id=$1", expiredEvent).Scan(&count); err != nil || count != 0 {
		t.Fatal("undelivered expired behavior retained", err)
	}
	// An actual profile deletion leaves an irreversible pseudonymous tombstone.
	profile := uuid.New()
	if _, err := integrationAdmin.Exec(ctx, `INSERT INTO social.profile(id,handle,display_name,state) VALUES($1,$2,'Stream deletion fixture','ACTIVE')`, profile, "stream_"+profile.String()[:12]); err != nil {
		t.Fatal(err)
	}
	if _, err := integrationAdmin.Exec(ctx, `INSERT INTO social.recommendation_preference(profile_id,personalization_enabled) VALUES($1,true) RETURNING stream_subject`, profile); err != nil {
		t.Fatal(err)
	}
	integrationAdmin.QueryRow(ctx, "SELECT stream_subject FROM social.recommendation_preference WHERE profile_id=$1", profile).Scan(&subject)
	if _, err := integrationAdmin.Exec(ctx, "DELETE FROM social.profile WHERE id=$1", profile); err != nil {
		t.Fatal(err)
	}
	authority, err = stream.CurrentAuthority(ctx, db, subject)
	if err != nil || authority.Enabled || !authority.Deleted || authority.Generation != 2 {
		t.Fatal("deletion tombstone missing", authority, err)
	}
}

func TestRecommendationStreamKafkaRedisReplay(t *testing.T) {
	a := testApp(t)
	if os.Getenv("JANSETU_RECOMMENDATION_STREAM_PROOF") != "1" {
		t.Skip("run make recommendation-stream-proof for isolated Kafka/Redis proof")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	owner := login(t, a, 0)
	viewer := parsed[struct{ Profile struct{ ID uuid.UUID } }](t, owner.request("GET", "me", nil, 0, "")).Profile.ID
	recommendationFixture(t, a)
	consentRecommendation(t, owner, true)
	t.Cleanup(func() { consentRecommendation(t, owner, false) })
	db := streamPool(t)
	cache := redis.NewClient(&redis.Options{Addr: "127.0.0.1:16379"})
	defer cache.Close()
	if err := cache.Ping(ctx).Err(); err != nil {
		t.Fatal(err)
	}
	namespace := "proof:" + uuid.NewString()
	projection := stream.Projection{Redis: cache, Namespace: namespace}
	producer, err := kgo.NewClient(kgo.SeedBrokers("127.0.0.1:19092"), kgo.RequiredAcks(kgo.AllISRAcks()))
	if err != nil {
		t.Fatal(err)
	}
	defer producer.Close()
	page := recommendedPage(t, owner, "")
	var exposure, post uuid.UUID
	for _, item := range page.Items {
		if item.Type == "POST" && item.Recommendation.ExposureID != uuid.Nil {
			exposure = item.Recommendation.ExposureID
			post = item.Post.ID
			break
		}
	}
	if exposure == uuid.Nil {
		t.Fatal("no exposure")
	}
	var subject uuid.UUID
	integrationAdmin.QueryRow(ctx, "SELECT stream_subject FROM social.recommendation_preference WHERE profile_id=$1", viewer).Scan(&subject)
	body := map[string]any{"eventId": uuid.New(), "exposureId": exposure, "kind": "MORE"}
	mustStatus(t, owner.request("POST", "me/recommendation-events", body, 0, ""), 200)
	drainRecommendationStream(t, db, stream.KafkaProducer{Client: producer})
	features := namespace + ":{viewer:" + subject.String() + "}:features"
	field := "MORE:" + post.String()
	// Use the actual group consumer and offset acknowledgement path.
	group := "proof-" + uuid.NewString()
	newConsumer := func() (*kgo.Client, error) {
		return kgo.NewClient(kgo.SeedBrokers("127.0.0.1:19092"), kgo.ConsumeTopics(stream.Topic), kgo.ConsumerGroup(group), kgo.DisableAutoCommit(), kgo.BlockRebalanceOnPoll(), kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()))
	}
	failedConsumer, err := newConsumer()
	if err != nil {
		t.Fatal(err)
	}
	failedCache := redis.NewClient(&redis.Options{Addr: "127.0.0.1:16379"})
	failedCache.Close()
	err = stream.Consume(ctx, failedConsumer, db, stream.Projection{Redis: failedCache, Namespace: namespace})
	if err == nil || ctx.Err() != nil {
		t.Fatal("Redis outage did not stop projection promptly", err)
	}
	for _, partitions := range failedConsumer.CommittedOffsets() {
		for _, offset := range partitions {
			if offset.Offset > 0 {
				t.Fatal("failed projection acknowledged Kafka offsets")
			}
		}
	}
	failedConsumer.Close()
	// Restart the same group: failed records must still be available for replay.
	consumer, err := newConsumer()
	if err != nil {
		t.Fatal(err)
	}
	consumeCtx, stop := context.WithCancel(ctx)
	var wg sync.WaitGroup
	var consumeErr error
	wg.Add(1)
	go func() { defer wg.Done(); consumeErr = stream.Consume(consumeCtx, consumer, db, projection) }()
	defer func() {
		stop()
		wg.Wait()
		consumer.Close()
		if consumeErr != nil && !errors.Is(consumeErr, context.Canceled) && !errors.Is(consumeErr, context.DeadlineExceeded) {
			t.Error(consumeErr)
		}
	}()
	wait := func(check func() bool) {
		t.Helper()
		for ctx.Err() == nil {
			if check() {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatal("projection did not converge")
	}
	wait(func() bool { return cache.HGet(ctx, features, field).Val() == "1" })
	var data []byte
	if err := integrationAdmin.QueryRow(ctx, "SELECT payload||jsonb_build_object('entityVersion',sequence) FROM rec_stream.outbox WHERE id=$1", body["eventId"]).Scan(&data); err != nil {
		t.Fatal(err)
	}
	e, err := stream.Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	authority, err := stream.CurrentAuthority(ctx, db, subject)
	if err != nil {
		t.Fatal(err)
	}
	if err := projection.Apply(ctx, e, authority); err != nil {
		t.Fatal(err)
	}
	if cache.HLen(ctx, features).Val() != 1 {
		t.Fatal("duplicate changed feature projection")
	}
	// Withdrawal propagates to the real consumer; an old event then arrives late.
	consentRecommendation(t, owner, false)
	drainRecommendationStream(t, db, stream.KafkaProducer{Client: producer})
	wait(func() bool { return cache.Exists(ctx, features).Val() == 0 })
	if err := producer.ProduceSync(ctx, &kgo.Record{Topic: stream.Topic, Key: []byte(e.Key()), Value: data}).FirstErr(); err != nil {
		t.Fatal(err)
	}
	authority, err = stream.CurrentAuthority(ctx, db, subject)
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.ProjectRecord(ctx, db, projection, &kgo.Record{Key: []byte(e.Key()), Value: data}); err != nil {
		t.Fatal(err)
	}
	if cache.Exists(ctx, features).Val() != 0 {
		t.Fatal("late event resurrected withdrawn behavior")
	}
	// Simulate loss of Redis projection state: replay still consults authority.
	state := namespace + ":{viewer:" + subject.String() + "}:state"
	cache.Del(ctx, state, features, namespace+":{viewer:"+subject.String()+"}:seen")
	if err := projection.Apply(ctx, e, authority); err != nil {
		t.Fatal(err)
	}
	if cache.Exists(ctx, features).Val() != 0 {
		t.Fatal("cache rebuild resurrected history")
	}
	// Per-field retention: another action cannot extend an older feature.
	copy := e
	copy.Subject = uuid.New()
	copy.EventID = uuid.New()
	copy.OccurredAt = time.Now().Add(-30*24*time.Hour + 3*time.Second)
	if err := projection.Apply(ctx, copy, stream.Authority{Generation: copy.Generation, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	oldFeatures := namespace + ":{viewer:" + copy.Subject.String() + "}:features"
	copy.EventID = uuid.New()
	copy.PostID = uuid.New()
	copy.OccurredAt = time.Now()
	if err := projection.Apply(ctx, copy, stream.Authority{Generation: copy.Generation, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	wait(func() bool { return !cache.HExists(ctx, oldFeatures, field).Val() })
	if cache.HLen(ctx, oldFeatures).Val() != 1 {
		t.Fatal("retention removed new field or kept old field")
	}
	// Decimal entity versions remain exact beyond float64's integer range.
	content := stream.Envelope{EventType: "CONTENT", PostID: uuid.New(), EntityVersion: 9007199254740993, Revision: 2, Eligible: true}
	if err := projection.Apply(ctx, content, stream.Authority{}); err != nil {
		t.Fatal(err)
	}
	content.EntityVersion--
	content.Revision = 1
	if err := projection.Apply(ctx, content, stream.Authority{}); err != nil {
		t.Fatal(err)
	}
	if cache.HGet(ctx, namespace+":{post:"+content.PostID.String()+"}:state", "revision").Val() != "2" {
		t.Fatal("older content version overwrote projection")
	}
	// Unknown/private payloads stop processing before any write.
	var raw map[string]any
	json.Unmarshal(data, &raw)
	raw["ocr"] = "private"
	private, _ := json.Marshal(raw)
	if err := stream.ProjectRecord(ctx, db, projection, &kgo.Record{Key: []byte(e.Key()), Value: private}); err == nil {
		t.Fatal("private stream field accepted")
	}
}

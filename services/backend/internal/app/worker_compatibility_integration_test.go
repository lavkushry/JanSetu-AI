package app

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/lavkushry/JanSetu-AI/services/backend/internal/store/dbgen"
)

func queuedProjectionEvent(t *testing.T, kind string, aggregate uuid.UUID, eventType string, payload any) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if err := dbgen.New(integrationAdmin).AddEvent(context.Background(), dbgen.AddEventParams{
		ID: id, AggregateType: kind, AggregateID: aggregate, AggregateVersion: 1,
		EventType: eventType, Payload: jsonBytes(payload),
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		integrationAdmin.Exec(context.Background(), "DELETE FROM infra.processed_event WHERE event_id=$1;", id)
		integrationAdmin.Exec(context.Background(), "DELETE FROM infra.outbox WHERE id=$1;", id)
	})
	return id
}

func assertProjectionFailure(t *testing.T, a *App, id uuid.UUID, code string, attempt int) {
	t.Helper()
	worked, err := a.ProjectOnce(context.Background(), "event-compatibility-test")
	if !worked || err == nil || ProjectionErrorCode(err) != code {
		t.Fatalf("expected retained %s failure: worked=%v err=%v", code, worked, err)
	}
	if strings.Contains(err.Error(), "private-event-marker") {
		t.Fatal("projection error exposed untrusted metadata")
	}
	var attempts int
	var storedCode string
	var delivered, processed, leased, deadLettered, delayed bool
	err = integrationAdmin.QueryRow(context.Background(), `SELECT attempts,last_error_code,
	 delivered_at IS NOT NULL,EXISTS(SELECT FROM infra.processed_event WHERE event_id=o.id),
	 lease_until IS NOT NULL,dead_lettered_at IS NOT NULL,available_at>now()
	 FROM infra.outbox o WHERE id=$1`, id).Scan(&attempts, &storedCode, &delivered, &processed, &leased, &deadLettered, &delayed)
	if err != nil || attempts != attempt || storedCode != code || delivered || processed || leased || deadLettered != (attempt == 8) || !delayed {
		t.Fatalf("failure was not retained atomically: attempts=%d code=%s delivered=%v processed=%v leased=%v dead=%v delayed=%v err=%v", attempts, storedCode, delivered, processed, leased, deadLettered, delayed, err)
	}
}

func TestUnsupportedProjectionEventsRetainHistoryAndDoNotBlockValidWork(t *testing.T) {
	a := testApp(t)
	author, mod := login(t, a, 0), login(t, a, 2)
	p := published(t, a, author, mod, "Worker compatibility fixture "+uuid.NewString())
	drainActivity(t, a)
	for _, fixture := range []struct{ name, eventType, code string }{
		{"future-type", "private-event-marker-future", "UNSUPPORTED_EVENT_TYPE"},
		{"future-schema", "PostVoteChanged", "UNSUPPORTED_PAYLOAD_VERSION"},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			id := queuedProjectionEvent(t, "POST", p.ID, fixture.eventType, map[string]any{"secret": "private-event-marker"})
			if fixture.name == "future-schema" {
				if _, err := integrationAdmin.Exec(context.Background(), "UPDATE infra.outbox SET payload_version=2 WHERE id=$1", id); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := integrationAdmin.Exec(context.Background(), "UPDATE social.post_stats SET up_count=771 WHERE post_id=$1", p.ID); err != nil {
				t.Fatal(err)
			}
			assertProjectionFailure(t, a, id, fixture.code, 1)
			var count int
			if err := integrationAdmin.QueryRow(context.Background(), "SELECT up_count FROM social.post_stats WHERE post_id=$1", p.ID).Scan(&count); err != nil || count != 771 {
				t.Fatal("unsupported event committed a projection", count, err)
			}
			valid := queuedProjectionEvent(t, "POST", p.ID, "PostVoteChanged", map[string]any{})
			if worked, err := a.ProjectOnce(context.Background(), "valid-event-test"); !worked || err != nil {
				t.Fatal("a delayed unsupported event blocked valid work", worked, err)
			}
			var complete bool
			if err := integrationAdmin.QueryRow(context.Background(), "SELECT delivered_at IS NOT NULL FROM infra.outbox WHERE id=$1", valid).Scan(&complete); err != nil || !complete {
				t.Fatal("valid event was not acknowledged", err)
			}
			if err := integrationAdmin.QueryRow(context.Background(), "SELECT up_count FROM social.post_stats WHERE post_id=$1", p.ID).Scan(&count); err != nil || count != 0 {
				t.Fatal("supported post projection did not run", count, err)
			}
			for attempt := 2; attempt <= 8; attempt++ {
				if _, err := integrationAdmin.Exec(context.Background(), "UPDATE infra.outbox SET available_at=now() WHERE id=$1", id); err != nil {
					t.Fatal(err)
				}
				assertProjectionFailure(t, a, id, fixture.code, attempt)
			}
			if worked, err := a.ProjectOnce(context.Background(), "dead-letter-test"); worked || err != nil {
				t.Fatal("dead-lettered event was claimed again", worked, err)
			}
		})
	}
}

func TestInvalidProjectionEnvelopeAndPayloadRemainUnacknowledged(t *testing.T) {
	a := testApp(t)
	drainActivity(t, a)
	for _, fixture := range []struct {
		name, kind, eventType string
		payload               any
	}{
		{"wrong-aggregate", "CASE", "ModerationDecisionRecorded", map[string]any{}},
		{"non-object", "CASE", "SafeReceiptPublished", []string{"private-event-marker"}},
		{"null-object", "CASE", "CaseCreated", nil},
		{"missing-receipt", "CASE", "SafeReceiptPublished", map[string]any{}},
		{"invalid-receipt", "CASE", "SafeReceiptPublished", map[string]any{"receiptId": "private-event-marker"}},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			id := queuedProjectionEvent(t, fixture.kind, uuid.New(), fixture.eventType, fixture.payload)
			assertProjectionFailure(t, a, id, "INVALID_EVENT", 1)
		})
	}
}

func TestProjectionRecoveryDeliversOnceAndClearsFailure(t *testing.T) {
	a := testApp(t)
	owner, writer, mod := login(t, a, 0), login(t, a, 1), login(t, a, 2)
	p := published(t, a, owner, mod, "Worker recovery fixture "+uuid.NewString())
	drainActivity(t, a)
	cid := approvedReply(t, writer, mod, p.ID, nil)
	var id uuid.UUID
	var original []byte
	if err := integrationAdmin.QueryRow(context.Background(), "SELECT id,payload FROM infra.outbox WHERE event_type='CommentPublished' AND payload->>'commentId'=$1", cid.String()).Scan(&id, &original); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		integrationAdmin.Exec(context.Background(), "UPDATE infra.outbox SET payload=$2 WHERE id=$1", id, original)
	})
	if _, err := integrationAdmin.Exec(context.Background(), "UPDATE infra.outbox SET payload=$2,available_at=now()+interval '1 hour' WHERE id=$1", id, jsonBytes(map[string]any{"commentId": "private-event-marker", "revision": 1})); err != nil {
		t.Fatal(err)
	}
	drainActivity(t, a)
	if _, err := integrationAdmin.Exec(context.Background(), "UPDATE infra.outbox SET available_at=now() WHERE id=$1", id); err != nil {
		t.Fatal(err)
	}
	if _, err := integrationAdmin.Exec(context.Background(), "UPDATE social.post_stats SET up_count=723 WHERE post_id=$1", p.ID); err != nil {
		t.Fatal(err)
	}
	assertProjectionFailure(t, a, id, "INVALID_EVENT", 1)
	var count int
	if err := integrationAdmin.QueryRow(context.Background(), "SELECT up_count FROM social.post_stats WHERE post_id=$1", p.ID).Scan(&count); err != nil || count != 723 {
		t.Fatal("invalid reply committed the preceding count rebuild", count, err)
	}
	if len(activityFor(t, owner, p.ID).Items) != 0 {
		t.Fatal("malformed reply delivered a notification")
	}
	if _, err := integrationAdmin.Exec(context.Background(), "UPDATE infra.outbox SET payload=$2,available_at=now() WHERE id=$1", id, original); err != nil {
		t.Fatal(err)
	}
	drainActivity(t, a)
	items := activityFor(t, owner, p.ID).Items
	if len(items) != 1 {
		t.Fatal("corrected retry did not deliver exactly once", items)
	}
	var successful bool
	if err := integrationAdmin.QueryRow(context.Background(), "SELECT delivered_at IS NOT NULL AND last_error_code IS NULL AND attempts=2 FROM infra.outbox WHERE id=$1", id).Scan(&successful); err != nil || !successful {
		t.Fatal("successful retry retained a stale failure", err)
	}
	path := "me/activity/" + items[0].ID.String() + "/read"
	mustStatus(t, owner.request("PUT", path, map[string]any{"read": true}, 0, ""), 200)
	if _, err := integrationAdmin.Exec(context.Background(), "UPDATE infra.outbox SET delivered_at=NULL WHERE id=$1", id); err != nil {
		t.Fatal(err)
	}
	drainActivity(t, a)
	if items = activityFor(t, owner, p.ID).Items; len(items) != 1 || items[0].ReadAt == nil {
		t.Fatal("event redelivery duplicated a recovered notice or lost read state", items)
	}
}

func TestPreviouslyProcessedUnsupportedEventsDoNotReplay(t *testing.T) {
	a := testApp(t)
	drainActivity(t, a)
	id := queuedProjectionEvent(t, "CASE", uuid.New(), "private-event-marker-legacy", map[string]any{"secret": "private-event-marker"})
	// Model a historical event already acknowledged by a pre-guard worker.
	if _, err := integrationAdmin.Exec(context.Background(), "INSERT INTO infra.processed_event(consumer_name,event_id) VALUES('core-projector',$1)", id); err != nil {
		t.Fatal(err)
	}
	if _, err := integrationAdmin.Exec(context.Background(), "UPDATE infra.outbox SET payload_version=99 WHERE id=$1", id); err != nil {
		t.Fatal(err)
	}
	if worked, err := a.ProjectOnce(context.Background(), "historical-redelivery-test"); !worked || err != nil {
		t.Fatal("historical deduplication was replaced by compatibility retry", worked, err)
	}
	var acknowledged bool
	var processed, notices int
	if err := integrationAdmin.QueryRow(context.Background(), `SELECT delivered_at IS NOT NULL AND last_error_code IS NULL,
	 (SELECT count(*) FROM infra.processed_event WHERE event_id=o.id),
	 (SELECT count(*) FROM social.notification WHERE event_id=o.id)
	 FROM infra.outbox o WHERE id=$1`, id).Scan(&acknowledged, &processed, &notices); err != nil || !acknowledged || processed != 1 || notices != 0 {
		t.Fatal("historical event replayed or lost deduplication", acknowledged, processed, notices, err)
	}
}

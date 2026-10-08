package app

import (
	"context"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lavkushry/JanSetu-AI/services/backend/internal/recommendation/stream"
)

func resetContentBackfill(t *testing.T) {
	t.Helper()
	if _, err := integrationAdmin.Exec(context.Background(), `UPDATE rec_stream.content_backfill SET started=false,upper_id=NULL,last_id=NULL,completed=false,scanned=0,enqueued=0`); err != nil {
		t.Fatal(err)
	}
}
func readBackfill(t *testing.T, db *pgxpool.Pool, size int) stream.BackfillProgress {
	t.Helper()
	p, err := stream.BackfillContent(context.Background(), db, size)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func TestRecommendationStreamContentBackfill(t *testing.T) {
	a := testApp(t)
	ctx := context.Background()
	// Register community cleanup before fixture cleanup (LIFO).
	community := uuid.New()
	t.Cleanup(func() {
		if _, err := integrationAdmin.Exec(ctx, "DELETE FROM social.community WHERE id=$1", community); err != nil {
			t.Error(err)
		}
	})
	ids := recommendationFixture(t, a)
	db := streamPool(t)
	resetContentBackfill(t)
	t.Cleanup(func() { resetContentBackfill(t) })
	execSQL := func(query string, args ...any) {
		t.Helper()
		if _, err := integrationAdmin.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	execSQL(`INSERT INTO social.community(id,slug,title,scope_kind,visibility,rules_body,state) VALUES($1,$2,'Backfill private fixture','TOPIC','PRIVATE','','ACTIVE')`, community, "backfill_"+community.String()[:8])
	execSQL("UPDATE social.post SET state='HIDDEN' WHERE id=$1", ids[0])
	execSQL("UPDATE social.post SET state='DRAFT' WHERE id=$1", ids[1])
	execSQL("UPDATE social.post_revision SET review_state='REJECTED' WHERE post_id=$1", ids[2])
	execSQL("UPDATE social.profile SET state='SUSPENDED' WHERE id=(SELECT author_id FROM social.post WHERE id=$1)", ids[4]) // also excludes ids[5]
	execSQL("UPDATE social.post SET community_id=$1 WHERE id=$2", community, ids[6])
	execSQL("UPDATE social.post SET kind='QUOTE',source_post_id=$1 WHERE id=$2", ids[0], ids[7])
	// These fixture posts predate the stream from the backfill's perspective.
	keys := make([]string, len(ids))
	for i, id := range ids {
		keys[i] = "post:" + id.String()
	}
	execSQL("DELETE FROM rec_stream.outbox WHERE aggregate_key=ANY($1)", keys)
	for _, table := range []string{"rec_stream.content_backfill", "rec_stream.outbox", "social.post", "social.post_revision", "ops.report"} {
		if _, err := db.Exec(ctx, "SELECT * FROM "+table+" LIMIT 1"); err == nil {
			t.Fatal("backfill worker can read", table)
		}
	}
	if _, err := db.Exec(ctx, "UPDATE rec_stream.content_backfill SET completed=false"); err == nil {
		t.Fatal("worker can rewrite checkpoint")
	}
	if _, err := a.DB.Exec(ctx, "SELECT rec_stream.backfill_content(1)"); err == nil {
		t.Fatal("API can backfill")
	}
	for _, size := range []any{nil, 0, 101, -1} {
		if _, err := db.Exec(ctx, "SELECT rec_stream.backfill_content($1)", size); err == nil {
			t.Fatal("invalid SQL batch accepted", size)
		}
	}
	for _, size := range []int{0, 101, -1} {
		if _, err := stream.BackfillContent(ctx, db, size); err == nil {
			t.Fatal("invalid Go batch accepted")
		}
	}
	var before int64
	if err := integrationAdmin.QueryRow(ctx, "SELECT coalesce(max(sequence),0) FROM rec_stream.outbox").Scan(&before); err != nil {
		t.Fatal(err)
	}
	// A lost/aborted transaction must not advance the cursor or leave any envelopes.
	tx, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, "SELECT * FROM rec_stream.backfill_content(2)"); err != nil {
		tx.Rollback(ctx)
		t.Fatal(err)
	}
	if err = tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	var started bool
	var count int
	if err := integrationAdmin.QueryRow(ctx, "SELECT started FROM rec_stream.content_backfill").Scan(&started); err != nil || started {
		t.Fatal("rolled-back checkpoint advanced", err)
	}
	if err := integrationAdmin.QueryRow(ctx, "SELECT count(*) FROM rec_stream.outbox WHERE sequence>$1", before).Scan(&count); err != nil || count != 0 {
		t.Fatal("rolled-back envelopes survived", count, err)
	}
	// Two callers share one durable cursor. Each successful page counts once.
	var wg sync.WaitGroup
	results := make(chan stream.BackfillProgress, 2)
	errs := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); p, e := stream.BackfillContent(ctx, db, 2); results <- p; errs <- e }()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	totals := map[int64]bool{}
	for result := range results {
		if result.Scanned != 2 {
			t.Fatal("page bound", result)
		}
		totals[result.TotalScanned] = true
	}
	if !totals[2] || !totals[4] {
		t.Fatal("concurrent callers did not advance once", totals)
	}
	var final stream.BackfillProgress
	for range 100 {
		final = readBackfill(t, db, 3)
		if final.Completed {
			break
		}
	}
	if !final.Completed {
		t.Fatal("backfill did not terminate")
	}
	again := readBackfill(t, db, 3)
	if !again.Completed || again.Scanned != 0 || again.Enqueued != 0 || again.TotalScanned != final.TotalScanned || again.TotalEnqueued != final.TotalEnqueued {
		t.Fatal("completed pass was replayed", again, final)
	}
	rows, err := integrationAdmin.Query(ctx, "SELECT payload||jsonb_build_object('entityVersion',sequence) FROM rec_stream.outbox WHERE sequence>$1", before)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[uuid.UUID]int{}
	for rows.Next() {
		var data []byte
		if err := rows.Scan(&data); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		event, err := stream.Decode(data)
		if err != nil {
			rows.Close()
			t.Fatal(err)
		}
		if event.EventType != "CONTENT" || !event.Eligible || event.Revision < 1 {
			rows.Close()
			t.Fatal("unexpected backfill envelope", event)
		}
		seen[event.PostID]++
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	for i, id := range ids {
		want := 1
		if i == 0 || i == 1 || i == 2 || i == 4 || i == 5 || i == 6 || i == 7 {
			want = 0
		}
		if seen[id] != want {
			t.Fatalf("post %d: got %d events, want %d", i, seen[id], want)
		}
	}
	if len(seen) != int(final.TotalEnqueued) {
		t.Fatal("checkpoint/outbox count mismatch")
	}
	// Existing durable publisher still owns delivery and retry.
	producer := &recordingStreamProducer{}
	drainRecommendationStream(t, db, producer)
	delivered := map[uuid.UUID]bool{}
	for _, event := range producer.items {
		if event.EventType == "CONTENT" {
			delivered[event.PostID] = true
		}
	}
	for id := range seen {
		if !delivered[id] {
			t.Fatal("backfilled reference was not delivered")
		}
	}
}

func TestRecommendationStreamContentBackfillContentionAndCommand(t *testing.T) {
	a := testApp(t)
	ids := recommendationFixture(t, a)
	ctx := context.Background()
	db := streamPool(t)
	resetContentBackfill(t)
	t.Cleanup(func() { resetContentBackfill(t) })
	var first uuid.UUID
	if err := integrationAdmin.QueryRow(ctx, "SELECT id FROM social.post ORDER BY id LIMIT 1").Scan(&first); err != nil {
		t.Fatal(err)
	}
	blocker, err := integrationAdmin.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback(ctx)
	if _, err = blocker.Exec(ctx, "SELECT id FROM social.post WHERE id=$1 FOR UPDATE", first); err != nil {
		t.Fatal(err)
	}
	if _, err = stream.BackfillContent(ctx, db, 2); err == nil {
		t.Fatal("backfill skipped a locked historical post")
	}
	var progressed bool
	if err := integrationAdmin.QueryRow(ctx, "SELECT started FROM rec_stream.content_backfill").Scan(&progressed); err != nil || progressed {
		t.Fatal("lock timeout advanced checkpoint", err)
	}
	if err = blocker.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if p := readBackfill(t, db, 1); p.Scanned != 1 {
		t.Fatal("restart failed", p)
	}
	var cursor uuid.UUID
	if err := integrationAdmin.QueryRow(ctx, "SELECT last_id FROM rec_stream.content_backfill").Scan(&cursor); err != nil || cursor != first {
		t.Fatal("busy row lost", err)
	}

	// New publication above the frozen upper bound is delivered by the live
	// trigger exactly once, never by extending the historical scan indefinitely.
	liveID := uuid.MustParse("ffffffff-ffff-ffff-ffff-ffffffffffff")
	if err := pgx.BeginFunc(ctx, integrationAdmin, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO social.post(id,author_id,kind,state,published_revision,published_at) SELECT $1,author_id,'SHORT','PUBLISHED',1,statement_timestamp() FROM social.post WHERE id=$2`, liveID, ids[0]); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO social.post_revision(post_id,revision,body,language_tag,review_state) VALUES($1,1,'Live after backfill start','en-IN','APPROVED')`, liveID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := pgx.BeginFunc(ctx, integrationAdmin, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, "DELETE FROM social.post_revision WHERE post_id=$1", liveID); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, "DELETE FROM social.post WHERE id=$1", liveID)
			return err
		}); err != nil {
			t.Error(err)
		}
	})
	binary := filepath.Join(t.TempDir(), "recommendation-stream")
	if output, err := exec.Command("go", "build", "-o", binary, "../../cmd/recommendation-stream").CombinedOutput(); err != nil {
		t.Fatal(err, string(output))
	}
	commandCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(commandCtx, binary, "-mode", "backfill", "-batch-size", "7")
	// No Kafka/Redis endpoints are available: backfill only needs its restricted DB.
	cmd.Env = []string{"JANSETU_ENV=test", "JANSETU_RECOMMENDATION_STREAM_DATABASE_URL=" + roleURL(integrationAdmin.Config().ConnString(), "js_recommendation_stream"), "JANSETU_RECOMMENDATION_KAFKA_BROKERS=127.0.0.1:1", "JANSETU_RECOMMENDATION_REDIS_URL=redis://127.0.0.1:1/0"}
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatal("backfill command", err, string(output))
	}
	if p := readBackfill(t, db, 1); !p.Completed || p.Enqueued != 0 {
		t.Fatal("command failed to finish", p)
	}
	var liveEvents int
	if err := integrationAdmin.QueryRow(ctx, "SELECT count(*) FROM rec_stream.outbox WHERE aggregate_key=$1", "post:"+liveID.String()).Scan(&liveEvents); err != nil || liveEvents != 1 {
		t.Fatal("live post was lost or backfilled beyond upper bound", liveEvents, err)
	}
	for _, args := range [][]string{{"-mode", "backfill", "-batch-size", "0"}, {"-mode", "invalid"}} {
		rejected := exec.Command(binary, args...)
		rejected.Env = []string{"JANSETU_ENV=test"}
		if err := rejected.Run(); err == nil {
			t.Fatal("invalid command accepted")
		}
	}
	production := exec.Command(binary, "-mode", "backfill")
	production.Env = []string{"JANSETU_ENV=production"}
	if err := production.Run(); err == nil {
		t.Fatal("production gate bypassed")
	}
}

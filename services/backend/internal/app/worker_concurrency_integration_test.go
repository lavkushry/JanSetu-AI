package app

import (
	"context"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lavkushry/JanSetu-AI/services/backend/internal/store/dbgen"
)

func TestReplyProjectionAndCommandDoNotDeadlock(t *testing.T) {
	a := testApp(t)
	owner, writer, mod := login(t, a, 0), login(t, a, 1), login(t, a, 2)
	p := published(t, a, owner, mod, "Concurrent reply projection fixture")
	drainActivity(t, a)
	cid := approvedReply(t, writer, mod, p.ID, nil)
	req := httptest.NewRequest("POST", "/v1/posts/"+p.ID.String()+"/comments", nil)
	req.AddCookie(owner.cookie)
	req = a.requestScope(req, uuid.NewString())
	actor, err := a.actor(req)
	if err != nil || actor == nil {
		t.Fatal("owner session unavailable", err)
	}

	// Pause the real notification insertion after the worker locked the post,
	// immediately before its foreign key acquires a lock on the recipient profile.
	const gateID int64 = 17052026
	gate, err := integrationAdmin.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer gate.Release()
	if _, err = gate.Exec(context.Background(), "SELECT pg_advisory_lock($1)", gateID); err != nil {
		t.Fatal(err)
	}
	defer gate.Exec(context.Background(), "SELECT pg_advisory_unlock($1)", gateID)
	ddl := fmt.Sprintf(`CREATE FUNCTION infra.test_notification_gate() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN PERFORM pg_advisory_xact_lock(%d); RETURN NEW; END $$;
CREATE TRIGGER test_notification_gate BEFORE INSERT ON social.notification
FOR EACH ROW WHEN (NEW.comment_id='%s'::uuid) EXECUTE FUNCTION infra.test_notification_gate();`, gateID, cid)
	if _, err = integrationAdmin.Exec(context.Background(), ddl); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		integrationAdmin.Exec(context.Background(), "DROP TRIGGER test_notification_gate ON social.notification; DROP FUNCTION infra.test_notification_gate()")
	})
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	workerDone := make(chan error, 1)
	go func() {
		for {
			worked, err := a.ProjectOnce(ctx, "concurrent-projection-test")
			if err != nil || !worked {
				workerDone <- err
				return
			}
		}
	}()
	deadline := time.Now().Add(3 * time.Second)
	for {
		var waiting bool
		if err := integrationAdmin.QueryRow(ctx, "SELECT EXISTS(SELECT FROM pg_locks WHERE locktype='advisory' AND objid::bigint=$1 AND NOT granted AND database=(SELECT oid FROM pg_database WHERE datname=current_database()))", gateID).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("worker did not reach notification insertion")
		}
		time.Sleep(10 * time.Millisecond)
	}
	commandCtx, commandCancel := context.WithTimeout(req.Context(), 8*time.Second)
	defer commandCancel()
	profileLocked := make(chan struct{})
	commandDone := make(chan error, 1)
	go func() {
		commandDone <- a.transaction(commandCtx, actor, func(q *dbgen.Queries) error {
			close(profileLocked)
			_, err := q.LockPost(commandCtx, p.ID)
			return err
		})
	}()
	select {
	case <-profileLocked:
		// Before the fix, this command holds the profile and waits for the post.
	case <-time.After(250 * time.Millisecond):
		// With ordered pilot serialization it waits before taking any row locks.
	}
	if _, err := gate.Exec(ctx, "SELECT pg_advisory_unlock($1)", gateID); err != nil {
		t.Fatal(err)
	}
	workerErr, commandErr := <-workerDone, <-commandDone
	if workerErr != nil || commandErr != nil {
		t.Fatalf("projection/command lock cycle: worker=%v command=%v", workerErr, commandErr)
	}
	var notifications int
	if err := integrationAdmin.QueryRow(ctx, "SELECT count(*) FROM social.notification WHERE comment_id=$1", cid).Scan(&notifications); err != nil || notifications != 1 {
		t.Fatal("reply notification not delivered exactly once", notifications, err)
	}
}

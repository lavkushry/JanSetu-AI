package app

import (
	"context"
	"errors"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/lavkushry/JanSetu-AI/services/backend/internal/vault"
)

type amendmentReceipt struct {
	ID, TaskID               uuid.UUID
	AddedPrerequisiteTaskIDs []uuid.UUID
	TaskVersion, CaseVersion int64
}

func amendmentInput(ids ...uuid.UUID) prerequisiteAmendmentInput {
	return prerequisiteAmendmentInput{ClientAmendmentID: uuid.New(), AddedPrerequisiteTaskIDs: ids, Reviewed: true, Reason: "PRIVATE coordinator reviewed necessary prerequisites before acceptance"}
}
func amendmentPath(oid uuid.UUID) string {
	return "authority/obligations/" + oid.String() + "/prerequisite-amendments"
}

func TestPrerequisiteAmendmentsConserveWorkAndOriginalProposalRetries(t *testing.T) {
	a := testApp(t)
	owner, coordinator, city, water, verifier := login(t, a, 0), login(t, a, 2), login(t, a, 3), login(t, a, 1), login(t, a, 4)
	taskTestGrant(t, DemoPrincipals[1], "AGENCY_AGENT")
	taskTestGrant(t, DemoPrincipals[4], "VERIFIER")
	rid, cid, received := publicationCaseFixture(t, owner, coordinator, "SANITIZED_RECEIPT")
	first := taskDetail(t, coordinator, cid).Obligations[0].ID
	publishPath := "authority/cases/" + cid.String() + "/publications"
	w := coordinator.request("POST", publishPath, publicationPreview(0, "Fictional original restoration progress"), 1, "")
	mustStatus(t, w, 200)
	receipt := parsed[struct{ ReceiptID uuid.UUID }](t, w).ReceiptID
	original := taskProposal{ClientTaskID: uuid.New(), AgencyID: cityTaskAgency, Scope: "PRIVATE resurface after all required groundwork"}
	w = coordinator.request("POST", "authority/cases/"+cid.String()+"/obligations", original, 1, "")
	mustStatus(t, w, 201)
	dependent := parsed[proposalResult](t, w).ID
	second := proposeSequencedTask(t, coordinator, cid, waterTaskAgency, "PRIVATE repair water service before resurfacing")
	if _, err := integrationAdmin.Exec(context.Background(), "UPDATE ops.obligation SET due_at=now()+interval '1 day' WHERE id=$1", dependent); err != nil {
		t.Fatal(err)
	}
	before := splitTask(t, taskDetail(t, coordinator, cid), dependent)
	input := amendmentInput(second, first)
	responses := make(chan *httptest.ResponseRecorder, 5)
	for i := 0; i < 5; i++ {
		go func() { responses <- coordinator.request("POST", amendmentPath(dependent), input, 1, "") }()
	}
	var saved amendmentReceipt
	for i := 0; i < 5; i++ {
		w = <-responses
		mustStatus(t, w, 201)
		s := parsed[amendmentReceipt](t, w)
		if saved.ID != uuid.Nil && saved.ID != s.ID {
			t.Fatal("retry duplicated amendment")
		}
		saved = s
	}
	d := taskDetail(t, coordinator, cid)
	task := splitTask(t, d, dependent)
	if d.Version != 4 || len(d.PrerequisiteAmendments) != 1 || task.Version != 2 || len(task.PrerequisiteTaskIDs) != 2 || d.FirstReportedAt != received || task.Scope != before.Scope || task.DueAt == nil || *task.DueAt != *before.DueAt {
		t.Fatal("amendment changed scope, clocks or accounting", d)
	}
	input.AddedPrerequisiteTaskIDs = []uuid.UUID{first, second}
	mustStatus(t, coordinator.request("POST", amendmentPath(dependent), input, 1, ""), 201)
	mustStatus(t, coordinator.request("POST", "authority/cases/"+cid.String()+"/obligations", original, 1, ""), 201)
	changedProposal := original
	changedProposal.PrerequisiteTaskIDs = []uuid.UUID{first}
	mustStatus(t, coordinator.request("POST", "authority/cases/"+cid.String()+"/obligations", changedProposal, 1, ""), 409)
	changed := input
	changed.Reason = "PRIVATE different immutable amendment reason"
	mustStatus(t, coordinator.request("POST", amendmentPath(dependent), changed, 1, ""), 409)
	duplicate := amendmentInput(first)
	w = coordinator.request("POST", amendmentPath(dependent), duplicate, 2, "")
	mustStatus(t, w, 409)
	if parsed[Problem](t, w).Code != "TASK_PREREQUISITE_EXISTS" {
		t.Fatal(w.Body.String())
	}
	var eventCount, outboxCount int
	if err := integrationAdmin.QueryRow(context.Background(), "SELECT (SELECT count(*) FROM ops.case_event WHERE case_id=$1 AND event_type='TASK_PREREQUISITES_ADDED'),(SELECT count(*) FROM infra.outbox WHERE aggregate_id=$1 AND aggregate_version=4 AND event_type='ObligationChanged')", cid).Scan(&eventCount, &outboxCount); err != nil || eventCount != 1 || outboxCount != 1 {
		t.Fatal("retry changed audit or outbox", eventCount, outboxCount, err)
	}
	performTask(t, city, dependent, 2, "accept")
	blockedSplitWork(t, city, cid, dependent)
	performTask(t, city, first, 1, "accept", "start", "completion-claims")
	inspectSequencedTask(t, verifier, cid, first, "VERIFIED")
	blockedSplitWork(t, city, cid, dependent)
	performTask(t, water, second, 1, "accept", "start", "completion-claims")
	inspectSequencedTask(t, verifier, cid, second, "VERIFIED")
	performTask(t, city, dependent, 3, "start", "completion-claims")
	inspectSequencedTask(t, verifier, cid, dependent, "VERIFIED")
	d = taskDetail(t, coordinator, cid)
	if d.State != "RESOLVED" || d.FirstReportedAt != received {
		t.Fatal("amended required work did not resolve independently", d)
	}
	mustStatus(t, coordinator.request("POST", amendmentPath(dependent), input, 1, ""), 201)
	mustStatus(t, coordinator.request("POST", "authority/cases/"+cid.String()+"/obligations", original, 1, ""), 201)
	closedAttempt := coordinator.request("POST", amendmentPath(dependent), amendmentInput(first), splitTask(t, d, dependent).Version, "")
	mustStatus(t, closedAttempt, 409)
	if parsed[Problem](t, closedAttempt).Code != "CASE_CLOSED" {
		t.Fatal("resolved case accepted a fresh amendment")
	}
	old := owner.request("GET", "case-receipts/"+receipt.String(), nil, 0, "")
	mustStatus(t, old, 200)
	if parsed[struct{ State string }](t, old).State != "OPEN" {
		t.Fatal("amendment auto-published private progress")
	}
	mustStatus(t, coordinator.request("POST", publishPath, publicationPreview(1, "Fictional reviewed restored service"), d.Version, ""), 200)
	pub := owner.request("GET", "case-receipts/"+receipt.String(), nil, 0, "")
	mustStatus(t, pub, 200)
	own := owner.request("GET", "my-reports/"+rid.String(), nil, 0, "")
	mustStatus(t, own, 200)
	for _, secret := range []string{input.Reason, saved.ID.String(), input.ClientAmendmentID.String(), dependent.String(), first.String(), second.String(), cid.String()} {
		if strings.Contains(pub.Body.String(), secret) || strings.Contains(own.Body.String(), secret) {
			t.Fatal("private amendment escaped staff workspace", secret)
		}
	}
}

func TestPrerequisiteAmendmentsRejectExplicitAndSplitReadinessCycles(t *testing.T) {
	a := testApp(t)
	owner, coordinator, agent := login(t, a, 0), login(t, a, 2), login(t, a, 3)
	_, cid, _ := publicationCaseFixture(t, owner, coordinator, "PRIVATE")
	parent := taskDetail(t, coordinator, cid).Obligations[0].ID
	dependent := proposeSequencedTask(t, coordinator, cid, cityTaskAgency, "PRIVATE dependent work waiting for parent", parent)
	w := coordinator.request("POST", amendmentPath(parent), amendmentInput(dependent), 1, "")
	mustStatus(t, w, 422)
	if parsed[Problem](t, w).Code != "TASK_PREREQUISITE_CYCLE" {
		t.Fatal(w.Body.String())
	}
	_, split := proposePartial(t, agent, cid, parent)
	split = confirmPartial(t, coordinator, cid, split.ID, cityTaskAgency)
	remainder := *split.RemainingTaskID
	before := taskDetail(t, coordinator, cid)
	w = coordinator.request("POST", amendmentPath(remainder), amendmentInput(dependent), 1, "")
	mustStatus(t, w, 422)
	if parsed[Problem](t, w).Code != "TASK_PREREQUISITE_CYCLE" || taskDetail(t, coordinator, cid).Version != before.Version {
		t.Fatal("split readiness cycle mutated case", w.Body.String())
	}
	if slices.Contains(splitTask(t, before, remainder).AvailablePrerequisiteTaskIDs, dependent) {
		t.Fatal("UI offered an implicit split cycle")
	}
	tx, err := integrationAdmin.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	aid := uuid.New()
	_, err = tx.Exec(context.Background(), "INSERT INTO ops.prerequisite_amendment(id,case_id,task_id,client_amendment_id,actor_ref,task_version,added_task_ids,reason,reviewed) VALUES($1,$2,$3,$4,$5,1,$6,'PRIVATE reviewed but circular dependency',true)", aid, cid, remainder, uuid.New(), DemoPrincipals[2], []uuid.UUID{dependent})
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(context.Background(), "INSERT INTO ops.task_prerequisite(case_id,task_id,prerequisite_task_id,amendment_id) VALUES($1,$2,$3,$4)", cid, remainder, dependent, aid)
	var p *pgconn.PgError
	if !errors.As(err, &p) || p.Code != "23514" || !strings.Contains(p.Message, "cycle") {
		t.Fatal("database allowed implicit readiness cycle", err)
	}
	tx.Rollback(context.Background())
	// Depending on the accepted sibling is acyclic and may precede remainder work.
	mustStatus(t, coordinator.request("POST", amendmentPath(remainder), amendmentInput(*split.AcceptedTaskID), 1, ""), 201)
	pendingInput := splitInput()
	pendingInput.AcceptedScope = "PRIVATE north remainder portion"
	pendingInput.RemainingScope = "PRIVATE south remainder portion"
	w = agent.request("POST", "authority/obligations/"+remainder.String()+"/partial-acceptances", pendingInput, 2, "")
	mustStatus(t, w, 201)
	pending := parsed[taskSplitReceipt](t, w)
	d := taskDetail(t, coordinator, cid)
	w = coordinator.request("POST", amendmentPath(remainder), amendmentInput(dependent), splitTask(t, d, remainder).Version, "")
	mustStatus(t, w, 409)
	if parsed[Problem](t, w).Code != "TASK_SPLIT_PENDING" {
		t.Fatal(w.Body.String())
	}
	reject := splitDecision{RequestID: pending.ID, Result: "REJECT", Reason: "PRIVATE review declines this remaining scope split"}
	mustStatus(t, coordinator.request("POST", "authority/cases/"+cid.String()+"/task-split-decisions", reject, d.Version, ""), 200)
}

func TestPrerequisiteAmendmentsFencesPrivacyAndAtomicDatabaseGuards(t *testing.T) {
	a := testApp(t)
	owner, coordinator, agent, verifier := login(t, a, 0), login(t, a, 2), login(t, a, 3), login(t, a, 4)
	_, cid, _ := publicationCaseFixture(t, owner, coordinator, "PRIVATE")
	original := taskDetail(t, coordinator, cid).Obligations[0].ID
	other := proposeSequencedTask(t, coordinator, cid, cityTaskAgency, "PRIVATE separate restoration work")
	input := amendmentInput(original)
	path := amendmentPath(other)
	for _, viewer := range []client{owner, agent, verifier} {
		mustStatus(t, viewer.request("POST", path, input, 1, ""), 403)
	}
	mustStatus(t, coordinator.request("POST", path, input, 0, ""), 428)
	mustStatus(t, coordinator.request("POST", path, input, 2, ""), 412)
	for _, bad := range []prerequisiteAmendmentInput{amendmentInput(), amendmentInput(uuid.Nil), amendmentInput(original, original), amendmentInput(other), amendmentInput(uuid.New())} {
		mustStatus(t, coordinator.request("POST", path, bad, 1, ""), 422)
	}
	unreviewed := input
	unreviewed.Reviewed = false
	mustStatus(t, coordinator.request("POST", path, unreviewed, 1, ""), 422)
	_, creationErr := integrationAdmin.Exec(context.Background(), "UPDATE ops.obligation SET created_at=transaction_timestamp() WHERE id=$1", other)
	var creationPg *pgconn.PgError
	if !errors.As(creationErr, &creationPg) || creationPg.Code != "23514" {
		t.Fatal("task creation time could disguise late additions", creationErr)
	}
	// No later anonymous edge is permitted, even for a coordinator's direct SQL.
	ctx := scopedContext(coordinator, a.Operations, vault.Grant{})
	_, err := a.store(ctx).Exec(ctx, "INSERT INTO ops.task_prerequisite(case_id,task_id,prerequisite_task_id) VALUES($1,$2,$3)", cid, other, original)
	var p *pgconn.PgError
	if !errors.As(err, &p) || p.Code != "23514" {
		t.Fatal("unattributed prerequisite committed", err)
	}
	// A ledger entry without all edges/version is rejected at commit.
	tx, err := integrationAdmin.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	_, err = tx.Exec(context.Background(), "INSERT INTO ops.prerequisite_amendment(id,case_id,task_id,client_amendment_id,actor_ref,task_version,added_task_ids,reason,reviewed) VALUES($1,$2,$3,$4,$5,1,$6,'PRIVATE incomplete prerequisite change',true)", uuid.New(), cid, other, uuid.New(), DemoPrincipals[2], []uuid.UUID{original})
	if err != nil {
		t.Fatal(err)
	}
	err = tx.Commit(context.Background())
	if !errors.As(err, &p) || p.Code != "23514" {
		t.Fatal("incomplete ledger committed", err)
	}
	w := coordinator.request("POST", path, input, 1, "")
	mustStatus(t, w, 201)
	saved := parsed[amendmentReceipt](t, w)
	for _, sql := range []string{"UPDATE ops.prerequisite_amendment SET reason='PRIVATE rewritten decision' WHERE id=$1", "DELETE FROM ops.prerequisite_amendment WHERE id=$1"} {
		_, err = integrationAdmin.Exec(context.Background(), sql, saved.ID)
		if !errors.As(err, &p) || p.Code != "23514" {
			t.Fatal("amendment history mutated", err)
		}
	}
	for _, viewer := range []client{owner, login(t, a, 1)} {
		ctx := scopedContext(viewer, a.Operations, vault.Grant{})
		var count int
		if err = a.store(ctx).QueryRow(ctx, "SELECT count(*) FROM ops.prerequisite_amendment WHERE case_id=$1", cid).Scan(&count); err != nil || count != 0 {
			t.Fatal("amendment RLS leaked history", count, err)
		}
	}
	deniedSQL(t, a.DB, "SELECT * FROM ops.prerequisite_amendment")
	for _, sql := range []string{"UPDATE ops.prerequisite_amendment SET reason=reason", "DELETE FROM ops.prerequisite_amendment"} {
		_, err = a.store(ctx).Exec(ctx, sql)
		if !errors.As(err, &p) || p.Code != "42501" {
			t.Fatal("runtime can rewrite amendment ledger", err)
		}
	}
	performTask(t, agent, other, 2, "accept")
	mustStatus(t, coordinator.request("POST", path, amendmentInput(original), 3, ""), 409)
	_, pending := proposePartial(t, agent, cid, original)
	_, err = integrationAdmin.Exec(context.Background(), "UPDATE ops.task_split_request SET state='REJECTED',reviewer_ref=$2,decision_reason=NULL,reviewed_at=now() WHERE id=$1", pending.ID, DemoPrincipals[2])
	if !errors.As(err, &p) || p.Code != "23514" || p.ConstraintName != "split_review_reason_required" {
		t.Fatal("nullable reason bypassed review requirement", err)
	}
	// A captured actor may not replay a receipt after its coordinator grant is revoked.
	req := httptest.NewRequest("POST", "/v1/"+path, strings.NewReader(string(jsonBytes(input))))
	req.Pattern = "POST /v1/authority/obligations/{id}/prerequisite-amendments"
	req.SetPathValue("id", other.String())
	req.Header.Set("If-Match", "\"1\"")
	req.AddCookie(coordinator.cookie)
	req = a.requestScope(req, uuid.NewString())
	cached, err := a.actor(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		integrationAdmin.Exec(context.Background(), "UPDATE identity.platform_grant SET revoked_at=NULL WHERE principal_id=$1 AND role='COORDINATOR'", DemoPrincipals[2])
	})
	if _, err = integrationAdmin.Exec(context.Background(), "UPDATE identity.platform_grant SET revoked_at=now() WHERE principal_id=$1 AND role='COORDINATOR'", DemoPrincipals[2]); err != nil {
		t.Fatal(err)
	}
	_, _, err = a.amendPrerequisites(httptest.NewRecorder(), req, cached)
	var problem *Problem
	if !errors.As(err, &problem) || problem.Status != 403 {
		t.Fatal("receipt retry bypassed revoked coordinator", err)
	}
	mustStatus(t, coordinator.request("POST", path, input, 1, ""), 403)
}

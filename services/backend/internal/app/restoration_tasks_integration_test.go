package app

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

var cityTaskAgency = uuid.MustParse("30000000-0000-4000-8000-000000000001")
var waterTaskAgency = uuid.MustParse("30000000-0000-4000-8000-000000000002")

type restorationDetail struct {
	State, FirstReportedAt string
	Version                int64
	CanProposeTask         bool
	Obligations            []restorationTask
	TaskSplitRequests      []struct {
		ID, TaskID                                                   uuid.UUID
		State, AcceptedScope, RemainingScope, Reason, DecisionReason string
		CanDecide                                                    bool
	}
}
type restorationTask struct {
	ID, AgencyID                          uuid.UUID
	State, Scope                          string
	Version                               int64
	RequiredForRestoration, CanVerify     bool
	PrerequisiteTaskIDs, BlockedByTaskIDs []uuid.UUID
	ScopeReplaced, CanPartiallyAccept     bool
	ParentTaskID                          *uuid.UUID
	DueAt                                 *string
}
type proposalResult struct {
	ID                   uuid.UUID
	State                string
	Version, CaseVersion int64
}

func taskDetail(t *testing.T, viewer client, cid uuid.UUID) restorationDetail {
	t.Helper()
	w := viewer.request("GET", "authority/cases/"+cid.String(), nil, 0, "")
	mustStatus(t, w, 200)
	return parsed[restorationDetail](t, w)
}

func taskTestGrant(t *testing.T, principal uuid.UUID, role string) {
	t.Helper()
	id := uuid.New()
	_, err := integrationAdmin.Exec(context.Background(), "INSERT INTO identity.organization_grant(id,principal_id,agency_id,role,valid_from,valid_to) VALUES($1,$2,$3,$4,now()-interval '1 day',now()+interval '1 day')", id, principal, waterTaskAgency, role)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		integrationAdmin.Exec(context.Background(), "DELETE FROM identity.organization_grant WHERE id=$1", id)
	})
}

func performTask(t *testing.T, actor client, oid uuid.UUID, version int64, routes ...string) {
	t.Helper()
	for _, route := range routes {
		mustStatus(t, actor.request("POST", "authority/obligations/"+oid.String()+"/"+route, map[string]any{"summary": "PRIVATE fictional task-specific work performed"}, version, ""), 200)
		version++
	}
}

func TestMultipleAgenciesKeepRequiredWorkAndVerification(t *testing.T) {
	a := testApp(t)
	owner, waterAgent, coordinator, cityAgent, verifier := login(t, a, 0), login(t, a, 1), login(t, a, 2), login(t, a, 3), login(t, a, 4)
	taskTestGrant(t, DemoPrincipals[1], "AGENCY_AGENT")
	taskTestGrant(t, DemoPrincipals[1], "VERIFIER")
	taskTestGrant(t, DemoPrincipals[4], "VERIFIER")
	rid, cid, received := publicationCaseFixture(t, owner, coordinator, "SANITIZED_RECEIPT")
	path := "authority/cases/" + cid.String()
	d := taskDetail(t, coordinator, cid)
	first := d.Obligations[0].ID
	w := coordinator.request("POST", path+"/publications", publicationPreview(0, "Fictional joint restoration"), d.Version, "")
	mustStatus(t, w, 200)
	publicID := parsed[publicationCommandResult](t, w).ReceiptID
	performTask(t, cityAgent, first, 1, "accept", "start", "completion-claims")
	d = taskDetail(t, coordinator, cid)
	b := taskProposal{ClientTaskID: uuid.New(), AgencyID: waterTaskAgency, Scope: "PRIVATE repair the water leak before resurfacing"}
	w = coordinator.request("POST", path+"/obligations", b, d.Version, "")
	mustStatus(t, w, 201)
	second := parsed[proposalResult](t, w).ID
	d = taskDetail(t, waterAgent, cid)
	if d.State != "VERIFICATION_PENDING" || len(d.Obligations) != 2 || d.FirstReportedAt != received {
		t.Fatal("additional task hid verification or reset age", d)
	}
	if !d.Obligations[1].RequiredForRestoration || d.Obligations[1].Scope != b.Scope {
		t.Fatal("required task lost its distinct scope", d)
	}
	mustStatus(t, cityAgent.request("POST", "authority/obligations/"+second.String()+"/accept", map[string]any{"summary": "Forged other-agency acceptance"}, 1, ""), 404)
	mustStatus(t, waterAgent.request("POST", "authority/obligations/"+first.String()+"/start", map[string]any{"summary": "Forged other-agency work"}, 4, ""), 404)
	performTask(t, waterAgent, second, 1, "accept", "start")
	d = taskDetail(t, verifier, cid)
	if d.State != "VERIFICATION_PENDING" {
		t.Fatal("other agency work hid completion claim", d)
	}
	mustStatus(t, verifier.request("POST", path+"/verification-decisions", map[string]any{"obligationId": first, "result": "VERIFIED", "reason": "Independent fictional surface restoration inspection"}, d.Version, ""), 200)
	d = taskDetail(t, coordinator, cid)
	if d.State != "ACTIVE" || d.Obligations[0].State != "VERIFIED" {
		t.Fatal("one verified task resolved incomplete required work", d)
	}
	w = owner.request("GET", "my-reports/"+rid.String(), nil, 0, "")
	mustStatus(t, w, 200)
	if parsed[struct{ State string }](t, w).State != "IN_PROGRESS" {
		t.Fatal("owner report was prematurely verified", w.Body.String())
	}
	mustStatus(t, waterAgent.request("GET", "my-reports/"+rid.String(), nil, 0, ""), 404)
	// Optional scaffold work does not alter the required restoration rollup.
	if _, err := integrationAdmin.Exec(context.Background(), "INSERT INTO ops.obligation(id,case_id,agency_id,obligation_type,state,required_for_restoration) VALUES($1,$2,$3,'RESTORATION','PROPOSED',false)", uuid.New(), cid, cityTaskAgency); err != nil {
		t.Fatal(err)
	}
	performTask(t, waterAgent, second, 3, "completion-claims")
	d = taskDetail(t, verifier, cid)
	decision := map[string]any{"obligationId": second, "result": "VERIFIED", "reason": "Independent fictional utility restoration inspection"}
	mustStatus(t, waterAgent.request("POST", path+"/verification-decisions", decision, d.Version, ""), 403)
	mustStatus(t, verifier.request("POST", path+"/verification-decisions", decision, d.Version-1, ""), 412)
	mustStatus(t, verifier.request("POST", path+"/verification-decisions", decision, d.Version, ""), 200)
	d = taskDetail(t, coordinator, cid)
	if d.State != "RESOLVED" || d.CanProposeTask || d.FirstReportedAt != received {
		t.Fatal("final independent verification failed", d)
	}
	w = coordinator.request("POST", path+"/obligations", b, 1, "")
	mustStatus(t, w, 201)
	if replay := parsed[proposalResult](t, w); replay.ID != second || replay.State != "VERIFIED" || replay.CaseVersion != d.Version {
		t.Fatal("late retry did not recover original task", replay)
	}
	b.ClientTaskID = uuid.New()
	mustStatus(t, coordinator.request("POST", path+"/obligations", b, d.Version, ""), 409)
	w = owner.request("GET", "case-receipts/"+publicID.String(), nil, 0, "")
	mustStatus(t, w, 200)
	if strings.Contains(w.Body.String(), "Water Services") || parsed[struct{ State string }](t, w).State != "OPEN" {
		t.Fatal("private tasks bypassed publication review", w.Body.String())
	}
	mustStatus(t, coordinator.request("POST", path+"/publications", publicationPreview(1, "Fictional joint restoration verified"), d.Version, ""), 200)
	w = owner.request("GET", "case-receipts/"+publicID.String(), nil, 0, "")
	mustStatus(t, w, 200)
	for _, secret := range []string{cid.String(), rid.String(), first.String(), second.String(), "PRIVATE", b.ClientTaskID.String()} {
		if strings.Contains(w.Body.String(), secret) {
			t.Fatal("public projection leaked task scope or identifier", secret)
		}
	}
	if !strings.Contains(w.Body.String(), "Water Services") || !strings.Contains(w.Body.String(), "TASK_PROPOSED") {
		t.Fatal("reviewed responsibilities missing", w.Body.String())
	}
	w = owner.request("GET", "my-reports/"+rid.String(), nil, 0, "")
	mustStatus(t, w, 200)
	if parsed[struct{ State string }](t, w).State != "VERIFIED" {
		t.Fatal("all required tasks not shown verified")
	}
}

func TestTaskProposalRetryConcurrencyValidationAndLimit(t *testing.T) {
	a := testApp(t)
	owner, coordinator := login(t, a, 0), login(t, a, 2)
	_, cid, _ := publicationCaseFixture(t, owner, coordinator, "PRIVATE")
	path := "authority/cases/" + cid.String() + "/obligations"
	b := taskProposal{ClientTaskID: uuid.New(), AgencyID: waterTaskAgency, Scope: "PRIVATE distinct utility restoration"}
	responses := make(chan *httptest.ResponseRecorder, 5)
	for i := 0; i < 5; i++ {
		go func() { responses <- coordinator.request("POST", path, b, 1, "") }()
	}
	var oid uuid.UUID
	for i := 0; i < 5; i++ {
		w := <-responses
		mustStatus(t, w, 201)
		result := parsed[proposalResult](t, w)
		if oid == uuid.Nil {
			oid = result.ID
		}
		if result.ID != oid || result.CaseVersion != 2 {
			t.Fatal("retry duplicated a task", result)
		}
	}
	d := taskDetail(t, coordinator, cid)
	if len(d.Obligations) != 2 || d.Version != 2 {
		t.Fatal("proposal retry advanced case again", d)
	}
	var count int
	if err := integrationAdmin.QueryRow(context.Background(), "SELECT count(*) FROM ops.case_event WHERE case_id=$1 AND event_type='TASK_PROPOSED'", cid).Scan(&count); err != nil || count != 1 {
		t.Fatal("retry duplicated decision history", count, err)
	}
	copyID := b.ClientTaskID
	b.ClientTaskID = uuid.New()
	mustStatus(t, coordinator.request("POST", path, b, 2, ""), 409)
	b.ClientTaskID = copyID
	b.Scope = "PRIVATE changed required scope"
	mustStatus(t, coordinator.request("POST", path, b, 2, ""), 409)
	_, err := integrationAdmin.Exec(context.Background(), "UPDATE ops.obligation SET scope_text='replacement scope' WHERE id=$1", oid)
	var violation *pgconn.PgError
	if !errors.As(err, &violation) || violation.Code != "23514" {
		t.Fatal("immutable proposal scope changed", err)
	}
	b.ClientTaskID = uuid.Nil
	mustStatus(t, coordinator.request("POST", path, b, 2, ""), 422)
	b.ClientTaskID = uuid.New()
	mustStatus(t, coordinator.request("POST", path, b, 0, ""), 428)
	mustStatus(t, coordinator.request("POST", path, b, 1, ""), 412)
	b.AgencyID = uuid.New()
	mustStatus(t, coordinator.request("POST", path, b, 2, ""), 422)
	b.AgencyID = cityTaskAgency
	for version := int64(2); version < 7; version++ {
		b.Scope = "PRIVATE distinct restoration phase " + uuid.NewString()
		b.ClientTaskID = uuid.New()
		mustStatus(t, coordinator.request("POST", path, b, version, ""), 201)
	}
	for i := 0; i < 2; i++ {
		proposal := taskProposal{ClientTaskID: uuid.New(), AgencyID: cityTaskAgency, Scope: "PRIVATE last distinct work scope " + uuid.NewString()}
		go func() { responses <- coordinator.request("POST", path, proposal, 7, "") }()
	}
	success, stale := 0, 0
	for i := 0; i < 2; i++ {
		w := <-responses
		if w.Code == 201 {
			success++
		} else if w.Code == 412 {
			stale++
		} else {
			t.Fatal("unexpected cap race response", w.Code, w.Body.String())
		}
	}
	if success != 1 || stale != 1 {
		t.Fatal("concurrent proposals bypassed version fencing", success, stale)
	}
	d = taskDetail(t, coordinator, cid)
	if len(d.Obligations) != 8 || d.CanProposeTask {
		t.Fatal("task limit not reflected in capability", d)
	}
	b.ClientTaskID = uuid.New()
	w := coordinator.request("POST", path, b, d.Version, "")
	mustStatus(t, w, 409)
	if parsed[Problem](t, w).Code != "TASK_LIMIT" {
		t.Fatal("wrong task limit code")
	}
}

func TestTaskProposalRechecksCoordinatorIncludingRetries(t *testing.T) {
	a := testApp(t)
	owner, coordinator, officer := login(t, a, 0), login(t, a, 2), login(t, a, 3)
	_, cid, _ := publicationCaseFixture(t, owner, coordinator, "PRIVATE")
	b := taskProposal{ClientTaskID: uuid.New(), AgencyID: cityTaskAgency, Scope: "PRIVATE remaining required restoration"}
	path := "authority/cases/" + cid.String() + "/obligations"
	mustStatus(t, owner.request("POST", path, b, 1, ""), 403)
	mustStatus(t, officer.request("POST", path, b, 1, ""), 403)
	mustStatus(t, coordinator.request("POST", path, b, 1, ""), 201)
	req := httptest.NewRequest("POST", "/v1/"+path, strings.NewReader(string(jsonBytes(b))))
	req.SetPathValue("id", cid.String())
	req.Header.Set("If-Match", `"1"`)
	req.AddCookie(coordinator.cookie)
	req = a.requestScope(req, uuid.NewString())
	cached, err := a.actor(req)
	if err != nil || cached == nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		integrationAdmin.Exec(context.Background(), "UPDATE identity.platform_grant SET revoked_at=NULL WHERE principal_id=$1 AND role='COORDINATOR'", DemoPrincipals[2])
	})
	if _, err = integrationAdmin.Exec(context.Background(), "UPDATE identity.platform_grant SET revoked_at=now() WHERE principal_id=$1 AND role='COORDINATOR'", DemoPrincipals[2]); err != nil {
		t.Fatal(err)
	}
	_, _, err = a.proposeObligation(httptest.NewRecorder(), req, cached)
	var problem *Problem
	if !errors.As(err, &problem) || problem.Status != 403 {
		t.Fatal("cached proposal retry bypassed revoked authority", err)
	}
}

func TestMultiTaskInspectionFailurePreservesOtherClaim(t *testing.T) {
	a := testApp(t)
	owner, coordinator, agent, verifier := login(t, a, 0), login(t, a, 2), login(t, a, 3), login(t, a, 4)
	_, cid, _ := publicationCaseFixture(t, owner, coordinator, "PRIVATE")
	path := "authority/cases/" + cid.String()
	first := taskDetail(t, coordinator, cid).Obligations[0].ID
	b := taskProposal{ClientTaskID: uuid.New(), AgencyID: cityTaskAgency, Scope: "PRIVATE separate restoration requiring another inspection"}
	w := coordinator.request("POST", path+"/obligations", b, 1, "")
	mustStatus(t, w, 201)
	second := parsed[proposalResult](t, w).ID
	performTask(t, agent, first, 1, "accept", "start", "completion-claims")
	performTask(t, agent, second, 1, "accept", "start", "completion-claims")
	decide := func(oid uuid.UUID, result string) {
		t.Helper()
		d := taskDetail(t, verifier, cid)
		mustStatus(t, verifier.request("POST", path+"/verification-decisions", map[string]any{"obligationId": oid, "result": result, "reason": "Independent fictional inspection documented for this task"}, d.Version, ""), 200)
	}
	decide(first, "NOT_RESTORED")
	d := taskDetail(t, verifier, cid)
	if d.State != "VERIFICATION_PENDING" || d.Obligations[0].State != "IN_PROGRESS" || d.Obligations[1].State != "COMPLETION_CLAIMED" {
		t.Fatal("failed inspection hid another claim", d)
	}
	decide(second, "VERIFIED")
	if d = taskDetail(t, verifier, cid); d.State != "ACTIVE" {
		t.Fatal("failed required work counted as restored", d)
	}
	performTask(t, agent, first, d.Obligations[0].Version, "completion-claims")
	decide(first, "INSUFFICIENT")
	if d = taskDetail(t, verifier, cid); d.State != "VERIFICATION_PENDING" || d.Obligations[0].State != "COMPLETION_CLAIMED" {
		t.Fatal("insufficient evidence counted as restoration", d)
	}
	decide(first, "VERIFIED")
	if d = taskDetail(t, verifier, cid); d.State != "RESOLVED" {
		t.Fatal("subsequent independent verification failed", d)
	}
}

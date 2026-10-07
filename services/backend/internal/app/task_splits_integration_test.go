package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/lavkushry/JanSetu-AI/services/backend/internal/vault"
)

type taskSplitReceipt struct {
	ID                              uuid.UUID
	State                           string
	TaskVersion, CaseVersion        int64
	AcceptedTaskID, RemainingTaskID *uuid.UUID
}

func TestPartialAcceptanceIndependentReviewAndRevokedRetryAuthority(t *testing.T) {
	a := testApp(t)
	owner, coordinator, agent := login(t, a, 0), login(t, a, 2), login(t, a, 3)
	_, cid, _ := publicationCaseFixture(t, owner, coordinator, "PRIVATE")
	oid := taskDetail(t, coordinator, cid).Obligations[0].ID
	b, s := proposePartial(t, agent, cid, oid)
	decision := splitDecision{RequestID: s.ID, Result: "APPROVE", RemainingAgencyID: cityTaskAgency, Reviewed: true, Reason: "PRIVATE reviewed complete non-overlapping scope coverage"}
	t.Run("proposer cannot review even with coordinator role", func(t *testing.T) {
		gid := uuid.New()
		_, err := integrationAdmin.Exec(context.Background(), "INSERT INTO identity.platform_grant(id,principal_id,role,valid_to) VALUES($1,$2,'COORDINATOR',now()+interval '1 day')", gid, DemoPrincipals[3])
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			integrationAdmin.Exec(context.Background(), "DELETE FROM identity.platform_grant WHERE id=$1", gid)
		})
		mustStatus(t, agent.request("POST", "authority/cases/"+cid.String()+"/task-split-decisions", decision, s.CaseVersion, ""), 403)
		d := taskDetail(t, agent, cid)
		if d.TaskSplitRequests[0].CanDecide {
			t.Fatal("proposer received self-review UI capability")
		}
	})
	approved := confirmPartial(t, coordinator, cid, s.ID, cityTaskAgency)
	constraint := func(sql string, args ...any) {
		t.Helper()
		_, err := integrationAdmin.Exec(context.Background(), sql, args...)
		var p *pgconn.PgError
		if !errors.As(err, &p) || p.Code != "23514" {
			t.Fatal("missing approved split guard", err)
		}
	}
	constraint("UPDATE ops.obligation SET scope_text='Rewritten archived scope' WHERE id=$1", oid)
	constraint("UPDATE ops.obligation SET required_for_restoration=false WHERE id=$1", *approved.RemainingTaskID)
	constraint("UPDATE ops.obligation SET parent_obligation_id=NULL WHERE id=$1", *approved.AcceptedTaskID)
	constraint("UPDATE ops.task_split_request SET decision_reason='Changed approved review reason' WHERE id=$1", s.ID)
	capture := func(path string, body any, version int64, viewer client) (*Actor, *http.Request) {
		r := httptest.NewRequest("POST", "/v1/"+path, strings.NewReader(string(jsonBytes(body))))
		r.Pattern = "POST /v1/authority/"
		r.Header.Set("If-Match", fmt.Sprintf("\"%d\"", version))
		r.AddCookie(viewer.cookie)
		r = a.requestScope(r, uuid.NewString())
		actor, err := a.actor(r)
		if err != nil || actor == nil {
			t.Fatal(err)
		}
		return actor, r
	}
	proposalPath := "authority/obligations/" + oid.String() + "/partial-acceptances"
	cachedAgent, proposalReq := capture(proposalPath, b, 1, agent)
	proposalReq.SetPathValue("id", oid.String())
	decisionPath := "authority/cases/" + cid.String() + "/task-split-decisions"
	cachedCoordinator, decisionReq := capture(decisionPath, decision, s.CaseVersion, coordinator)
	decisionReq.SetPathValue("id", cid.String())
	t.Cleanup(func() {
		integrationAdmin.Exec(context.Background(), "UPDATE identity.organization_grant SET revoked_at=NULL WHERE principal_id=$1 AND agency_id=$2", DemoPrincipals[3], cityTaskAgency)
		integrationAdmin.Exec(context.Background(), "UPDATE identity.platform_grant SET revoked_at=NULL WHERE principal_id=$1 AND role='COORDINATOR'", DemoPrincipals[2])
	})
	if _, err := integrationAdmin.Exec(context.Background(), "UPDATE identity.organization_grant SET revoked_at=now() WHERE principal_id=$1 AND agency_id=$2", DemoPrincipals[3], cityTaskAgency); err != nil {
		t.Fatal(err)
	}
	_, _, err := a.partiallyAcceptTask(httptest.NewRecorder(), proposalReq, cachedAgent)
	if err == nil {
		t.Fatal("cached agency retry bypassed revoked grant")
	}
	mustStatus(t, agent.request("POST", proposalPath, b, 1, ""), 404)
	if _, err = integrationAdmin.Exec(context.Background(), "UPDATE identity.platform_grant SET revoked_at=now() WHERE principal_id=$1 AND role='COORDINATOR'", DemoPrincipals[2]); err != nil {
		t.Fatal(err)
	}
	_, _, err = a.decideTaskSplit(httptest.NewRecorder(), decisionReq, cachedCoordinator)
	var problem *Problem
	if !errors.As(err, &problem) || problem.Status != 403 {
		t.Fatal("cached coordinator retry bypassed revoked role", err)
	}
	mustStatus(t, coordinator.request("POST", decisionPath, decision, s.CaseVersion, ""), 403)
	var currentVersion int64
	if err = integrationAdmin.QueryRow(context.Background(), "SELECT version FROM ops.case_record WHERE id=$1", cid).Scan(&currentVersion); err != nil || currentVersion != approved.CaseVersion {
		t.Fatal("revoked retry changed case", currentVersion, err)
	}
}

func splitTask(t *testing.T, d restorationDetail, oid uuid.UUID) restorationTask {
	t.Helper()
	for _, o := range d.Obligations {
		if o.ID == oid {
			return o
		}
	}
	t.Fatal("task missing", oid, d)
	return restorationTask{}
}
func splitInput() partialAcceptance {
	return partialAcceptance{ClientRequestID: uuid.New(), AcceptedScope: "PRIVATE repair the defined pavement portion", RemainingScope: "PRIVATE repair the separate drainage portion", AuthorityBasisRef: localTaskAuthority, Reason: "PRIVATE agency can accept only the pavement scope"}
}
func proposePartial(t *testing.T, agent client, cid, oid uuid.UUID) (partialAcceptance, taskSplitReceipt) {
	t.Helper()
	b := splitInput()
	o := splitTask(t, taskDetail(t, agent, cid), oid)
	w := agent.request("POST", "authority/obligations/"+oid.String()+"/partial-acceptances", b, o.Version, "")
	mustStatus(t, w, 201)
	return b, parsed[taskSplitReceipt](t, w)
}
func confirmPartial(t *testing.T, coordinator client, cid, sid, agency uuid.UUID) taskSplitReceipt {
	t.Helper()
	b := splitDecision{RequestID: sid, Result: "APPROVE", RemainingAgencyID: agency, Reviewed: true, Reason: "PRIVATE reviewed complete non-overlapping scope coverage"}
	d := taskDetail(t, coordinator, cid)
	w := coordinator.request("POST", "authority/cases/"+cid.String()+"/task-split-decisions", b, d.Version, "")
	mustStatus(t, w, 200)
	return parsed[taskSplitReceipt](t, w)
}
func blockedSplitWork(t *testing.T, agent client, cid, oid uuid.UUID) {
	t.Helper()
	d := taskDetail(t, agent, cid)
	o := splitTask(t, d, oid)
	w := agent.request("POST", "authority/obligations/"+oid.String()+"/start", map[string]any{"summary": "Attempt work before all required predecessor scopes are verified"}, o.Version, "")
	mustStatus(t, w, 409)
	if parsed[Problem](t, w).Code != "TASK_PREREQUISITES_PENDING" || taskDetail(t, agent, cid).Version != d.Version {
		t.Fatal("blocked split work changed state", w.Body.String())
	}
}

func TestPartialAcceptancePreservesCrossAgencyRemainderAndDependents(t *testing.T) {
	a := testApp(t)
	owner, coordinator, city, water, verifier := login(t, a, 0), login(t, a, 2), login(t, a, 3), login(t, a, 1), login(t, a, 4)
	taskTestGrant(t, DemoPrincipals[1], "AGENCY_AGENT")
	taskTestGrant(t, DemoPrincipals[4], "VERIFIER")
	rid, cid, received := publicationCaseFixture(t, owner, coordinator, "SANITIZED_RECEIPT")
	path := "authority/cases/" + cid.String()
	prerequisite := taskDetail(t, coordinator, cid).Obligations[0].ID
	public := coordinator.request("POST", path+"/publications", publicationPreview(0, "Fictional scope restoration"), 1, "")
	mustStatus(t, public, 200)
	receiptID := parsed[publicationCommandResult](t, public).ReceiptID
	original := proposeSequencedTask(t, coordinator, cid, cityTaskAgency, "PRIVATE joint pavement and drainage restoration", prerequisite)
	dependent := proposeSequencedTask(t, coordinator, cid, cityTaskAgency, "PRIVATE finish crossing after joint scope restoration", original)
	_, err := integrationAdmin.Exec(context.Background(), "UPDATE ops.obligation SET due_at=now()+interval '3 days' WHERE id=$1", original)
	if err != nil {
		t.Fatal(err)
	}
	originalScope := splitTask(t, taskDetail(t, coordinator, cid), original)
	b, request := proposePartial(t, city, cid, original)
	d := taskDetail(t, coordinator, cid)
	if request.State != "PENDING" || len(d.Obligations) != 3 || splitTask(t, d, original).State != "PROPOSED" || d.FirstReportedAt != received {
		t.Fatal("partial request prematurely split/accepted work", d, request)
	}
	w := city.request("POST", "authority/obligations/"+original.String()+"/accept", map[string]any{"summary": "Attempt whole acceptance while partial review is pending"}, 2, "")
	mustStatus(t, w, 409)
	if parsed[Problem](t, w).Code != "TASK_SPLIT_PENDING" {
		t.Fatal("pending split failed to fence whole acceptance")
	}
	approved := confirmPartial(t, coordinator, cid, request.ID, waterTaskAgency)
	if approved.AcceptedTaskID == nil || approved.RemainingTaskID == nil {
		t.Fatal("approved split lost required children", approved)
	}
	accepted, remaining := *approved.AcceptedTaskID, *approved.RemainingTaskID
	d = taskDetail(t, coordinator, cid)
	parent, part, rest := splitTask(t, d, original), splitTask(t, d, accepted), splitTask(t, d, remaining)
	if !parent.ScopeReplaced || parent.RequiredForRestoration || parent.State != "CANCELLED" || parent.Scope != originalScope.Scope || len(d.Obligations) != 5 || d.FirstReportedAt != received {
		t.Fatal("original scope/history was discarded or double-counted", d)
	}
	for _, child := range []restorationTask{part, rest} {
		if !child.RequiredForRestoration || child.ParentTaskID == nil || *child.ParentTaskID != original || !slices.Equal(child.PrerequisiteTaskIDs, []uuid.UUID{prerequisite}) || child.DueAt == nil || originalScope.DueAt == nil || *child.DueAt != *originalScope.DueAt {
			t.Fatal("replacement lost scope, due date or prerequisite", child)
		}
	}
	if part.State != "ACCEPTED" || part.AgencyID != cityTaskAgency || part.Scope != b.AcceptedScope || rest.State != "PROPOSED" || rest.AgencyID != waterTaskAgency || rest.Scope != b.RemainingScope {
		t.Fatal("partial acceptance or required remainder assigned incorrectly", part, rest)
	}
	mustStatus(t, city.request("POST", "authority/obligations/"+remaining.String()+"/accept", map[string]any{"summary": "Unauthorized remaining agency acceptance"}, 1, ""), 404)
	performTask(t, water, remaining, 1, "accept")
	performTask(t, city, dependent, 1, "accept")
	blockedSplitWork(t, city, cid, accepted)
	blockedSplitWork(t, water, cid, remaining)
	blockedSplitWork(t, city, cid, dependent)
	performTask(t, city, prerequisite, 1, "accept", "start", "completion-claims")
	inspectSequencedTask(t, verifier, cid, prerequisite, "VERIFIED")
	performTask(t, city, accepted, 2, "start", "completion-claims")
	inspectSequencedTask(t, verifier, cid, accepted, "VERIFIED")
	blockedSplitWork(t, city, cid, dependent)
	d = taskDetail(t, coordinator, cid)
	if d.State == "RESOLVED" || splitTask(t, d, remaining).State != "ACCEPTED" {
		t.Fatal("accepted portion hid required remainder", d)
	}
	own := owner.request("GET", "my-reports/"+rid.String(), nil, 0, "")
	mustStatus(t, own, 200)
	if parsed[struct{ State string }](t, own).State == "VERIFIED" {
		t.Fatal("resident saw partial restoration as complete")
	}
	performTask(t, water, remaining, 2, "start", "completion-claims")
	inspectSequencedTask(t, verifier, cid, remaining, "VERIFIED")
	d = taskDetail(t, coordinator, cid)
	if len(splitTask(t, d, dependent).BlockedByTaskIDs) != 0 || d.State == "RESOLVED" {
		t.Fatal("original dependency was bypassed or never released", d)
	}
	performTask(t, city, dependent, 2, "start", "completion-claims")
	inspectSequencedTask(t, verifier, cid, dependent, "VERIFIED")
	d = taskDetail(t, coordinator, cid)
	if d.State != "RESOLVED" || d.FirstReportedAt != received {
		t.Fatal("complete verified split did not resolve with original age", d)
	}
	own = owner.request("GET", "my-reports/"+rid.String(), nil, 0, "")
	mustStatus(t, own, 200)
	progress := parsed[struct {
		State            string
		Responsibilities []any
	}](t, own)
	if progress.State != "VERIFIED" || len(progress.Responsibilities) != 4 {
		t.Fatal("owner progress retained replaced parent or lost required work", own.Body.String())
	}
	before := owner.request("GET", "case-receipts/"+receiptID.String(), nil, 0, "")
	mustStatus(t, before, 200)
	if parsed[struct{ State string }](t, before).State != "OPEN" {
		t.Fatal("split auto-published private work")
	}
	mustStatus(t, coordinator.request("POST", path+"/publications", publicationPreview(1, "Fictional complete split restored"), d.Version, ""), 200)
	after := owner.request("GET", "case-receipts/"+receiptID.String(), nil, 0, "")
	mustStatus(t, after, 200)
	if len(parsed[struct{ Responsibilities []any }](t, after).Responsibilities) != 4 {
		t.Fatal("public projection retained historical split parent")
	}
	for _, response := range []*httptest.ResponseRecorder{own, after} {
		for _, secret := range []string{b.AcceptedScope, b.RemainingScope, b.Reason, original.String(), accepted.String(), remaining.String(), request.ID.String(), b.ClientRequestID.String(), "taskSplitRequests", "parentTaskId", "scopeReplaced"} {
			if strings.Contains(response.Body.String(), secret) {
				t.Fatal("private split data leaked", secret)
			}
		}
	}
	replay := city.request("POST", "authority/obligations/"+original.String()+"/partial-acceptances", b, 1, "")
	mustStatus(t, replay, 201)
	if parsed[taskSplitReceipt](t, replay).State != "APPROVED" {
		t.Fatal("late request retry lost reviewed result")
	}
	approvedReplay := coordinator.request("POST", path+"/task-split-decisions", splitDecision{RequestID: request.ID, Result: "APPROVE", RemainingAgencyID: waterTaskAgency, Reviewed: true, Reason: "PRIVATE reviewed complete non-overlapping scope coverage"}, request.CaseVersion, "")
	mustStatus(t, approvedReplay, 200)
	if taskDetail(t, coordinator, cid).Version != d.Version {
		t.Fatal("late split retries changed history")
	}
}

func TestPartialAcceptanceNestedRemainderConservesRestoration(t *testing.T) {
	a := testApp(t)
	owner, coordinator, agent, verifier := login(t, a, 0), login(t, a, 2), login(t, a, 3), login(t, a, 4)
	_, cid, _ := publicationCaseFixture(t, owner, coordinator, "PRIVATE")
	original := taskDetail(t, coordinator, cid).Obligations[0].ID
	dependent := proposeSequencedTask(t, coordinator, cid, cityTaskAgency, "PRIVATE dependent final inspection after all original work", original)
	_, first := proposePartial(t, agent, cid, original)
	pair := confirmPartial(t, coordinator, cid, first.ID, cityTaskAgency)
	remaining := *pair.RemainingTaskID
	nested := splitInput()
	nested.AcceptedScope = "PRIVATE drainage inlet repair portion"
	nested.RemainingScope = "PRIVATE drainage outlet repair remainder"
	w := agent.request("POST", "authority/obligations/"+remaining.String()+"/partial-acceptances", nested, 1, "")
	mustStatus(t, w, 201)
	second := parsed[taskSplitReceipt](t, w)
	nestedPair := confirmPartial(t, coordinator, cid, second.ID, cityTaskAgency)
	performTask(t, agent, dependent, 1, "accept")
	for _, oid := range []uuid.UUID{*pair.AcceptedTaskID, *nestedPair.AcceptedTaskID} {
		performTask(t, agent, oid, 2, "start", "completion-claims")
		inspectSequencedTask(t, verifier, cid, oid, "VERIFIED")
	}
	blockedSplitWork(t, agent, cid, dependent)
	performTask(t, agent, *nestedPair.RemainingTaskID, 1, "accept", "start", "completion-claims")
	inspectSequencedTask(t, verifier, cid, *nestedPair.RemainingTaskID, "INSUFFICIENT")
	blockedSplitWork(t, agent, cid, dependent)
	inspectSequencedTask(t, verifier, cid, *nestedPair.RemainingTaskID, "VERIFIED")
	performTask(t, agent, dependent, 2, "start", "completion-claims")
	inspectSequencedTask(t, verifier, cid, dependent, "VERIFIED")
	d := taskDetail(t, coordinator, cid)
	if d.State != "RESOLVED" || len(d.Obligations) != 6 {
		t.Fatal("nested split lost required remainder or original history", d)
	}
	var restored bool
	if err := integrationAdmin.QueryRow(context.Background(), "SELECT ops.task_restored($1)", original).Scan(&restored); err != nil || !restored {
		t.Fatal("nested scope verification did not satisfy original dependency", err)
	}
}

func TestPartialAcceptanceInheritsAlreadySplitPrerequisite(t *testing.T) {
	a := testApp(t)
	owner, coordinator, agent, verifier := login(t, a, 0), login(t, a, 2), login(t, a, 3), login(t, a, 4)
	_, cid, _ := publicationCaseFixture(t, owner, coordinator, "PRIVATE")
	prerequisite := taskDetail(t, coordinator, cid).Obligations[0].ID
	dependent := proposeSequencedTask(t, coordinator, cid, cityTaskAgency, "PRIVATE restore surface after all drainage scopes", prerequisite)
	_, first := proposePartial(t, agent, cid, prerequisite)
	first = confirmPartial(t, coordinator, cid, first.ID, cityTaskAgency)
	input := splitInput()
	input.AcceptedScope = "PRIVATE restore the north surface after drainage"
	input.RemainingScope = "PRIVATE restore the south surface after drainage"
	w := agent.request("POST", "authority/obligations/"+dependent.String()+"/partial-acceptances", input, 1, "")
	mustStatus(t, w, 201)
	second := confirmPartial(t, coordinator, cid, parsed[taskSplitReceipt](t, w).ID, cityTaskAgency)
	d := taskDetail(t, coordinator, cid)
	for _, child := range []uuid.UUID{*second.AcceptedTaskID, *second.RemainingTaskID} {
		task := splitTask(t, d, child)
		if !slices.Equal(task.PrerequisiteTaskIDs, []uuid.UUID{prerequisite}) || !slices.Equal(task.BlockedByTaskIDs, []uuid.UUID{prerequisite}) {
			t.Fatal("split lost historical prerequisite", task)
		}
	}
	blockedSplitWork(t, agent, cid, *second.AcceptedTaskID)
	performTask(t, agent, *first.AcceptedTaskID, 2, "start", "completion-claims")
	inspectSequencedTask(t, verifier, cid, *first.AcceptedTaskID, "VERIFIED")
	blockedSplitWork(t, agent, cid, *second.AcceptedTaskID)
	performTask(t, agent, *first.RemainingTaskID, 1, "accept", "start", "completion-claims")
	inspectSequencedTask(t, verifier, cid, *first.RemainingTaskID, "VERIFIED")
	performTask(t, agent, *second.AcceptedTaskID, 2, "start", "completion-claims")
	inspectSequencedTask(t, verifier, cid, *second.AcceptedTaskID, "VERIFIED")
	performTask(t, agent, *second.RemainingTaskID, 1, "accept", "start", "completion-claims")
	inspectSequencedTask(t, verifier, cid, *second.RemainingTaskID, "VERIFIED")
	if taskDetail(t, coordinator, cid).State != "RESOLVED" {
		t.Fatal("inherited reviewed prerequisite did not resolve")
	}
}

func TestPartialAcceptanceRejectsIncompleteDatabaseConfirmation(t *testing.T) {
	a := testApp(t)
	owner, coordinator, agent := login(t, a, 0), login(t, a, 2), login(t, a, 3)
	_, cid, _ := publicationCaseFixture(t, owner, coordinator, "PRIVATE")
	oid := taskDetail(t, coordinator, cid).Obligations[0].ID
	input, s := proposePartial(t, agent, cid, oid)
	tx, err := integrationAdmin.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	accepted, remaining := uuid.New(), uuid.New()
	for _, child := range []struct {
		id    uuid.UUID
		scope string
	}{{accepted, input.AcceptedScope}, {remaining, input.RemainingScope}} {
		_, err = tx.Exec(context.Background(), "INSERT INTO ops.obligation(id,case_id,agency_id,obligation_type,state,scope_text,parent_obligation_id) VALUES($1,$2,$3,'RESTORATION','PROPOSED',$4,$5)", child.id, cid, cityTaskAgency, child.scope, oid)
		if err != nil {
			t.Fatal(err)
		}
	}
	_, err = tx.Exec(context.Background(), "UPDATE ops.task_split_request SET state='APPROVED',reviewer_ref=$2,decision_reason='PRIVATE reviewed entire scope coverage',reviewed_at=now(),accepted_task_id=$3,remaining_task_id=$4,remaining_agency_id=$5 WHERE id=$1", s.ID, DemoPrincipals[2], accepted, remaining, cityTaskAgency)
	if err != nil {
		t.Fatal(err)
	}
	err = tx.Commit(context.Background())
	var p *pgconn.PgError
	if !errors.As(err, &p) || p.Code != "23514" {
		t.Fatal("incomplete confirmation committed", err)
	}
	d := taskDetail(t, coordinator, cid)
	if len(d.Obligations) != 1 || d.TaskSplitRequests[0].State != "PENDING" || d.Version != s.CaseVersion {
		t.Fatal("incomplete split did not roll back atomically", d)
	}
}

func TestPartialAcceptanceConcurrentRetriesAndDecisionFences(t *testing.T) {
	a := testApp(t)
	owner, coordinator, agent := login(t, a, 0), login(t, a, 2), login(t, a, 3)
	_, cid, _ := publicationCaseFixture(t, owner, coordinator, "PRIVATE")
	oid := taskDetail(t, coordinator, cid).Obligations[0].ID
	path := "authority/obligations/" + oid.String() + "/partial-acceptances"
	b := splitInput()
	mustStatus(t, owner.request("POST", path, b, 1, ""), 404)
	mustStatus(t, coordinator.request("POST", path, b, 1, ""), 403)
	mustStatus(t, agent.request("POST", path, b, 0, ""), 428)
	responses := make(chan *httptest.ResponseRecorder, 5)
	for i := 0; i < 5; i++ {
		go func() { responses <- agent.request("POST", path, b, 1, "") }()
	}
	var result taskSplitReceipt
	for i := 0; i < 5; i++ {
		w := <-responses
		mustStatus(t, w, 201)
		s := parsed[taskSplitReceipt](t, w)
		if result.ID != uuid.Nil && result.ID != s.ID {
			t.Fatal("retry created multiple requests")
		}
		result = s
	}
	d := taskDetail(t, coordinator, cid)
	if d.Version != 2 || len(d.Obligations) != 1 || len(d.TaskSplitRequests) != 1 {
		t.Fatal("request retry duplicated state/history", d)
	}
	changed := b
	changed.RemainingScope = "PRIVATE changed remaining work scope"
	w := agent.request("POST", path, changed, 1, "")
	mustStatus(t, w, 409)
	if parsed[Problem](t, w).Code != "TASK_SPLIT_CONFLICT" {
		t.Fatal("changed request reused identity")
	}
	changed = b
	changed.ClientRequestID = uuid.New()
	mustStatus(t, agent.request("POST", path, changed, 1, ""), 412)
	w = agent.request("POST", path, changed, 2, "")
	mustStatus(t, w, 409)
	if parsed[Problem](t, w).Code != "TASK_SPLIT_PENDING" {
		t.Fatal("second partial request replaced pending scope")
	}
	for _, bad := range []partialAcceptance{{ClientRequestID: uuid.Nil}, {ClientRequestID: uuid.New(), AcceptedScope: b.AcceptedScope, RemainingScope: b.AcceptedScope, Reason: b.Reason, AuthorityBasisRef: localTaskAuthority}, {ClientRequestID: uuid.New(), AcceptedScope: b.AcceptedScope, RemainingScope: b.RemainingScope, Reason: b.Reason, AuthorityBasisRef: "FORGED_REAL_MANDATE"}} {
		mustStatus(t, agent.request("POST", path, bad, 2, ""), 422)
	}
	decisionPath := "authority/cases/" + cid.String() + "/task-split-decisions"
	decision := splitDecision{RequestID: result.ID, Result: "APPROVE", RemainingAgencyID: cityTaskAgency, Reviewed: true, Reason: "PRIVATE complete non-overlapping split review"}
	mustStatus(t, agent.request("POST", decisionPath, decision, 2, ""), 403)
	noReview := decision
	noReview.Reviewed = false
	mustStatus(t, coordinator.request("POST", decisionPath, noReview, 2, ""), 422)
	noAgency := decision
	noAgency.RemainingAgencyID = uuid.New()
	mustStatus(t, coordinator.request("POST", decisionPath, noAgency, 2, ""), 422)
	mustStatus(t, coordinator.request("POST", decisionPath, decision, 1, ""), 412)
	for i := 0; i < 5; i++ {
		go func() { responses <- coordinator.request("POST", decisionPath, decision, 2, "") }()
	}
	var approved taskSplitReceipt
	for i := 0; i < 5; i++ {
		w := <-responses
		mustStatus(t, w, 200)
		s := parsed[taskSplitReceipt](t, w)
		if approved.AcceptedTaskID != nil && *approved.AcceptedTaskID != *s.AcceptedTaskID {
			t.Fatal("decision retry created another split")
		}
		approved = s
	}
	d = taskDetail(t, coordinator, cid)
	if d.Version != 3 || len(d.Obligations) != 3 {
		t.Fatal("review retry duplicated work", d)
	}
	var proposedEvents, approvedEvents, outbox int
	err := integrationAdmin.QueryRow(context.Background(), "SELECT (SELECT count(*) FROM ops.case_event WHERE case_id=$1 AND event_type='TASK_SPLIT_REQUESTED'),(SELECT count(*) FROM ops.case_event WHERE case_id=$1 AND event_type='TASK_SPLIT_APPROVED'),(SELECT count(*) FROM infra.outbox WHERE aggregate_id=$1 AND event_type='ObligationChanged')", cid).Scan(&proposedEvents, &approvedEvents, &outbox)
	if err != nil || proposedEvents != 1 || approvedEvents != 1 || outbox != 2 {
		t.Fatal("split retries changed audit/outbox", proposedEvents, approvedEvents, outbox, err)
	}
	conflict := decision
	conflict.Reason = "PRIVATE changed finalized review reason"
	w = coordinator.request("POST", decisionPath, conflict, 2, "")
	mustStatus(t, w, 409)
	if parsed[Problem](t, w).Code != "TASK_SPLIT_DECISION_CONFLICT" {
		t.Fatal("review decision was overwritten")
	}
	_, foreignCase, _ := publicationCaseFixture(t, owner, coordinator, "PRIVATE")
	mustStatus(t, coordinator.request("POST", "authority/cases/"+foreignCase.String()+"/task-split-decisions", decision, 1, ""), 404)
}

func TestPartialAcceptanceCapacityRejectionAndDatabaseGuards(t *testing.T) {
	a := testApp(t)
	owner, coordinator, agent := login(t, a, 0), login(t, a, 2), login(t, a, 3)
	_, cid, _ := publicationCaseFixture(t, owner, coordinator, "PRIVATE")
	oid := taskDetail(t, coordinator, cid).Obligations[0].ID
	b, s := proposePartial(t, agent, cid, oid)
	constraint := func(sql string, args ...any) {
		t.Helper()
		_, err := integrationAdmin.Exec(context.Background(), sql, args...)
		var p *pgconn.PgError
		if !errors.As(err, &p) || p.Code != "23514" {
			t.Fatal("missing split database guard", err)
		}
	}
	constraint("UPDATE ops.obligation SET state='ACCEPTED',accepted_at=now() WHERE id=$1", oid)
	constraint("UPDATE ops.task_split_request SET accepted_scope='Rewritten private accepted scope' WHERE id=$1", s.ID)
	for i := 0; i < 6; i++ {
		proposeSequencedTask(t, coordinator, cid, cityTaskAgency, "PRIVATE additional separate required work "+uuid.NewString())
	}
	d := taskDetail(t, coordinator, cid)
	decision := splitDecision{RequestID: s.ID, Result: "APPROVE", RemainingAgencyID: cityTaskAgency, Reviewed: true, Reason: "PRIVATE reviewed scope but capacity was consumed"}
	path := "authority/cases/" + cid.String() + "/task-split-decisions"
	w := coordinator.request("POST", path, decision, d.Version, "")
	mustStatus(t, w, 409)
	if parsed[Problem](t, w).Code != "TASK_LIMIT" || len(taskDetail(t, coordinator, cid).Obligations) != 7 {
		t.Fatal("capacity race partially created split", w.Body.String())
	}
	decision.Result = "REJECT"
	decision.RemainingAgencyID = uuid.Nil
	decision.Reviewed = false
	decision.Reason = "PRIVATE remaining work requires a different complete scope proposal"
	mustStatus(t, coordinator.request("POST", path, decision, d.Version, ""), 200)
	mustStatus(t, coordinator.request("POST", path, decision, d.Version, ""), 200)
	d = taskDetail(t, coordinator, cid)
	if d.TaskSplitRequests[0].State != "REJECTED" || splitTask(t, d, oid).State != "PROPOSED" || splitTask(t, d, oid).ScopeReplaced {
		t.Fatal("rejection discarded original required work", d)
	}
	mustStatus(t, agent.request("POST", "authority/obligations/"+oid.String()+"/partial-acceptances", b, 1, ""), 201)
	performTask(t, agent, oid, 3, "accept")
	constraint("UPDATE ops.task_split_request SET state='PENDING' WHERE id=$1", s.ID)
	for _, viewer := range []client{owner, login(t, a, 1)} {
		ctx := scopedContext(viewer, a.Operations, vault.Grant{})
		var count int
		if err := a.store(ctx).QueryRow(ctx, "SELECT count(*) FROM ops.task_split_request WHERE case_id=$1", cid).Scan(&count); err != nil || count != 0 {
			t.Fatal("private split request escaped case access", count, err)
		}
	}
	deniedSQL(t, a.DB, "SELECT ops.task_restored($1)", oid)
	ctx := scopedContext(coordinator, a.Operations, vault.Grant{})
	_, err := a.store(ctx).Exec(ctx, "DELETE FROM ops.task_split_request WHERE id=$1", s.ID)
	var p *pgconn.PgError
	if !errors.As(err, &p) || p.Code != "42501" {
		t.Fatal("runtime can delete split history", err)
	}
	cancelled := uuid.New()
	_, err = integrationAdmin.Exec(context.Background(), "INSERT INTO ops.obligation(id,case_id,agency_id,obligation_type,state) VALUES($1,$2,$3,'RESTORATION','CANCELLED')", cancelled, cid, cityTaskAgency)
	if err != nil {
		t.Fatal(err)
	}
	var restored bool
	if err = integrationAdmin.QueryRow(context.Background(), "SELECT ops.task_restored($1)", cancelled).Scan(&restored); err != nil || restored {
		t.Fatal("ordinary cancellation counted as restored", err)
	}
}

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

func proposeSequencedTask(t *testing.T, coordinator client, cid, agency uuid.UUID, scope string, prerequisites ...uuid.UUID) uuid.UUID {
	t.Helper()
	d := taskDetail(t, coordinator, cid)
	w := coordinator.request("POST", "authority/cases/"+cid.String()+"/obligations", taskProposal{ClientTaskID: uuid.New(), AgencyID: agency, Scope: scope, PrerequisiteTaskIDs: prerequisites}, d.Version, "")
	mustStatus(t, w, 201)
	return parsed[proposalResult](t, w).ID
}

func inspectSequencedTask(t *testing.T, verifier client, cid, oid uuid.UUID, result string) {
	t.Helper()
	d := taskDetail(t, verifier, cid)
	w := verifier.request("POST", "authority/cases/"+cid.String()+"/verification-decisions", map[string]any{"obligationId": oid, "result": result, "reason": "Independent fictional inspection of this prerequisite work"}, d.Version, "")
	mustStatus(t, w, 200)
}

func TestTaskPrerequisitesGateCrossAgencyWorkUntilIndependentVerification(t *testing.T) {
	a := testApp(t)
	owner, coordinator, city, water, verifier := login(t, a, 0), login(t, a, 2), login(t, a, 3), login(t, a, 1), login(t, a, 4)
	taskTestGrant(t, DemoPrincipals[1], "AGENCY_AGENT")
	taskTestGrant(t, DemoPrincipals[4], "VERIFIER")
	rid, cid, received := publicationCaseFixture(t, owner, coordinator, "SANITIZED_RECEIPT")
	path := "authority/cases/" + cid.String()
	first := taskDetail(t, coordinator, cid).Obligations[0].ID
	published := coordinator.request("POST", path+"/publications", publicationPreview(0, "Fictional sequenced restoration"), 1, "")
	mustStatus(t, published, 200)
	receipt := parsed[publicationCommandResult](t, published).ReceiptID
	second := proposeSequencedTask(t, coordinator, cid, waterTaskAgency, "PRIVATE repair drainage after the prerequisite inspection", first)
	third := proposeSequencedTask(t, coordinator, cid, cityTaskAgency, "PRIVATE resurface after both prerequisite inspections", first, second)
	performTask(t, water, second, 1, "accept")
	performTask(t, city, third, 1, "accept")
	blocked := func(actor client, oid uuid.UUID) {
		t.Helper()
		before := taskDetail(t, coordinator, cid)
		w := actor.request("POST", "authority/obligations/"+oid.String()+"/start", map[string]any{"summary": "Attempt to start before verification"}, 2, "")
		mustStatus(t, w, 409)
		if parsed[Problem](t, w).Code != "TASK_PREREQUISITES_PENDING" {
			t.Fatal("wrong prerequisite failure", w.Body.String())
		}
		after := taskDetail(t, coordinator, cid)
		if after.Version != before.Version || after.FirstReportedAt != received {
			t.Fatal("blocked work changed case/age", after)
		}
	}
	blocked(water, second)
	blocked(city, third)
	mustStatus(t, city.request("POST", "authority/obligations/"+second.String()+"/start", map[string]any{"summary": "Unauthorized cross-agency start"}, 2, ""), 404)
	performTask(t, city, first, 1, "accept", "start", "completion-claims")
	blocked(water, second)
	inspectSequencedTask(t, verifier, cid, first, "INSUFFICIENT")
	blocked(water, second)
	inspectSequencedTask(t, verifier, cid, first, "NOT_RESTORED")
	blocked(water, second)
	d := taskDetail(t, coordinator, cid)
	performTask(t, city, first, d.Obligations[0].Version, "completion-claims")
	inspectSequencedTask(t, verifier, cid, first, "VERIFIED")
	d = taskDetail(t, water, cid)
	if len(d.Obligations[1].BlockedByTaskIDs) != 0 || !slices.Equal(d.Obligations[1].PrerequisiteTaskIDs, []uuid.UUID{first}) || !slices.Equal(d.Obligations[2].BlockedByTaskIDs, []uuid.UUID{second}) {
		t.Fatal("prerequisite readiness lost fan-in", d)
	}
	blocked(city, third)
	// Parallel early starts cannot create work/history while the last prerequisite is pending.
	responses := make(chan *httptest.ResponseRecorder, 5)
	before := taskDetail(t, coordinator, cid)
	for i := 0; i < 5; i++ {
		go func() {
			responses <- city.request("POST", "authority/obligations/"+third.String()+"/start", map[string]any{"summary": "Concurrent blocked start"}, 2, "")
		}()
	}
	for i := 0; i < 5; i++ {
		mustStatus(t, <-responses, 409)
	}
	if taskDetail(t, coordinator, cid).Version != before.Version {
		t.Fatal("blocked start race wrote state")
	}
	performTask(t, water, second, 2, "start", "completion-claims")
	blocked(city, third)
	inspectSequencedTask(t, verifier, cid, second, "VERIFIED")
	d = taskDetail(t, city, cid)
	if len(d.Obligations[2].BlockedByTaskIDs) != 0 || d.State == "RESOLVED" {
		t.Fatal("fan-in readiness or required remainder failed", d)
	}
	mustStatus(t, city.request("POST", "authority/obligations/"+third.String()+"/start", map[string]any{"summary": "Stale task version start"}, 1, ""), 412)
	performTask(t, city, third, 2, "start", "completion-claims")
	inspectSequencedTask(t, verifier, cid, third, "VERIFIED")
	d = taskDetail(t, coordinator, cid)
	if d.State != "RESOLVED" || d.FirstReportedAt != received {
		t.Fatal("verified sequence did not resolve while preserving age", d)
	}
	own := owner.request("GET", "my-reports/"+rid.String(), nil, 0, "")
	mustStatus(t, own, 200)
	if parsed[struct{ State string }](t, own).State != "VERIFIED" {
		t.Fatal("owner progress did not resolve")
	}
	pub := owner.request("GET", "case-receipts/"+receipt.String(), nil, 0, "")
	mustStatus(t, pub, 200)
	if parsed[struct{ State string }](t, pub).State != "OPEN" {
		t.Fatal("private sequencing auto-published")
	}
	w := coordinator.request("POST", path+"/publications", publicationPreview(1, "Fictional sequenced work verified"), d.Version, "")
	mustStatus(t, w, 200)
	pub = owner.request("GET", "case-receipts/"+receipt.String(), nil, 0, "")
	mustStatus(t, pub, 200)
	for _, response := range []*httptest.ResponseRecorder{own, pub} {
		for _, private := range []string{"prerequisiteTaskIds", "blockedByTaskIds", first.String(), second.String(), third.String(), "PRIVATE repair drainage", "PRIVATE resurface"} {
			if strings.Contains(response.Body.String(), private) {
				t.Fatal("sequencing leaked outside staff", private, response.Body.String())
			}
		}
	}
}

func TestTaskPrerequisiteProposalsNormalizeRetriesAndRejectInvalidLinks(t *testing.T) {
	a := testApp(t)
	owner, coordinator, agent := login(t, a, 0), login(t, a, 2), login(t, a, 3)
	_, cid, _ := publicationCaseFixture(t, owner, coordinator, "PRIVATE")
	first := taskDetail(t, coordinator, cid).Obligations[0].ID
	second := proposeSequencedTask(t, coordinator, cid, cityTaskAgency, "PRIVATE separate independent restoration")
	b := taskProposal{ClientTaskID: uuid.New(), AgencyID: cityTaskAgency, Scope: "PRIVATE dependent final restoration", PrerequisiteTaskIDs: []uuid.UUID{first, second}}
	path := "authority/cases/" + cid.String() + "/obligations"
	w := coordinator.request("POST", path, b, 2, "")
	mustStatus(t, w, 201)
	dependent := parsed[proposalResult](t, w).ID
	slices.Reverse(b.PrerequisiteTaskIDs)
	responses := make(chan *httptest.ResponseRecorder, 5)
	for i := 0; i < 5; i++ {
		go func() { responses <- coordinator.request("POST", path, b, 2, "") }()
	}
	for i := 0; i < 5; i++ {
		retry := <-responses
		mustStatus(t, retry, 201)
		if parsed[proposalResult](t, retry).ID != dependent {
			t.Fatal("retry duplicated sequenced task")
		}
	}
	var links, events, outbox int
	err := integrationAdmin.QueryRow(context.Background(), "SELECT (SELECT count(*) FROM ops.task_prerequisite WHERE task_id=$1),(SELECT count(*) FROM ops.case_event WHERE case_id=$2 AND event_type='TASK_PROPOSED'),(SELECT count(*) FROM infra.outbox WHERE aggregate_id=$2 AND event_type='ObligationChanged')", dependent, cid).Scan(&links, &events, &outbox)
	if err != nil || links != 2 || events != 2 || outbox != 2 || taskDetail(t, coordinator, cid).Version != 3 {
		t.Fatal("proposal replay changed graph/history", links, events, outbox, err)
	}
	b.PrerequisiteTaskIDs = []uuid.UUID{first}
	w = coordinator.request("POST", path, b, 3, "")
	mustStatus(t, w, 409)
	if parsed[Problem](t, w).Code != "TASK_PROPOSAL_CONFLICT" {
		t.Fatal("changed prerequisite set reused identity")
	}
	_, foreignCase, _ := publicationCaseFixture(t, owner, coordinator, "PRIVATE")
	foreign := taskDetail(t, coordinator, foreignCase).Obligations[0].ID
	invalidSets := [][]uuid.UUID{{uuid.Nil}, {first, first}, {uuid.New()}, {foreign}, {first, second, dependent, uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()}}
	// Optional, cancelled and non-restoration scaffold work cannot release required work.
	for _, kind := range []string{"OPTIONAL", "CANCELLED", "OTHER_TYPE"} {
		oid := uuid.New()
		_, err := integrationAdmin.Exec(context.Background(), "INSERT INTO ops.obligation(id,case_id,agency_id,obligation_type,state,required_for_restoration) VALUES($1,$2,$3,$4,$5,$6)", oid, cid, cityTaskAgency, map[string]string{"OTHER_TYPE": "INFORMATION", "OPTIONAL": "RESTORATION", "CANCELLED": "RESTORATION"}[kind], map[string]string{"OTHER_TYPE": "PROPOSED", "OPTIONAL": "PROPOSED", "CANCELLED": "CANCELLED"}[kind], kind != "OPTIONAL")
		if err != nil {
			t.Fatal(err)
		}
		invalidSets = append(invalidSets, []uuid.UUID{oid})
	}
	for _, ids := range invalidSets {
		input := taskProposal{ClientTaskID: uuid.New(), AgencyID: cityTaskAgency, Scope: "PRIVATE invalid prerequisite proposal " + uuid.NewString(), PrerequisiteTaskIDs: ids}
		bad := coordinator.request("POST", path, input, 3, "")
		mustStatus(t, bad, 422)
		if strings.Contains(bad.Body.String(), foreign.String()) {
			t.Fatal("invalid link exposed foreign task")
		}
	}
	if taskDetail(t, coordinator, cid).Version != 3 {
		t.Fatal("invalid links wrote case history")
	}
	// Verification changes readiness but not the immutable set or lifetime retry receipt.
	performTask(t, agent, first, 1, "accept", "start", "completion-claims")
	verifier := login(t, a, 4)
	inspectSequencedTask(t, verifier, cid, first, "VERIFIED")
	b.PrerequisiteTaskIDs = []uuid.UUID{second, first}
	w = coordinator.request("POST", path, b, 2, "")
	mustStatus(t, w, 201)
	if parsed[proposalResult](t, w).ID != dependent {
		t.Fatal("live prerequisite progress broke retry")
	}
}

func TestTaskPrerequisiteDatabaseGuardsAndStaffIsolation(t *testing.T) {
	a := testApp(t)
	owner, coordinator, agent, other := login(t, a, 0), login(t, a, 2), login(t, a, 3), login(t, a, 1)
	taskTestGrant(t, DemoPrincipals[1], "AGENCY_AGENT")
	_, cid, _ := publicationCaseFixture(t, owner, coordinator, "PRIVATE")
	first := taskDetail(t, coordinator, cid).Obligations[0].ID
	second := proposeSequencedTask(t, coordinator, cid, cityTaskAgency, "PRIVATE constrained prerequisite task", first)
	checkConstraint := func(sql string, args ...any) {
		t.Helper()
		_, err := integrationAdmin.Exec(context.Background(), sql, args...)
		var p *pgconn.PgError
		if !errors.As(err, &p) || p.Code != "23514" {
			t.Fatal("missing database prerequisite constraint", err)
		}
	}
	checkConstraint("INSERT INTO ops.task_prerequisite(case_id,task_id,prerequisite_task_id) VALUES($1,$2,$3)", cid, first, second)
	checkConstraint("INSERT INTO ops.task_prerequisite(case_id,task_id,prerequisite_task_id) VALUES($1,$2,$2)", cid, first)
	checkConstraint("UPDATE ops.task_prerequisite SET prerequisite_task_id=task_id WHERE task_id=$1", second)
	checkConstraint("DELETE FROM ops.task_prerequisite WHERE task_id=$1", second)
	performTask(t, agent, second, 1, "accept")
	for _, state := range []string{"IN_PROGRESS", "COMPLETION_CLAIMED", "VERIFIED"} {
		checkConstraint("UPDATE ops.obligation SET state=$2 WHERE id=$1", second, state)
	}
	var count int
	for _, viewer := range []client{owner, other} {
		ctx := scopedContext(viewer, a.Operations, vault.Grant{})
		if err := a.store(ctx).QueryRow(ctx, "SELECT count(*) FROM ops.task_prerequisite WHERE case_id=$1", cid).Scan(&count); err != nil || count != 0 {
			t.Fatal("private graph escaped case access", count, err)
		}
		deniedSQL(t, a.DB, "SELECT * FROM ops.task_prerequisite")
	}
	ctx := scopedContext(agent, a.Operations, vault.Grant{})
	if err := a.store(ctx).QueryRow(ctx, "SELECT count(*) FROM ops.task_prerequisite WHERE case_id=$1", cid).Scan(&count); err != nil || count != 1 {
		t.Fatal("assigned agency lost required sequencing context", count, err)
	}
	if _, err := a.store(ctx).Exec(ctx, "INSERT INTO ops.task_prerequisite(case_id,task_id,prerequisite_task_id) VALUES($1,$2,$3)", cid, first, second); err == nil {
		t.Fatal("agency inserted prerequisite without coordinator role")
	}
	for _, sql := range []string{"UPDATE ops.task_prerequisite SET case_id=case_id", "DELETE FROM ops.task_prerequisite"} {
		coordinatorContext := scopedContext(coordinator, a.Operations, vault.Grant{})
		_, err := a.store(coordinatorContext).Exec(coordinatorContext, sql)
		var p *pgconn.PgError
		if !errors.As(err, &p) || p.Code != "42501" {
			t.Fatal("runtime role can rewrite sequencing", sql, err)
		}
	}
}

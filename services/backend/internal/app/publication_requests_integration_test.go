package app

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/lavkushry/JanSetu-AI/services/backend/internal/vault"
)

type sharingRequest struct {
	ID                 uuid.UUID
	State              string
	Version            int64
	PublicationVersion int64
	Outcome            *struct{ Result, Reason string }
}

func sharingInput(revision int64) map[string]any {
	return map[string]any{"clientRequestId": uuid.New(), "publicationVersion": revision, "reasonCode": "PRIVACY", "confirmed": true}
}
func sharingDecision(result string, caseVersion, publicationVersion int64) map[string]any {
	return map[string]any{"result": result, "caseVersion": caseVersion, "publicationVersion": publicationVersion, "internalReason": "PRIVATE publisher investigation notes", "residentReason": "Your public sharing request has been reviewed.", "reviewed": true}
}
func sharingFixture(t *testing.T, owner, publisher client) (uuid.UUID, uuid.UUID, uuid.UUID, string) {
	t.Helper()
	report, cid, received := publicationCaseFixture(t, owner, publisher, "SANITIZED_RECEIPT")
	w := publisher.request("POST", "authority/cases/"+cid.String()+"/publications", publicationPreview(0, "Synthetic sharing review "+uuid.NewString()), 1, "")
	mustStatus(t, w, 200)
	return report, cid, parsed[publicationCommandResult](t, w).ReceiptID, received
}

func TestResidentWithdrawalApprovalOwnershipRetriesAndPublicIsolation(t *testing.T) {
	a := testApp(t)
	owner, other, publisher, officer := login(t, a, 0), login(t, a, 1), login(t, a, 2), login(t, a, 3)
	unpublished := privateFixture(t, owner)
	unpublishedPath := "my-reports/" + unpublished.String()
	unpublishedResponse := owner.request("GET", unpublishedPath+"/public-sharing", nil, 0, "")
	mustStatus(t, unpublishedResponse, 200)
	if got := parsed[struct {
		Publication any
		Requests    []sharingRequest
	}](t, unpublishedResponse); got.Publication != nil || len(got.Requests) != 0 {
		t.Fatal("unpublished report exposed a preview", got)
	}
	mustStatus(t, owner.request("POST", unpublishedPath+"/publication-withdrawal-requests", sharingInput(1), 0, uuid.NewString()), 409)
	report, cid, rid, received := sharingFixture(t, owner, publisher)
	own := "my-reports/" + report.String()
	casePath := "authority/cases/" + cid.String()
	create := own + "/publication-withdrawal-requests"
	input, key := sharingInput(1), uuid.NewString()
	for _, c := range []client{other, publisher, officer} {
		mustStatus(t, c.request("GET", own+"/public-sharing", nil, 0, ""), 404)
		mustStatus(t, c.request("POST", create, input, 0, key), 404)
	}
	mustStatus(t, (client{app: a}).request("GET", own+"/public-sharing", nil, 0, ""), 401)
	mustStatus(t, owner.request("POST", create, input, 0, ""), 422)
	input["reasonCode"] = "PRIVATE arbitrary text"
	mustStatus(t, owner.request("POST", create, input, 0, key), 422)
	input["reasonCode"] = "PRIVACY"
	input["statement"] = "extra identifying details"
	mustStatus(t, owner.request("POST", create, input, 0, key), 400)
	delete(input, "statement")
	input["confirmed"] = false
	mustStatus(t, owner.request("POST", create, input, 0, key), 422)
	input["confirmed"] = true
	input["publicationVersion"] = 2
	mustStatus(t, owner.request("POST", create, input, 0, key), 409)
	input["publicationVersion"] = 1
	w := owner.request("POST", create, input, 0, key)
	mustStatus(t, w, 201)
	req := parsed[sharingRequest](t, w)
	if req.State != "REQUESTED" || req.Version != 1 || req.PublicationVersion != 1 {
		t.Fatal("bad owner request", req)
	}
	for _, retryKey := range []string{key, uuid.NewString()} {
		w = owner.request("POST", create, input, 0, retryKey)
		mustStatus(t, w, 201)
		if parsed[sharingRequest](t, w).ID != req.ID {
			t.Fatal("lifetime retry duplicated request")
		}
	}
	duplicate := sharingInput(1)
	w = owner.request("POST", create, duplicate, 0, uuid.NewString())
	mustStatus(t, w, 201)
	if parsed[sharingRequest](t, w).ID != req.ID {
		t.Fatal("same snapshot duplicated request")
	}
	input["reasonCode"] = "LOCATION"
	mustStatus(t, owner.request("POST", create, input, 0, key), 409)
	mustStatus(t, owner.request("POST", create, input, 0, uuid.NewString()), 409)
	input["reasonCode"] = "PRIVACY"
	mustStatus(t, publisher.request("POST", casePath+"/publications", publicationPreview(1, "Blocked correction"), 1, ""), 403)
	mustStatus(t, other.request("GET", "case-receipts/"+rid.String(), nil, 0, ""), 200)
	w = publisher.request("GET", casePath, nil, 0, "")
	mustStatus(t, w, 200)
	if !parsed[struct{ PublicationBlocked bool }](t, w).PublicationBlocked {
		t.Fatal("case controls did not reflect pending sharing veto")
	}
	reviewPath := "authority/publication-withdrawal-requests/" + req.ID.String()
	w = publisher.request("GET", reviewPath, nil, 0, "")
	mustStatus(t, w, 200)
	for _, secret := range []string{report.String(), DemoPrincipals[0].String(), DemoPrincipals[2].String(), input["clientRequestId"].(uuid.UUID).String(), "PRIVATE publication lifecycle original", "reportId", "reviewerRef"} {
		if strings.Contains(w.Body.String(), secret) {
			t.Fatal("publisher review leaked provenance", secret)
		}
	}
	for _, c := range []client{owner, other, officer} {
		mustStatus(t, c.request("GET", reviewPath, nil, 0, ""), 403)
		mustStatus(t, c.request("POST", reviewPath+"/decisions", sharingDecision("APPROVED", 1, 1), 1, ""), 403)
	}
	mustStatus(t, publisher.request("POST", reviewPath+"/decisions", sharingDecision("APPROVED", 1, 1), 0, ""), 428)
	mustStatus(t, publisher.request("POST", reviewPath+"/decisions", sharingDecision("APPROVED", 1, 99), 1, ""), 409)
	// Work continues independently and invalidates an old case review.
	w = officer.request("GET", casePath, nil, 0, "")
	mustStatus(t, w, 200)
	detail := parsed[struct {
		Obligations []struct {
			ID      uuid.UUID
			Version int64
		}
	}](t, w)
	obligation := detail.Obligations[0]
	mustStatus(t, officer.request("POST", "authority/obligations/"+obligation.ID.String()+"/accept", map[string]any{"summary": "Private agency acceptance during sharing review"}, obligation.Version, ""), 200)
	mustStatus(t, publisher.request("POST", reviewPath+"/decisions", sharingDecision("APPROVED", 1, 1), 1, ""), 412)
	w = publisher.request("POST", reviewPath+"/decisions", sharingDecision("APPROVED", 2, 1), 1, "")
	mustStatus(t, w, 200)
	if got := parsed[sharingRequest](t, w); got.State != "APPROVED" || got.Version != 2 {
		t.Fatal("approval not recorded", got)
	}
	mustStatus(t, publisher.request("POST", reviewPath+"/decisions", sharingDecision("APPROVED", 2, 1), 1, ""), 412)
	mustStatus(t, publisher.request("POST", casePath+"/publications", publicationPreview(2, "Forbidden republication"), 2, ""), 403)
	for _, c := range []client{{app: a}, owner, other, publisher, officer} {
		mustStatus(t, c.request("GET", "case-receipts/"+rid.String(), nil, 0, ""), 404)
	}
	drainActivity(t, a)
	if len(activityFor(t, other, rid).Items) != 0 {
		t.Fatal("withdrawal retained public Activity")
	}
	for _, path := range []string{"feed?mode=UNRESOLVED", "search?q=Synthetic%20sharing%20review"} {
		w = other.request("GET", path, nil, 0, "")
		mustStatus(t, w, 200)
		if strings.Contains(w.Body.String(), rid.String()) {
			t.Fatal("withdrawn receipt discoverable", path)
		}
	}
	w = owner.request("GET", own+"/public-sharing", nil, 0, "")
	mustStatus(t, w, 200)
	sharing := parsed[struct {
		Blocked     bool
		Publication any
		Requests    []sharingRequest
	}](t, w)
	if !sharing.Blocked || sharing.Publication != nil || len(sharing.Requests) != 1 || sharing.Requests[0].Outcome == nil || sharing.Requests[0].Outcome.Reason != "Your public sharing request has been reviewed." {
		t.Fatal("owner did not receive safe current outcome", sharing)
	}
	for _, secret := range []string{"PRIVATE", cid.String(), "internalReason", "reviewerRef", "clientRequestId"} {
		if strings.Contains(w.Body.String(), secret) {
			t.Fatal("owner outcome leaked private metadata", secret)
		}
	}
	// Stored command replay must hydrate the final outcome rather than old REQUESTED.
	w = owner.request("POST", create, input, 0, key)
	mustStatus(t, w, 201)
	if got := parsed[sharingRequest](t, w); got.State != "APPROVED" || got.Version != 2 || got.ID != req.ID {
		t.Fatal("retry returned stale command response", got)
	}
	if _, err := integrationAdmin.Exec(context.Background(), "DELETE FROM infra.idempotency_record WHERE principal_ref=$1 AND operation='RequestPublicationWithdrawal'", DemoPrincipals[0]); err != nil {
		t.Fatal(err)
	}
	w = owner.request("POST", create, input, 0, key)
	mustStatus(t, w, 201)
	if parsed[sharingRequest](t, w).State != "APPROVED" {
		t.Fatal("expired command key duplicated or lost outcome")
	}
	w = owner.request("GET", own, nil, 0, "")
	mustStatus(t, w, 200)
	if !parsed[struct{ HasPublicationRequest bool }](t, w).HasPublicationRequest || strings.Contains(w.Body.String(), rid.String()) {
		t.Fatal("private progress lost sharing history or retained a dead public link")
	}
	w = publisher.request("GET", casePath, nil, 0, "")
	mustStatus(t, w, 200)
	final := parsed[struct {
		Version         int64
		FirstReportedAt string
		Publication     struct {
			Version int64
			State   string
		}
	}](t, w)
	if final.Version != 2 || final.FirstReportedAt != received || final.Publication.Version != 2 || final.Publication.State != "WITHDRAWN" {
		t.Fatal("approval changed case age/work or receipt revision", final)
	}
	var decisions int
	if err := integrationAdmin.QueryRow(context.Background(), "SELECT count(*) FROM ops.publication_withdrawal_decision WHERE request_id=$1", req.ID).Scan(&decisions); err != nil || decisions != 1 {
		t.Fatal("duplicate review", decisions, err)
	}
	for _, pool := range []struct {
		name string
		sql  string
	}{{"decision update", "UPDATE ops.publication_withdrawal_decision SET resident_reason='tampered'"}, {"decision delete", "DELETE FROM ops.publication_withdrawal_decision"}, {"provenance", "SELECT report_id FROM ops.publication_withdrawal_request"}, {"canonical reasons", "SELECT * FROM ops.publication_withdrawal_decision"}} {
		t.Run(pool.name, func(t *testing.T) { deniedSQL(t, a.Publication, pool.sql) })
	}
	for _, pool := range []string{"SELECT * FROM ops.publication_withdrawal_request", "SELECT * FROM ops.publication_withdrawal_decision", "SELECT * FROM ops.publication_withdrawal_outcome", "SELECT * FROM ops.publication_withdrawal_review"} {
		deniedSQL(t, a.Worker, pool)
		deniedSQL(t, a.DB, pool)
	}
	deniedSQL(t, a.Operations, "SELECT * FROM ops.publication_withdrawal_decision")
	deniedSQL(t, a.Operations, "DELETE FROM ops.publication_withdrawal_cancel")
}

func TestResidentWithdrawalCancellationDeclineAndRace(t *testing.T) {
	a := testApp(t)
	owner, other, publisher := login(t, a, 0), login(t, a, 1), login(t, a, 2)
	report, cid, rid, _ := sharingFixture(t, owner, publisher)
	own := "my-reports/" + report.String()
	create := own + "/publication-withdrawal-requests"
	w := owner.request("POST", create, sharingInput(1), 0, uuid.NewString())
	mustStatus(t, w, 201)
	req := parsed[sharingRequest](t, w)
	cancel := create + "/" + req.ID.String() + "/cancellations"
	mustStatus(t, other.request("POST", cancel, map[string]any{"confirmed": true}, 1, ""), 404)
	mustStatus(t, owner.request("POST", cancel, map[string]any{"confirmed": false}, 1, ""), 422)
	mustStatus(t, owner.request("POST", cancel, map[string]any{"confirmed": true}, 0, ""), 428)
	w = owner.request("POST", cancel, map[string]any{"confirmed": true}, 1, "")
	mustStatus(t, w, 200)
	if got := parsed[sharingRequest](t, w); got.State != "CANCELLED" || got.Version != 2 {
		t.Fatal("cancel not recorded", got)
	}
	mustStatus(t, owner.request("POST", cancel, map[string]any{"confirmed": true}, 1, ""), 412)
	mustStatus(t, owner.request("POST", cancel, map[string]any{"confirmed": true}, 2, ""), 200)
	mustStatus(t, publisher.request("POST", "authority/publication-withdrawal-requests/"+req.ID.String()+"/decisions", sharingDecision("APPROVED", 1, 1), 2, ""), 409)
	w = owner.request("GET", own+"/public-sharing", nil, 0, "")
	mustStatus(t, w, 200)
	if parsed[struct{ Blocked bool }](t, w).Blocked {
		t.Fatal("cancel retained publication veto")
	}
	w = owner.request("POST", create, sharingInput(1), 0, uuid.NewString())
	mustStatus(t, w, 201)
	next := parsed[sharingRequest](t, w)
	if next.ID == req.ID {
		t.Fatal("cancelled snapshot prevented a new request")
	}
	review := "authority/publication-withdrawal-requests/" + next.ID.String()
	mustStatus(t, publisher.request("POST", review+"/decisions", sharingDecision("DECLINED", 1, 1), 1, ""), 200)
	mustStatus(t, owner.request("POST", create+"/"+next.ID.String()+"/cancellations", map[string]any{"confirmed": true}, 2, ""), 409)
	w = owner.request("POST", create, sharingInput(1), 0, uuid.NewString())
	mustStatus(t, w, 201)
	if got := parsed[sharingRequest](t, w); got.ID != next.ID || got.State != "DECLINED" {
		t.Fatal("declined snapshot allowed duplicate spam", got)
	}
	casePath := "authority/cases/" + cid.String()
	mustStatus(t, publisher.request("POST", casePath+"/publications", publicationPreview(1, "Fresh review following decline"), 1, ""), 200)
	w = owner.request("POST", create, sharingInput(2), 0, uuid.NewString())
	mustStatus(t, w, 201)
	racing := parsed[sharingRequest](t, w)
	var wg sync.WaitGroup
	codes := make(chan int, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		codes <- owner.request("POST", create+"/"+racing.ID.String()+"/cancellations", map[string]any{"confirmed": true}, 1, "").Code
	}()
	go func() {
		defer wg.Done()
		codes <- publisher.request("POST", "authority/publication-withdrawal-requests/"+racing.ID.String()+"/decisions", sharingDecision("APPROVED", 1, 2), 1, "").Code
	}()
	wg.Wait()
	close(codes)
	counts := map[int]int{}
	for code := range codes {
		counts[code]++
	}
	if counts[200] != 1 || counts[412] != 1 {
		t.Fatal("cancel/review race recorded two outcomes", counts)
	}
	var markers, decisions int
	err := integrationAdmin.QueryRow(context.Background(), "SELECT (SELECT count(*) FROM ops.publication_withdrawal_cancel WHERE request_id=$1),(SELECT count(*) FROM ops.publication_withdrawal_decision WHERE request_id=$1)", racing.ID).Scan(&markers, &decisions)
	if err != nil || markers+decisions != 1 {
		t.Fatal("race left conflicting canonical outcomes", markers, decisions, err)
	}
	if markers == 1 {
		mustStatus(t, other.request("GET", "case-receipts/"+rid.String(), nil, 0, ""), 200)
	} else {
		mustStatus(t, other.request("GET", "case-receipts/"+rid.String(), nil, 0, ""), 404)
	}
}

func TestResidentWithdrawalDatabaseScopeAndRevocation(t *testing.T) {
	a := testApp(t)
	owner, other, publisher := login(t, a, 0), login(t, a, 1), login(t, a, 2)
	report, cid, rid, _ := sharingFixture(t, owner, publisher)
	grant, err := a.Vault.Aliases(context.Background(), owner.cookie.Value, uuid.NewString(), uuid.Nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := scopedContext(owner, a.Operations, grant)
	// A valid owner claim cannot fabricate another case, receipt, or preview version.
	for _, forged := range []struct {
		caseID, receipt uuid.UUID
		version         int64
	}{{uuid.New(), rid, 1}, {cid, uuid.New(), 1}, {cid, rid, 99}} {
		_, err = a.store(ctx).Exec(ctx, "INSERT INTO ops.publication_withdrawal_request(id,report_id,case_id,receipt_id,client_request_id,publication_version,reason_code) VALUES($1,$2,$3,$4,$5,$6,'PRIVACY')", uuid.New(), report, forged.caseID, forged.receipt, uuid.New(), forged.version)
		var denial *pgconn.PgError
		if !errors.As(err, &denial) || denial.Code != "42501" {
			t.Fatal("forged request bypassed insertion scope", err)
		}
	}
	w := owner.request("POST", "my-reports/"+report.String()+"/publication-withdrawal-requests", sharingInput(1), 0, uuid.NewString())
	mustStatus(t, w, 201)
	req := parsed[sharingRequest](t, w)
	for _, scope := range []context.Context{scopedContext(other, a.Operations, grant), scopedContext(owner, a.Operations, vault.Grant{})} {
		var count int
		if err = a.store(scope).QueryRow(scope, "SELECT count(*) FROM ops.publication_withdrawal_request WHERE id=$1", req.ID).Scan(&count); err != nil || count != 0 {
			t.Fatal("unverified or transferred owner scope read request", count, err)
		}
	}
	_, err = a.store(ctx).Exec(ctx, "UPDATE ops.publication_withdrawal_request SET state='CANCELLED',version=version+1 WHERE id=$1", req.ID)
	var denial *pgconn.PgError
	if !errors.As(err, &denial) || denial.Code != "23514" {
		t.Fatal("canonical cancellation marker not required", err)
	}
	pubctx := scopedContext(publisher, a.Publication, vault.Grant{})
	// Even separately issued scoped SQL cannot append both outcome kinds.
	if _, err = a.store(ctx).Exec(ctx, "INSERT INTO ops.publication_withdrawal_cancel(request_id) VALUES($1)", req.ID); err != nil {
		t.Fatal(err)
	}
	_, err = a.store(pubctx).Exec(pubctx, "INSERT INTO ops.publication_withdrawal_decision(id,request_id,result,case_version,publication_version,internal_reason,resident_reason,reviewer_ref) VALUES($1,$2,'APPROVED',1,1,'Private review reason','Shared review reason',$3)", uuid.New(), req.ID, DemoPrincipals[2])
	if !errors.As(err, &denial) || denial.Code != "23514" {
		t.Fatal("cancellation and decision could both commit", err)
	}
	if _, err = a.store(ctx).Exec(ctx, "UPDATE ops.publication_withdrawal_request SET state='CANCELLED',version=version+1 WHERE id=$1", req.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = integrationAdmin.Exec(context.Background(), "UPDATE identity.platform_grant SET revoked_at=now() WHERE principal_id=$1 AND role='PUBLISHER'", DemoPrincipals[2]); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, err := integrationAdmin.Exec(context.Background(), "UPDATE identity.platform_grant SET revoked_at=NULL WHERE principal_id=$1 AND role='PUBLISHER'", DemoPrincipals[2])
		if err != nil {
			t.Error(err)
		}
	})
	for _, view := range []string{"publication_withdrawal_review", "publication_sharing_eligibility"} {
		var count int
		if err = a.store(pubctx).QueryRow(pubctx, "SELECT count(*) FROM ops."+view+" WHERE case_id=$1", cid).Scan(&count); err != nil || count != 0 {
			t.Fatal("revoked publisher retained private projection", view, count, err)
		}
	}
	mustStatus(t, publisher.request("GET", "authority/publication-withdrawal-requests", nil, 0, ""), 403)
	mustStatus(t, publisher.request("POST", "authority/publication-withdrawal-requests/"+req.ID.String()+"/decisions", sharingDecision("APPROVED", 1, 1), 1, ""), 403)
	if _, err = integrationAdmin.Exec(context.Background(), "UPDATE identity.platform_grant SET revoked_at=NULL WHERE principal_id=$1 AND role='PUBLISHER'", DemoPrincipals[2]); err != nil {
		t.Fatal(err)
	}
	if _, err = integrationAdmin.Exec(context.Background(), "UPDATE identity.session SET revoked_at=now() WHERE token_hash=$1", tokenHash(publisher.cookie.Value)); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = a.store(pubctx).QueryRow(pubctx, "SELECT count(*) FROM ops.publication_withdrawal_review WHERE id=$1", req.ID).Scan(&count); err != nil || count != 0 {
		t.Fatal("revoked session retained review", count, err)
	}
	mustStatus(t, publisher.request("GET", "authority/publication-withdrawal-requests", nil, 0, ""), 401)
}

func TestResidentWithdrawalPublisherOnlyApprovalAfterDirectWithdrawal(t *testing.T) {
	a := testApp(t)
	owner, publisher := login(t, a, 0), login(t, a, 2)
	report, cid, _, _ := sharingFixture(t, owner, publisher)
	w := owner.request("POST", "my-reports/"+report.String()+"/publication-withdrawal-requests", sharingInput(1), 0, uuid.NewString())
	mustStatus(t, w, 201)
	req := parsed[sharingRequest](t, w)
	// PUBLISHER must suffice without a coordinator's report access.
	if _, err := integrationAdmin.Exec(context.Background(), "UPDATE identity.platform_grant SET revoked_at=now() WHERE principal_id=$1 AND role<>'PUBLISHER'", DemoPrincipals[2]); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := integrationAdmin.Exec(context.Background(), "UPDATE identity.platform_grant SET revoked_at=NULL WHERE principal_id=$1 AND role<>'PUBLISHER'", DemoPrincipals[2]); err != nil {
			t.Error(err)
		}
	})
	path := "authority/cases/" + cid.String()
	mustStatus(t, publisher.request("GET", path, nil, 0, ""), 200)
	mustStatus(t, publisher.request("GET", "authority/publication-withdrawal-requests", nil, 0, ""), 200)
	mustStatus(t, publisher.request("POST", path+"/publication-withdrawals", map[string]any{"publicationVersion": 1, "reason": "Independent direct withdrawal review", "reviewed": true}, 1, ""), 200)
	review := "authority/publication-withdrawal-requests/" + req.ID.String()
	mustStatus(t, publisher.request("POST", review+"/decisions", sharingDecision("APPROVED", 1, 1), 1, ""), 409)
	mustStatus(t, publisher.request("POST", review+"/decisions", sharingDecision("APPROVED", 1, 2), 1, ""), 200)
	var withdrawals, events int
	err := integrationAdmin.QueryRow(context.Background(), "SELECT (SELECT count(*) FROM ops.publication_decision WHERE case_id=$1 AND action='WITHDRAW'),(SELECT count(*) FROM infra.outbox WHERE aggregate_id=$1 AND event_type='SafeReceiptWithdrawn')", cid).Scan(&withdrawals, &events)
	if err != nil || withdrawals != 1 || events != 1 {
		t.Fatal("approval repeated an already committed withdrawal", withdrawals, events, err)
	}
	mustStatus(t, publisher.request("POST", path+"/publications", publicationPreview(2, "Blocked publisher-only republication"), 1, ""), 403)
}

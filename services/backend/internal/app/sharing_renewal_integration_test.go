package app

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/lavkushry/JanSetu-AI/services/backend/internal/vault"
)

type sharingPermission struct {
	ID                          uuid.UUID
	State                       string
	Version, PublicationVersion int64
	CreatedAt                   string
	CancelledAt                 *string
}
type renewedSharingRequest struct {
	sharingRequest
	SharingReview *struct {
		PublicationVersion int64
		CanRenew, CanUndo  bool
		Renewal            *sharingPermission
	}
}
type ownerSharingReview struct {
	Blocked           bool
	Publication       any
	Requests          []renewedSharingRequest
	PermissionRequest *renewedSharingRequest
}

func renewalInput(revision int64) map[string]any {
	return map[string]any{"clientRequestId": uuid.New(), "publicationVersion": revision, "confirmed": true}
}
func undoSharingInput(revision int64) map[string]any {
	return map[string]any{"publicationVersion": revision, "confirmed": true}
}
func approvedSharingFixture(t *testing.T, owner, publisher client) (uuid.UUID, uuid.UUID, uuid.UUID, renewedSharingRequest) {
	t.Helper()
	report, cid, rid, _ := sharingFixture(t, owner, publisher)
	w := owner.request("POST", "my-reports/"+report.String()+"/publication-withdrawal-requests", sharingInput(1), 0, uuid.NewString())
	mustStatus(t, w, 201)
	req := parsed[sharingRequest](t, w)
	mustStatus(t, publisher.request("POST", "authority/publication-withdrawal-requests/"+req.ID.String()+"/decisions", sharingDecision("APPROVED", 1, 1), 1, ""), 200)
	w = owner.request("GET", "my-reports/"+report.String()+"/public-sharing", nil, 0, "")
	mustStatus(t, w, 200)
	got := parsed[ownerSharingReview](t, w)
	if !got.Blocked || got.Publication != nil || got.PermissionRequest == nil || got.PermissionRequest.ID != req.ID || got.PermissionRequest.SharingReview == nil || !got.PermissionRequest.SharingReview.CanRenew || got.PermissionRequest.SharingReview.CanUndo || got.PermissionRequest.SharingReview.Renewal != nil {
		t.Fatal("approved withdrawal did not offer a private permission review", w.Body.String())
	}
	return report, cid, rid, *got.PermissionRequest
}

func TestSharingRenewalRetriesUndoAndFreshPublisherReview(t *testing.T) {
	a := testApp(t)
	owner, other, publisher := login(t, a, 0), login(t, a, 1), login(t, a, 2)
	report, cid, rid, req := approvedSharingFixture(t, owner, publisher)
	own := "my-reports/" + report.String()
	path := own + "/publication-withdrawal-requests/" + req.ID.String() + "/sharing-renewals"
	pub := "authority/cases/" + cid.String() + "/publications"
	input, key := renewalInput(2), uuid.NewString()
	for _, c := range []client{other, publisher} {
		mustStatus(t, c.request("POST", path, input, 2, key), 404)
	}
	mustStatus(t, owner.request("POST", path, input, 0, key), 428)
	mustStatus(t, owner.request("POST", path, input, 2, ""), 422)
	mustStatus(t, owner.request("POST", path, input, 1, key), 412)
	input["publicationVersion"] = 1
	mustStatus(t, owner.request("POST", path, input, 2, key), 409)
	input["publicationVersion"], input["confirmed"] = 2, false
	mustStatus(t, owner.request("POST", path, input, 2, key), 422)
	input["confirmed"], input["statement"] = true, "PRIVATE identifying text"
	mustStatus(t, owner.request("POST", path, input, 2, key), 400)
	delete(input, "statement")
	var before, after string
	snapshot := `SELECT jsonb_build_object('report',to_jsonb(r),'case',to_jsonb(c),'request',to_jsonb(w),'decision',to_jsonb(d),'receipt',to_jsonb(p),'events',(SELECT count(*) FROM infra.outbox WHERE aggregate_id=c.id))::text FROM ops.report r JOIN ops.intake_review i ON i.report_id=r.id JOIN ops.case_record c ON c.id=i.case_id JOIN ops.publication_withdrawal_request w ON w.report_id=r.id JOIN ops.publication_withdrawal_decision d ON d.request_id=w.id JOIN social.case_receipt p ON p.id=w.receipt_id WHERE r.id=$1`
	if err := integrationAdmin.QueryRow(context.Background(), snapshot, report).Scan(&before); err != nil {
		t.Fatal(err)
	}
	w := owner.request("POST", path, input, 2, key)
	mustStatus(t, w, 201)
	got := parsed[renewedSharingRequest](t, w)
	n := got.SharingReview.Renewal
	if got.State != "APPROVED" || got.Version != 2 || n == nil || n.State != "ACTIVE" || n.Version != 1 || n.PublicationVersion != 2 || n.CancelledAt != nil || got.SharingReview.CanRenew || !got.SharingReview.CanUndo {
		t.Fatal("wrong renewal receipt", w.Body.String())
	}
	if err := integrationAdmin.QueryRow(context.Background(), snapshot, report).Scan(&after); err != nil || before != after {
		t.Fatal("permission changed original report, work, withdrawal, public receipt or outbox", err)
	}
	for _, secret := range []string{cid.String(), rid.String(), DemoPrincipals[0].String(), input["clientRequestId"].(uuid.UUID).String(), "PRIVATE"} {
		if strings.Contains(w.Body.String(), secret) {
			t.Fatal("owner permission exposed private provenance", secret)
		}
	}
	for _, retry := range []struct {
		body map[string]any
		key  string
	}{{input, key}, {input, uuid.NewString()}} {
		w = owner.request("POST", path, retry.body, 2, retry.key)
		mustStatus(t, w, 201)
		if parsed[renewedSharingRequest](t, w).SharingReview.Renewal.ID != n.ID {
			t.Fatal("retry duplicated active permission")
		}
	}
	// A different ID is a new intent, never an acknowledged but unretained retry.
	mustStatus(t, owner.request("POST", path, renewalInput(2), 2, uuid.NewString()), 409)
	mustStatus(t, other.request("GET", "case-receipts/"+rid.String(), nil, 0, ""), 404)
	preview := publicationPreview(2, "Fresh permission review")
	preview["reviewed"] = false
	mustStatus(t, publisher.request("POST", pub, preview, 1, ""), 422)
	preview["reviewed"] = true
	mustStatus(t, publisher.request("POST", pub, preview, 99, ""), 412)
	preview["publicationVersion"] = 1
	mustStatus(t, publisher.request("POST", pub, preview, 1, ""), 409)
	undo := path + "/" + n.ID.String() + "/cancellations"
	mustStatus(t, other.request("POST", undo, undoSharingInput(2), 1, ""), 404)
	w = owner.request("POST", undo, undoSharingInput(2), 1, "")
	mustStatus(t, w, 200)
	got = parsed[renewedSharingRequest](t, w)
	if got.SharingReview.Renewal.State != "CANCELLED" || got.SharingReview.Renewal.Version != 2 || got.SharingReview.Renewal.CancelledAt == nil || !got.SharingReview.CanRenew || got.SharingReview.CanUndo {
		t.Fatal("undo did not revoke permission", w.Body.String())
	}
	mustStatus(t, owner.request("POST", undo, undoSharingInput(2), 1, ""), 412)
	mustStatus(t, owner.request("POST", undo, undoSharingInput(2), 2, ""), 200)
	mustStatus(t, publisher.request("POST", pub, publicationPreview(2, "Blocked after undo"), 1, ""), 403)
	// Lost acknowledgements replay current state even after command-key expiry.
	if _, err := integrationAdmin.Exec(context.Background(), "DELETE FROM infra.idempotency_record WHERE principal_ref=$1 AND operation='RenewPublicSharing'", DemoPrincipals[0]); err != nil {
		t.Fatal(err)
	}
	w = owner.request("POST", path, input, 2, key)
	mustStatus(t, w, 201)
	if parsed[renewedSharingRequest](t, w).SharingReview.Renewal.State != "CANCELLED" {
		t.Fatal("expired retry reactivated canceled permission")
	}
	w = owner.request("POST", path, renewalInput(2), 2, uuid.NewString())
	mustStatus(t, w, 201)
	newPermission := parsed[renewedSharingRequest](t, w).SharingReview.Renewal
	if newPermission.ID == n.ID {
		t.Fatal("fresh consent reused revoked history")
	}
	mustStatus(t, owner.request("POST", undo, undoSharingInput(2), 2, ""), 200)
	w = owner.request("POST", path, input, 2, key)
	mustStatus(t, w, 201)
	if parsed[renewedSharingRequest](t, w).SharingReview.Renewal.ID != newPermission.ID {
		t.Fatal("old replay did not hydrate current permission")
	}
	input["publicationVersion"] = 3
	mustStatus(t, owner.request("POST", path, input, 2, key), 409)
	mustStatus(t, owner.request("POST", path, input, 2, uuid.NewString()), 409)
	w = publisher.request("POST", pub, publicationPreview(2, "Newly reviewed public progress"), 1, "")
	mustStatus(t, w, 200)
	publication := parsed[publicationCommandResult](t, w)
	if publication.ReceiptID != rid || publication.PublicationVersion != 3 || publication.Version != 1 {
		t.Fatal("republication changed private work or receipt identity", publication)
	}
	mustStatus(t, other.request("GET", "case-receipts/"+rid.String(), nil, 0, ""), 200)
	mustStatus(t, owner.request("POST", path+"/"+newPermission.ID.String()+"/cancellations", undoSharingInput(2), 1, ""), 409)
	mustStatus(t, owner.request("POST", path+"/"+newPermission.ID.String()+"/cancellations", undoSharingInput(3), 1, ""), 409)
	w = owner.request("POST", own+"/publication-withdrawal-requests", sharingInput(3), 0, uuid.NewString())
	mustStatus(t, w, 201)
	next := parsed[sharingRequest](t, w)
	mustStatus(t, publisher.request("POST", "authority/publication-withdrawal-requests/"+next.ID.String()+"/decisions", sharingDecision("APPROVED", 1, 3), 1, ""), 200)
	mustStatus(t, publisher.request("POST", pub, publicationPreview(4, "Old permission cannot restore"), 1, ""), 403)
	input["publicationVersion"] = 2
	mustStatus(t, owner.request("POST", path, input, 2, key), 201)
	mustStatus(t, publisher.request("POST", pub, publicationPreview(4, "Old replay cannot restore"), 1, ""), 403)
	mustStatus(t, owner.request("POST", path, renewalInput(4), 2, uuid.NewString()), 409)
	mustStatus(t, owner.request("POST", own+"/publication-withdrawal-requests/"+next.ID.String()+"/sharing-renewals", renewalInput(4), 2, uuid.NewString()), 201)
	mustStatus(t, publisher.request("POST", pub, publicationPreview(4, "Fresh consent for second withdrawal"), 1, ""), 200)
}

func TestSharingRenewalSupersedesOlderUndoAndSurvivesHistoryLimit(t *testing.T) {
	a := testApp(t)
	owner, publisher := login(t, a, 0), login(t, a, 2)
	report, cid, _, req := approvedSharingFixture(t, owner, publisher)
	own := "my-reports/" + report.String()
	create := own + "/publication-withdrawal-requests"
	path := create + "/" + req.ID.String() + "/sharing-renewals"
	casePath := "authority/cases/" + cid.String()
	w := owner.request("POST", path, renewalInput(2), 2, uuid.NewString())
	mustStatus(t, w, 201)
	n := parsed[renewedSharingRequest](t, w).SharingReview.Renewal
	mustStatus(t, publisher.request("POST", casePath+"/publications", publicationPreview(2, "History fixture"), 1, ""), 200)
	// Recent canceled requests must never displace the actionable permission.
	for i := 0; i < 21; i++ {
		w = owner.request("POST", create, sharingInput(3), 0, uuid.NewString())
		mustStatus(t, w, 201)
		id := parsed[sharingRequest](t, w).ID
		mustStatus(t, owner.request("POST", create+"/"+id.String()+"/cancellations", map[string]any{"confirmed": true}, 1, ""), 200)
	}
	w = owner.request("GET", own+"/public-sharing", nil, 0, "")
	mustStatus(t, w, 200)
	review := parsed[ownerSharingReview](t, w)
	if len(review.Requests) != 20 || review.PermissionRequest == nil || review.PermissionRequest.ID != req.ID || review.PermissionRequest.SharingReview.Renewal.ID != n.ID {
		t.Fatal("recent history hid current permission", w.Body.String())
	}
	for _, item := range review.Requests {
		if item.ID == req.ID {
			t.Fatal("fixture did not push approval out of recent history")
		}
	}
	w = owner.request("POST", create, sharingInput(3), 0, uuid.NewString())
	mustStatus(t, w, 201)
	next := parsed[sharingRequest](t, w)
	mustStatus(t, publisher.request("POST", casePath+"/publication-withdrawals", map[string]any{"publicationVersion": 3, "reason": "Independent privacy review", "reviewed": true}, 1, ""), 200)
	mustStatus(t, owner.request("POST", path+"/"+n.ID.String()+"/cancellations", undoSharingInput(4), 1, ""), 200)
	mustStatus(t, publisher.request("POST", "authority/publication-withdrawal-requests/"+next.ID.String()+"/decisions", sharingDecision("APPROVED", 1, 4), 1, ""), 200)
	mustStatus(t, owner.request("POST", path, renewalInput(4), 2, uuid.NewString()), 409)
	mustStatus(t, owner.request("POST", create+"/"+next.ID.String()+"/sharing-renewals", renewalInput(4), 2, uuid.NewString()), 201)
	mustStatus(t, publisher.request("POST", casePath+"/publications", publicationPreview(4, "Newest consent supersedes older cancellation"), 1, ""), 200)
}

func TestSharingRenewalDatabaseIsolationAndOriginalPrivateVeto(t *testing.T) {
	a := testApp(t)
	owner, other, publisher := login(t, a, 0), login(t, a, 1), login(t, a, 2)
	report, cid, _, req := approvedSharingFixture(t, owner, publisher)
	grant, err := a.Vault.Aliases(context.Background(), owner.cookie.Value, uuid.NewString(), uuid.Nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := scopedContext(owner, a.Operations, grant)
	for _, forged := range []struct {
		request, report uuid.UUID
		revision        int64
	}{{uuid.New(), report, 2}, {req.ID, privateFixture(t, other), 2}, {req.ID, report, 1}} {
		_, err = a.store(ctx).Exec(ctx, "INSERT INTO ops.publication_sharing_renewal(id,request_id,report_id,client_request_id,publication_version) VALUES($1,$2,$3,$4,$5)", uuid.New(), forged.request, forged.report, uuid.New(), forged.revision)
		var denial *pgconn.PgError
		if !errors.As(err, &denial) || (denial.Code != "23514" && denial.Code != "42501") {
			t.Fatal("forged permission bypassed canonical target", err)
		}
	}
	path := "my-reports/" + report.String() + "/publication-withdrawal-requests/" + req.ID.String() + "/sharing-renewals"
	w := owner.request("POST", path, renewalInput(2), 2, uuid.NewString())
	mustStatus(t, w, 201)
	n := parsed[renewedSharingRequest](t, w).SharingReview.Renewal
	for _, scope := range []context.Context{scopedContext(other, a.Operations, grant), scopedContext(owner, a.Operations, vault.Grant{})} {
		for _, table := range []string{"publication_sharing_renewal", "publication_sharing_renewal_review"} {
			var count int
			if err = a.store(scope).QueryRow(scope, "SELECT count(*) FROM ops."+table+" WHERE report_id=$1", report).Scan(&count); err != nil || count != 0 {
				t.Fatal("unverified or transferred owner read permission", table, count, err)
			}
		}
	}
	for _, table := range []string{"publication_sharing_renewal", "publication_sharing_renewal_review"} {
		deniedSQL(t, a.Publication, "SELECT * FROM ops."+table)
		deniedSQL(t, a.Worker, "SELECT * FROM ops."+table)
		deniedSQL(t, a.DB, "SELECT * FROM ops."+table)
	}
	for _, sql := range []string{"UPDATE ops.publication_sharing_renewal SET created_at=now()", "UPDATE ops.publication_sharing_renewal SET request_id=request_id", "DELETE FROM ops.publication_sharing_renewal"} {
		if _, err = a.store(ctx).Exec(ctx, sql); err == nil {
			t.Fatal("mutable canonical permission provenance", sql)
		}
	}
	_, err = a.store(ctx).Exec(ctx, "UPDATE ops.publication_sharing_renewal SET state='CANCELLED' WHERE id=$1", n.ID)
	var denial *pgconn.PgError
	if !errors.As(err, &denial) || denial.Code != "23514" {
		t.Fatal("cancellation bypassed version guard", err)
	}
	// A separate PRIVATE observation continues to veto all case publication.
	private := privateFixture(t, other)
	if _, err = integrationAdmin.Exec(context.Background(), "INSERT INTO ops.case_observation(case_id,report_id,relation) VALUES($1,$2,'SUPPORTING')", cid, private); err != nil {
		t.Fatal(err)
	}
	mustStatus(t, publisher.request("POST", "authority/cases/"+cid.String()+"/publications", publicationPreview(2, "Private source must still block"), 1, ""), 403)
	if _, err = integrationAdmin.Exec(context.Background(), "DELETE FROM ops.case_observation WHERE case_id=$1 AND report_id=$2", cid, private); err != nil {
		t.Fatal(err)
	}
	mustStatus(t, publisher.request("POST", "authority/cases/"+cid.String()+"/publications", publicationPreview(2, "Owner scoped fresh review"), 1, ""), 200)
	_, err = a.store(ctx).Exec(ctx, "UPDATE ops.publication_sharing_renewal SET state='CANCELLED',version=version+1 WHERE id=$1", n.ID)
	if !errors.As(err, &denial) || denial.Code != "23514" {
		t.Fatal("direct SQL undid permission after publication", err)
	}
	if _, err = integrationAdmin.Exec(context.Background(), "UPDATE identity.session SET revoked_at=now() WHERE token_hash=$1", tokenHash(owner.cookie.Value)); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"publication_sharing_renewal", "publication_sharing_renewal_review"} {
		var count int
		if err = a.store(ctx).QueryRow(ctx, "SELECT count(*) FROM ops."+table+" WHERE report_id=$1", report).Scan(&count); err != nil || count != 0 {
			t.Fatal("revoked owner session retained permission scope", table, count, err)
		}
	}
	mustStatus(t, owner.request("POST", path, renewalInput(3), 2, uuid.NewString()), 401)
}

func TestSharingRenewalConcurrentCreationAndUndoPublicationRace(t *testing.T) {
	a := testApp(t)
	owner, publisher := login(t, a, 0), login(t, a, 2)
	report, cid, rid, req := approvedSharingFixture(t, owner, publisher)
	path := "my-reports/" + report.String() + "/publication-withdrawal-requests/" + req.ID.String() + "/sharing-renewals"
	var wg sync.WaitGroup
	ids := make(chan uuid.UUID, 4)
	input := renewalInput(2)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := owner.request("POST", path, input, 2, uuid.NewString())
			mustStatus(t, w, 201)
			ids <- parsed[renewedSharingRequest](t, w).SharingReview.Renewal.ID
		}()
	}
	wg.Wait()
	close(ids)
	var n uuid.UUID
	for id := range ids {
		if n != uuid.Nil && n != id {
			t.Fatal("concurrent owner confirmations duplicated active permission")
		}
		n = id
	}
	statuses := make(chan int, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		statuses <- owner.request("POST", path+"/"+n.String()+"/cancellations", undoSharingInput(2), 1, "").Code
	}()
	go func() {
		defer wg.Done()
		statuses <- publisher.request("POST", "authority/cases/"+cid.String()+"/publications", publicationPreview(2, "Race fresh review"), 1, "").Code
	}()
	wg.Wait()
	close(statuses)
	success := 0
	for status := range statuses {
		if status == 200 {
			success++
		} else if status != 403 && status != 409 {
			t.Fatal("unexpected race result", status)
		}
	}
	if success != 1 {
		t.Fatal("undo and publication both won", success)
	}
	var permission, publication string
	if err := integrationAdmin.QueryRow(context.Background(), "SELECT n.state,p.publication_state FROM ops.publication_sharing_renewal n JOIN social.case_receipt p ON p.id=$2 WHERE n.id=$1", n, rid).Scan(&permission, &publication); err != nil {
		t.Fatal(err)
	}
	if (permission == "CANCELLED" && publication != "WITHDRAWN") || (permission == "ACTIVE" && publication != "PUBLISHED") {
		t.Fatal("race left inconsistent sharing", permission, publication)
	}
}

func TestSharingRenewalPublisherRechecksAfterScopedUndo(t *testing.T) {
	a := testApp(t)
	owner, publisher := login(t, a, 0), login(t, a, 2)
	report, cid, rid, req := approvedSharingFixture(t, owner, publisher)
	path := "my-reports/" + report.String() + "/publication-withdrawal-requests/" + req.ID.String() + "/sharing-renewals"
	w := owner.request("POST", path, renewalInput(2), 2, uuid.NewString())
	mustStatus(t, w, 201)
	n := parsed[renewedSharingRequest](t, w).SharingReview.Renewal
	grant, err := a.Vault.Aliases(context.Background(), owner.cookie.Value, uuid.NewString(), uuid.Nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(scopedContext(owner, a.Operations, grant), 8*time.Second)
	defer cancel()
	tx, err := a.begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	var ownerPID int
	if err = tx.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&ownerPID); err != nil {
		t.Fatal(err)
	}
	// A separately issued owner-scoped SQL transition holds the receipt through
	// its trigger, without taking the API's pilot mutation lock. The publisher
	// sees committed ACTIVE permission, then waits for this receipt lock.
	if _, err = tx.Exec(ctx, "UPDATE ops.publication_sharing_renewal SET state='CANCELLED',version=version+1 WHERE id=$1", n.ID); err != nil {
		t.Fatal(err)
	}
	done := make(chan int, 1)
	go func() {
		done <- publisher.request("POST", "authority/cases/"+cid.String()+"/publications", publicationPreview(2, "Scoped undo racing with review"), 1, "").Code
	}()
	deadline := time.Now().Add(3 * time.Second)
	for {
		var blocked bool
		if err = integrationAdmin.QueryRow(ctx, "SELECT EXISTS(SELECT FROM pg_stat_activity a WHERE a.datname=current_database() AND $1=ANY(pg_blocking_pids(a.pid)) AND a.query LIKE '%FROM social.case_receipt%FOR UPDATE%')", ownerPID).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("publisher did not wait for the owner receipt lock")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case status := <-done:
		if status != 403 {
			t.Fatal("publisher used consent read before receipt lock", status)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("publisher did not finish after scoped undo committed")
	}
	mustStatus(t, owner.request("GET", "case-receipts/"+rid.String(), nil, 0, ""), 404)
}

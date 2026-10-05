package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/lavkushry/JanSetu-AI/services/backend/internal/vault"
)

type publicationCommandResult struct {
	ReceiptID          uuid.UUID
	Version            int64
	PublicationVersion int64
	State              string
}

func publicationCaseFixture(t *testing.T, owner, publisher client, preference string) (uuid.UUID, uuid.UUID, string) {
	t.Helper()
	w := owner.request("POST", "service-reports", ReportInput{ClientSubmissionID: uuid.New(), Statement: "PRIVATE publication lifecycle original " + uuid.NewString(), LocationLabel: "Synthetic crossing", Category: "FOOTPATH", LanguageTag: "en-IN", PublicationPreference: preference}, 0, uuid.NewString())
	mustStatus(t, w, 201)
	report := parsed[struct {
		ID         uuid.UUID
		ReceivedAt string
	}](t, w)
	w = publisher.request("POST", "authority/reports/"+report.ID.String()+"/triage", map[string]any{"agencyId": "30000000-0000-4000-8000-000000000001", "category": "FOOTPATH", "urgencyTier": 2, "reason": "Private synthetic restoration assessment"}, 1, "")
	mustStatus(t, w, 201)
	return report.ID, parsed[struct{ CaseID uuid.UUID }](t, w).CaseID, report.ReceivedAt
}

func publicationPreview(version int64, title string) map[string]any {
	return map[string]any{"title": title, "summary": "A reviewed fictional restoration task is awaiting agency acceptance.", "area": "Synthetic broad area", "reviewed": true, "publicationVersion": version, "reason": "PRIVATE publication review reason"}
}

func TestPublicProgressCorrectionWithdrawalAndFreshRepublication(t *testing.T) {
	a := testApp(t)
	owner, follower, publisher, officer := login(t, a, 0), login(t, a, 1), login(t, a, 2), login(t, a, 3)
	reportID, cid, received := publicationCaseFixture(t, owner, publisher, "SANITIZED_RECEIPT")
	path := "authority/cases/" + cid.String()
	preview := publicationPreview(0, "Reviewed lifecycle crossing "+uuid.NewString())
	w := publisher.request("POST", path+"/publications", preview, 1, "")
	mustStatus(t, w, 200)
	first := parsed[publicationCommandResult](t, w)
	if first.Version != 1 || first.PublicationVersion != 1 || first.State != "PUBLISHED" || first.ReceiptID == cid || first.ReceiptID == reportID {
		t.Fatal("publication changed the case or reused a private identifier", first)
	}
	rid := first.ReceiptID
	publicPath := "case-receipts/" + rid.String()
	mustStatus(t, follower.request("PUT", publicPath+"/follow", map[string]any{"following": true}, 0, ""), 200)
	for _, forged := range []struct {
		recipient uuid.UUID
		version   int64
	}{{myProfileID(t, owner), 1}, {myProfileID(t, follower), 99}} {
		_, err := a.Worker.Exec(context.Background(), "INSERT INTO social.notification(id,recipient_id,event_id,channel,receipt_id,kind,source_version,state) VALUES($1,$2,$3,'IN_APP',$4,'CASE_PROGRESS',$5,'SENT')", uuid.New(), forged.recipient, uuid.New(), rid, forged.version)
		var denial *pgconn.PgError
		if !errors.As(err, &denial) || denial.Code != "42501" {
			t.Fatal("forged service recipient/version bypassed row policy", err)
		}
	}
	drainActivity(t, a)
	if len(activityFor(t, follower, rid).Items) != 0 {
		t.Fatal("following backfilled the first publication")
	}
	// A version-1 event from a compatible older producer still projects the
	// matching legacy case revision; identical retries keep the existing notice.
	queuedProjectionEvent(t, "CASE", cid, "SafeReceiptPublished", map[string]any{"receiptId": rid})
	drainActivity(t, a)
	old := activityFor(t, follower, rid).Items
	if len(old) != 1 {
		t.Fatal("legacy publication event was not supported")
	}
	mustStatus(t, follower.request("PUT", "me/activity/"+old[0].ID.String()+"/read", map[string]any{"read": true}, 0, ""), 200)
	preview["publicationVersion"] = int64(1)
	preview["title"] = "Corrected lifecycle crossing " + uuid.NewString()
	w = publisher.request("POST", path+"/publications", preview, 1, "")
	mustStatus(t, w, 200)
	second := parsed[publicationCommandResult](t, w)
	if second.ReceiptID != rid || second.Version != 1 || second.PublicationVersion != 2 {
		t.Fatal("correction did not retain an independently versioned receipt", second)
	}
	drainActivity(t, a)
	if got := activityFor(t, follower, rid).Items; len(got) != 2 {
		t.Fatal("same-case-version correction did not generate new reviewed progress", got)
	}
	preview["publicationVersion"] = int64(2)
	w = publisher.request("POST", path+"/publications", preview, 1, "")
	mustStatus(t, w, 200)
	if parsed[publicationCommandResult](t, w) != second {
		t.Fatal("unchanged reviewed preview was not a no-op")
	}
	var decisions int
	if err := integrationAdmin.QueryRow(context.Background(), "SELECT count(*) FROM ops.publication_decision WHERE case_id=$1", cid).Scan(&decisions); err != nil || decisions != 2 {
		t.Fatal("unchanged preview created another decision", decisions, err)
	}
	preview["publicationVersion"] = int64(1)
	mustStatus(t, publisher.request("POST", path+"/publications", preview, 1, ""), 409)
	preview["publicationVersion"] = int64(2)
	preview["title"] = "Queued progress to withdraw " + uuid.NewString()
	mustStatus(t, publisher.request("POST", path+"/publications", preview, 1, ""), 200)
	mustStatus(t, owner.request("PUT", publicPath+"/follow", map[string]any{"following": true}, 0, ""), 200)
	withdrawal := map[string]any{"publicationVersion": 3, "reason": "PRIVATE withdrawal review reason", "reviewed": true}
	mustStatus(t, officer.request("POST", path+"/publication-withdrawals", withdrawal, 1, ""), 403)
	mustStatus(t, owner.request("POST", path+"/publication-withdrawals", withdrawal, 1, ""), 403)
	mustStatus(t, publisher.request("POST", path+"/publication-withdrawals", withdrawal, 2, ""), 412)
	mustStatus(t, publisher.request("POST", path+"/publication-withdrawals", withdrawal, 0, ""), 428)
	w = publisher.request("POST", path+"/publication-withdrawals", withdrawal, 1, "")
	mustStatus(t, w, 200)
	withdrawn := parsed[publicationCommandResult](t, w)
	if withdrawn.ReceiptID != rid || withdrawn.Version != 1 || withdrawn.PublicationVersion != 4 || withdrawn.State != "WITHDRAWN" {
		t.Fatal("withdrawal changed operational state or failed to advance publication", withdrawn)
	}
	for _, reader := range []client{{app: a}, owner, follower, publisher, officer} {
		mustStatus(t, reader.request("GET", publicPath, nil, 0, ""), 404)
	}
	mustStatus(t, follower.request("PUT", publicPath+"/follow", map[string]any{"following": true}, 0, ""), 404)
	mustStatus(t, owner.request("PUT", publicPath+"/follow", map[string]any{"following": false}, 0, ""), 200)
	mustStatus(t, owner.request("PUT", "case-receipts/"+uuid.NewString()+"/follow", map[string]any{"following": false}, 0, ""), 200)
	for _, n := range activityFor(t, follower, rid).Items {
		t.Fatal("withdrawn progress remained in Activity", n)
	}
	mustStatus(t, follower.request("PUT", "me/activity/"+old[0].ID.String()+"/read", map[string]any{"read": false}, 0, ""), 404)
	for _, route := range []string{"feed?mode=UNRESOLVED", "feed?mode=RESOLVED", "feed?mode=FOLLOWING", "search?q=Queued%20progress%20to%20withdraw"} {
		w = follower.request("GET", route, nil, 0, "")
		mustStatus(t, w, 200)
		if strings.Contains(w.Body.String(), rid.String()) || strings.Contains(w.Body.String(), preview["title"].(string)) {
			t.Fatal("withdrawn progress remained discoverable", route)
		}
	}
	w = owner.request("GET", "my-reports/"+reportID.String(), nil, 0, "")
	mustStatus(t, w, 200)
	if !strings.Contains(w.Body.String(), "PRIVATE publication lifecycle original") || strings.Contains(w.Body.String(), rid.String()) {
		t.Fatal("withdrawal lost private progress or retained its dead public link")
	}
	drainActivity(t, a)
	if len(activityFor(t, follower, rid).Items) != 0 {
		t.Fatal("queued publication reappeared after withdrawal")
	}
	withdrawal["publicationVersion"] = 4
	w = publisher.request("POST", path+"/publication-withdrawals", withdrawal, 1, "")
	mustStatus(t, w, 200)
	if parsed[publicationCommandResult](t, w) != withdrawn {
		t.Fatal("current repeated withdrawal advanced its revision")
	}
	preview["publicationVersion"] = int64(3)
	mustStatus(t, publisher.request("POST", path+"/publications", preview, 1, ""), 409)
	preview["publicationVersion"] = int64(4)
	preview["title"] = "Fresh republished crossing " + uuid.NewString()
	mustStatus(t, publisher.request("POST", path+"/publications", preview, 1, ""), 200)
	drainActivity(t, a)
	if got := activityFor(t, follower, rid).Items; len(got) != 1 || got[0].ReadAt != nil || got[0].ID == old[0].ID {
		t.Fatal("fresh republication resurrected pre-withdrawal notices", got)
	}
	if len(activityFor(t, owner, rid).Items) != 0 {
		t.Fatal("withdrawn-source unsubscribe did not stop fresh publication Activity")
	}
	mustStatus(t, follower.request("PUT", "me/activity/"+old[0].ID.String()+"/read", map[string]any{"read": false}, 0, ""), 404)
	w = follower.request("GET", publicPath, nil, 0, "")
	mustStatus(t, w, 200)
	if public := parsed[struct{ Version int64 }](t, w); public.Version != 5 {
		t.Fatal("public receipt exposed operational version instead of publication revision", public)
	}
	for _, secret := range []string{"PRIVATE", cid.String(), reportID.String(), DemoPrincipals[0].String(), DemoPrincipals[2].String()} {
		if strings.Contains(w.Body.String(), secret) {
			t.Fatal("publication leaked private review metadata", secret)
		}
	}
	w = publisher.request("GET", path, nil, 0, "")
	mustStatus(t, w, 200)
	detail := parsed[struct {
		Version         int64
		State           string
		FirstReportedAt string
		Publication     struct {
			Version   int64
			Decisions []struct{ Action string }
		}
	}](t, w)
	if detail.Version != 1 || detail.State != "OPEN" || detail.FirstReportedAt != received || detail.Publication.Version != 5 || len(detail.Publication.Decisions) != 5 || detail.Publication.Decisions[1].Action != "WITHDRAW" {
		t.Fatal("publication lifecycle reset case history or lost review decisions", detail)
	}
	deniedSQL(t, a.Publication, "UPDATE ops.publication_decision SET internal_reason='tampered'")
	deniedSQL(t, a.Publication, "DELETE FROM ops.publication_decision")
	deniedSQL(t, a.Operations, "SELECT * FROM ops.publication_decision")
	deniedSQL(t, a.Worker, "SELECT * FROM ops.publication_history")
}

func TestPublicationReviewsFenceConcurrentChangesAndRevokedPublishers(t *testing.T) {
	a := testApp(t)
	owner, publisher, officer := login(t, a, 0), login(t, a, 2), login(t, a, 3)
	_, cid, _ := publicationCaseFixture(t, owner, publisher, "SANITIZED_RECEIPT")
	path := "authority/cases/" + cid.String()
	mustStatus(t, publisher.request("POST", path+"/publications", publicationPreview(0, "Concurrent review "+uuid.NewString()), 1, ""), 200)
	var wg sync.WaitGroup
	codes := make(chan int, 2)
	for _, title := range []string{"First concurrent correction ", "Second concurrent correction "} {
		wg.Add(1)
		go func(title string) {
			defer wg.Done()
			codes <- publisher.request("POST", path+"/publications", publicationPreview(1, title+uuid.NewString()), 1, "").Code
		}(title)
	}
	wg.Wait()
	close(codes)
	counts := map[int]int{}
	for status := range codes {
		counts[status]++
	}
	if counts[200] != 1 || counts[409] != 1 {
		t.Fatal("concurrent stale review overwrote a newer publication", counts)
	}
	w := officer.request("GET", path, nil, 0, "")
	mustStatus(t, w, 200)
	other := parsed[struct {
		CanPublish  bool
		Publication json.RawMessage
	}](t, w)
	if other.CanPublish || string(other.Publication) != "null" || strings.Contains(w.Body.String(), "PRIVATE publication review") {
		t.Fatal("agency reader received publisher-only history")
	}
	ctx := scopedContext(publisher, a.Operations, vault.Grant{})
	if _, err := integrationAdmin.Exec(context.Background(), "UPDATE identity.platform_grant SET revoked_at=now() WHERE principal_id=$1 AND role='PUBLISHER'", DemoPrincipals[2]); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := integrationAdmin.Exec(context.Background(), "UPDATE identity.platform_grant SET revoked_at=NULL WHERE principal_id=$1 AND role='PUBLISHER'", DemoPrincipals[2]); err != nil {
			t.Error(err)
		}
	})
	for _, view := range []string{"publication_status", "publication_history"} {
		var count int
		if err := a.store(ctx).QueryRow(ctx, "SELECT count(*) FROM ops."+view+" WHERE case_id=$1", cid).Scan(&count); err != nil || count != 0 {
			t.Fatal("revoked publisher retained review projection", view, count, err)
		}
	}
	mustStatus(t, publisher.request("POST", path+"/publication-withdrawals", map[string]any{"publicationVersion": 2, "reason": "Revoked publisher review", "reviewed": true}, 1, ""), 403)
	mustStatus(t, publisher.request("POST", path+"/publications", publicationPreview(2, "Revoked correction "+uuid.NewString()), 1, ""), 403)
	w = publisher.request("GET", path, nil, 0, "")
	mustStatus(t, w, 200)
	if parsed[struct{ CanPublish bool }](t, w).CanPublish || !strings.Contains(w.Body.String(), `"publication":null`) {
		t.Fatal("live case DTO retained revoked publisher controls")
	}
	if _, err := integrationAdmin.Exec(context.Background(), "UPDATE identity.platform_grant SET revoked_at=NULL WHERE principal_id=$1 AND role='PUBLISHER'", DemoPrincipals[2]); err != nil {
		t.Fatal(err)
	}
	if _, err := integrationAdmin.Exec(context.Background(), "UPDATE identity.session SET revoked_at=now() WHERE token_hash=$1", tokenHash(publisher.cookie.Value)); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := a.store(ctx).QueryRow(ctx, "SELECT count(*) FROM ops.publication_history WHERE case_id=$1", cid).Scan(&count); err != nil || count != 0 {
		t.Fatal("revoked session retained publisher history", count, err)
	}
	mustStatus(t, publisher.request("GET", path, nil, 0, ""), 401)
}

func TestNewPublicationEnvelopesRequireExactSupportedRevisions(t *testing.T) {
	a := testApp(t)
	drainActivity(t, a)
	for _, fixture := range []struct {
		name    string
		kind    string
		version int32
		payload any
		code    string
	}{
		{"missing-publication-revision", "SafeReceiptPublished", 2, map[string]any{"receiptId": uuid.New()}, "INVALID_EVENT"},
		{"zero-publication-revision", "SafeReceiptPublished", 2, map[string]any{"receiptId": uuid.New(), "publicationVersion": 0}, "INVALID_EVENT"},
		{"future-publication-envelope", "SafeReceiptPublished", 3, map[string]any{}, "UNSUPPORTED_PAYLOAD_VERSION"},
		{"missing-withdrawal-revision", "SafeReceiptWithdrawn", 1, map[string]any{"receiptId": uuid.New()}, "INVALID_EVENT"},
		{"future-withdrawal-envelope", "SafeReceiptWithdrawn", 2, map[string]any{}, "UNSUPPORTED_PAYLOAD_VERSION"},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			id := queuedProjectionEvent(t, "CASE", uuid.New(), fixture.kind, fixture.payload)
			if _, err := integrationAdmin.Exec(context.Background(), "UPDATE infra.outbox SET payload_version=$2 WHERE id=$1", id, fixture.version); err != nil {
				t.Fatal(err)
			}
			assertProjectionFailure(t, a, id, fixture.code, 1)
		})
	}
}

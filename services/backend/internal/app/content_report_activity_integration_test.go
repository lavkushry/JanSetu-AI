package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/lavkushry/JanSetu-AI/services/backend/internal/store/dbgen"
	"github.com/lavkushry/JanSetu-AI/services/backend/internal/vault"
)

func TestContentReportActivityOutcomeOwnershipAndRetries(t *testing.T) {
	for _, fixture := range []struct{ kind, action string }{{"POST", "DISMISS"}, {"COMMENT", "REMOVE"}} {
		t.Run(fixture.kind+fixture.action, func(t *testing.T) {
			a := testApp(t)
			reporter, author, mod := login(t, a, 0), login(t, a, 1), login(t, a, 2)
			cleanContentReports(t, reporter)
			p := published(t, a, author, mod, "Private outcome source marker "+uuid.NewString())
			target := p.ID
			if fixture.kind == "COMMENT" {
				target = approvedReply(t, author, mod, p.ID, nil)
			}
			r := submitContentReport(t, reporter, fixture.kind, target, 1)
			drainActivity(t, a)
			if len(activityFor(t, reporter, r.ID).Items) != 0 {
				t.Fatal("an undecided report generated an outcome alert")
			}
			decideContentReport(t, mod, r, fixture.action)
			drainActivity(t, a)
			items := activityFor(t, reporter, r.ID).Items
			if len(items) != 1 || items[0].Kind != "CONTENT_REPORT_OUTCOME" {
				t.Fatal("missing reporter outcome", items)
			}
			for _, c := range []client{author, mod} {
				if len(activityFor(t, c, r.ID).Items) != 0 {
					t.Fatal("reporter notice delivered to the source author or moderator")
				}
				mustStatus(t, c.request("GET", "me/content-reports/"+r.ID.String(), nil, 0, ""), 404)
			}
			raw := reporter.request("GET", "me/activity?filter=MODERATION", nil, 0, "")
			mustStatus(t, raw, 200)
			if !strings.Contains(raw.Body.String(), `"actor":null`) || !strings.Contains(raw.Body.String(), "A review outcome is available for your content report.") {
				t.Fatal("report notice must use fixed owner-only metadata")
			}
			var stored, payload string
			if e := integrationAdmin.QueryRow(context.Background(), "SELECT row_to_json(n)::text FROM social.notification n WHERE content_report_id=$1", r.ID).Scan(&stored); e != nil {
				t.Fatal(e)
			}
			for _, secret := range []string{"Private outcome source marker", "Fictional private report details", "Reviewed fictional community policy concern", "This content violates community policy", target.String(), DemoPrincipals[0].String(), DemoPrincipals[2].String()} {
				if strings.Contains(raw.Body.String(), secret) || strings.Contains(stored, secret) {
					t.Fatal("report text, source or identity entered notice metadata", secret)
				}
			}
			if e := integrationAdmin.QueryRow(context.Background(), "SELECT payload::text FROM infra.outbox WHERE aggregate_id=$1 AND event_type='ContentReportOutcomeRecorded'", r.ID).Scan(&payload); e != nil || payload != "{}" {
				t.Fatal("report outcome event contains private data", payload, e)
			}
			deniedSQL(t, a.Worker, "SELECT grounds FROM social.moderation_case")
			deniedSQL(t, a.Worker, "SELECT reason FROM social.moderation_decision")
			deniedSQL(t, a.Worker, "SELECT * FROM identity.principal")
			deniedSQL(t, a.DB, "SELECT * FROM social.activity_review_source")
			ctx := scopedContext(author, a.DB, vault.Grant{})
			var count int
			if e := a.store(ctx).QueryRow(ctx, "SELECT count(*) FROM social.activity_review_target WHERE source_id=$1", r.ID).Scan(&count); e != nil || count != 0 {
				t.Fatal("foreign report source visible", count, e)
			}
			for _, forged := range []struct {
				recipient uuid.UUID
				version   int64
			}{{myProfileID(t, author), 2}, {myProfileID(t, reporter), 99}} {
				_, e := a.Worker.Exec(context.Background(), "INSERT INTO social.notification(id,recipient_id,event_id,channel,kind,source_version,content_report_id,state) VALUES($1,$2,$3,'IN_APP','CONTENT_REPORT_OUTCOME',$4,$5,'SENT')", uuid.New(), forged.recipient, uuid.New(), forged.version, r.ID)
				var denial *pgconn.PgError
				if !errors.As(e, &denial) || denial.Code != "42501" {
					t.Fatalf("forged report owner/version must be denied by row policy: %v", e)
				}
			}
			path := "me/activity/" + items[0].ID.String() + "/read"
			mustStatus(t, author.request("PUT", path, map[string]any{"read": true}, 0, ""), 404)
			unread := unreadActivity(t, reporter)
			first := reporter.request("PUT", path, map[string]any{"read": true}, 0, "")
			mustStatus(t, first, 200)
			retry := reporter.request("PUT", path, map[string]any{"read": true}, 0, "")
			mustStatus(t, retry, 200)
			if first.Body.String() != retry.Body.String() || unreadActivity(t, reporter) != unread-1 {
				t.Fatal("report notice read state is not persistent/idempotent")
			}
			mustStatus(t, reporter.request("PUT", path, map[string]any{"read": false}, 0, ""), 200)
			if _, e := integrationAdmin.Exec(context.Background(), "UPDATE infra.outbox SET delivered_at=NULL WHERE aggregate_id=$1 AND event_type='ContentReportOutcomeRecorded'", r.ID); e != nil {
				t.Fatal(e)
			}
			if e := addEvent(context.Background(), dbgen.New(integrationAdmin), "CONTENT_REPORT", r.ID, 2, "ContentReportOutcomeRecorded", map[string]any{}); e != nil {
				t.Fatal(e)
			}
			drainActivity(t, a)
			if len(activityFor(t, reporter, r.ID).Items) != 1 || unreadActivity(t, reporter) != unread {
				t.Fatal("report redelivery duplicated the alert or changed read state")
			}
			w := reporter.request("GET", "me/content-reports/"+r.ID.String(), nil, 0, "")
			mustStatus(t, w, 200)
			if got := parsed[contentReportResult](t, w); got.Decision == nil || got.Decision.Action != fixture.action {
				t.Fatal("exact receipt does not contain the original outcome")
			}
		})
	}
}

func TestContentReportActivityConsentAndRetainedReceipt(t *testing.T) {
	a := testApp(t)
	reporter, author, mod := login(t, a, 0), login(t, a, 1), login(t, a, 2)
	cleanContentReports(t, reporter)
	profileID, authorID := myProfileID(t, reporter), myProfileID(t, author)
	t.Cleanup(func() {
		integrationAdmin.Exec(context.Background(), "DELETE FROM social.feed_preference WHERE profile_id=$1", profileID)
		integrationAdmin.Exec(context.Background(), "DELETE FROM social.mute WHERE profile_id=$1 AND muted_profile_id=$2", profileID, authorID)
		integrationAdmin.Exec(context.Background(), "DELETE FROM social.profile_block WHERE blocker_id=$1 AND blocked_id=$2", profileID, authorID)
	})
	p := published(t, a, author, mod, "Paused report activity "+uuid.NewString())
	r := submitContentReport(t, reporter, "POST", p.ID, 1)
	setReviewConsent(t, reporter, false)
	decideContentReport(t, mod, r, "DISMISS")
	drainActivity(t, a)
	setReviewConsent(t, reporter, true)
	drainActivity(t, a)
	if len(activityFor(t, reporter, r.ID).Items) != 0 {
		t.Fatal("paused outcome was replayed")
	}
	p = published(t, a, author, mod, "Retained report activity "+uuid.NewString())
	r = submitContentReport(t, reporter, "POST", p.ID, 1)
	muteTarget(t, reporter, "PROFILE", authorID, true, nil)
	flag(t, reporter, "blocks", authorID, true)
	mustStatus(t, author.request("DELETE", "posts/"+p.ID.String(), nil, p.Version, ""), 204)
	decideContentReport(t, mod, r, "DISMISS")
	drainActivity(t, a)
	items := activityFor(t, reporter, r.ID).Items
	if len(items) != 1 {
		t.Fatal("source deletion or block/mute hid own report outcome")
	}
	path := "me/activity/" + items[0].ID.String() + "/read"
	mustStatus(t, reporter.request("PUT", path, map[string]any{"read": true}, 0, ""), 200)
	setReviewConsent(t, reporter, false)
	if len(activityFor(t, reporter, r.ID).Items) != 0 || unreadActivity(t, reporter) != 0 {
		t.Fatal("paused report inbox/counts remain visible")
	}
	mustStatus(t, reporter.request("PUT", path, map[string]any{"read": false}, 0, ""), 404)
	w := reporter.request("GET", "me/content-reports/"+r.ID.String(), nil, 0, "")
	mustStatus(t, w, 200)
	if got := parsed[contentReportResult](t, w); got.Target != nil || got.TargetState != "UNAVAILABLE" || got.Decision == nil || got.Decision.Action != "DISMISS" {
		t.Fatal("retained private receipt exposed deleted/blocked source or lost outcome")
	}
	setReviewConsent(t, reporter, true)
	if got := activityFor(t, reporter, r.ID).Items; len(got) != 1 || got[0].ReadAt == nil {
		t.Fatal("resuming consent lost the delivered receipt or read state")
	}
	ctx := scopedContext(reporter, a.DB, vault.Grant{})
	if _, e := integrationAdmin.Exec(context.Background(), "UPDATE identity.session SET revoked_at=now() WHERE token_hash=$1", tokenHash(reporter.cookie.Value)); e != nil {
		t.Fatal(e)
	}
	var count int
	if e := a.store(ctx).QueryRow(ctx, "SELECT count(*) FROM social.activity_review_target WHERE source_id=$1", r.ID).Scan(&count); e != nil || count != 0 {
		t.Fatal("revoked session retained reporter source", e)
	}
	mustStatus(t, reporter.request("GET", "me/content-reports/"+r.ID.String(), nil, 0, ""), 401)
}

func TestContentReportActivityInactiveRecipientAndNoHistoricalBackfill(t *testing.T) {
	a := testApp(t)
	reporter, author, mod := login(t, a, 0), login(t, a, 1), login(t, a, 2)
	cleanContentReports(t, reporter)
	profileID := myProfileID(t, reporter)
	t.Cleanup(func() {
		integrationAdmin.Exec(context.Background(), "UPDATE social.profile SET state='ACTIVE' WHERE id=$1", profileID)
	})
	p := published(t, a, author, mod, "Inactive report activity "+uuid.NewString())
	r := submitContentReport(t, reporter, "POST", p.ID, 1)
	decideContentReport(t, mod, r, "DISMISS")
	if _, e := integrationAdmin.Exec(context.Background(), "UPDATE social.profile SET state='SUSPENDED' WHERE id=$1", profileID); e != nil {
		t.Fatal(e)
	}
	drainActivity(t, a)
	if _, e := integrationAdmin.Exec(context.Background(), "UPDATE social.profile SET state='ACTIVE' WHERE id=$1", profileID); e != nil {
		t.Fatal(e)
	}
	drainActivity(t, a)
	if len(activityFor(t, reporter, r.ID).Items) != 0 {
		t.Fatal("inactive recipient received/replayed queued alert")
	}
	p = published(t, a, author, mod, "Historical report activity "+uuid.NewString())
	r = submitContentReport(t, reporter, "POST", p.ID, 1)
	// Simulate an existing finalized report from before this event was introduced.
	if _, e := integrationAdmin.Exec(context.Background(), "INSERT INTO social.moderation_decision(id,moderation_case_id,sequence,action,rule_version,actor_ref,reason) VALUES($1,$2,1,'ALLOW','legacy',$3,'Historical report outcome')", uuid.New(), r.ID, DemoPrincipals[2]); e != nil {
		t.Fatal(e)
	}
	if e := dbgen.New(integrationAdmin).FinishModeration(context.Background(), r.ID); e != nil {
		t.Fatal(e)
	}
	drainActivity(t, a)
	if len(activityFor(t, reporter, r.ID).Items) != 0 {
		t.Fatal("historical report outcome was backfilled")
	}
}

package app

import (
	"context"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lavkushry/JanSetu-AI/services/backend/internal/store/dbgen"
	"github.com/lavkushry/JanSetu-AI/services/backend/internal/vault"
)

func setReviewConsent(t *testing.T, c client, enabled bool) {
	t.Helper()
	w := c.request("GET", "me/notification-preferences", nil, 0, "")
	mustStatus(t, w, 200)
	v := parsed[struct{ Version int64 }](t, w)
	mustStatus(t, c.request("PATCH", "me/notification-preferences", map[string]any{"inApp": enabled}, v.Version, ""), 200)
}
func TestPrivateReviewActivityOwnershipDeliveryAndRedelivery(t *testing.T) {
	a := testApp(t)
	owner, other, mod, reviewer := login(t, a, 0), login(t, a, 1), login(t, a, 2), login(t, a, 4)
	p := initialPost(t, owner)
	did := restrictInitial(t, mod, p.ID, uuid.Nil)
	drainActivity(t, a)
	notices := activityFor(t, owner, did).Items
	if len(notices) != 1 || notices[0].Kind != "MODERATION_DECISION" {
		t.Fatal("missing private moderation activity", notices)
	}
	for _, c := range []client{other, mod, reviewer} {
		if len(activityFor(t, c, did).Items) != 0 {
			t.Fatal("private decision delivered to another account")
		}
	}
	raw := owner.request("GET", "me/activity?filter=MODERATION", nil, 0, "")
	mustStatus(t, raw, 200)
	for _, secret := range []string{"Initial privately rejected fixture", "Internal review marker", "contact details", DemoPrincipals[0].String(), DemoPrincipals[2].String(), "reporterRef", "grounds", "ruleVersion"} {
		if strings.Contains(raw.Body.String(), secret) {
			t.Fatal("private preview or identity leaked", secret)
		}
	}
	var stored string
	if e := integrationAdmin.QueryRow(context.Background(), "SELECT row_to_json(n)::text FROM social.notification n WHERE moderation_decision_id=$1", did).Scan(&stored); e != nil {
		t.Fatal(e)
	}
	for _, secret := range []string{"Initial privately rejected fixture", "Internal review marker", "contact details", DemoPrincipals[0].String(), DemoPrincipals[2].String()} {
		if strings.Contains(stored, secret) {
			t.Fatal("stored private preview", secret)
		}
	}
	var payload string
	if e := integrationAdmin.QueryRow(context.Background(), "SELECT payload::text FROM infra.outbox WHERE aggregate_id=$1 AND event_type='ModerationDecisionRecorded'", did).Scan(&payload); e != nil || payload != "{}" {
		t.Fatal("event contains private content", payload, e)
	}
	deniedSQL(t, a.DB, "SELECT * FROM social.activity_review_source")
	deniedSQL(t, a.Worker, "SELECT reason FROM social.moderation_decision")
	deniedSQL(t, a.Worker, "SELECT grounds FROM social.appeal")
	deniedSQL(t, a.Worker, "SELECT author_reason FROM social.appeal_decision")
	deniedSQL(t, a.Worker, "SELECT * FROM identity.account_binding")
	ctx := scopedContext(other, a.DB, vault.Grant{})
	var count int
	if e := a.store(ctx).QueryRow(ctx, "SELECT count(*) FROM social.activity_review_target WHERE source_id=$1", did).Scan(&count); e != nil || count != 0 {
		t.Fatal("foreign private target escaped owner view", e)
	}
	// Even the worker cannot deliver a private source to the wrong owner.
	if _, e := a.Worker.Exec(context.Background(), "INSERT INTO social.notification(id,recipient_id,event_id,channel,kind,source_version,moderation_decision_id,state) VALUES($1,$2,$3,'IN_APP','MODERATION_DECISION',1,$4,'SENT')", uuid.New(), myProfileID(t, other), uuid.New(), did); e == nil {
		t.Fatal("worker forged private recipient")
	}
	nid := notices[0].ID
	path := "me/activity/" + nid.String() + "/read"
	mustStatus(t, other.request("PUT", path, map[string]any{"read": true}, 0, ""), 404)
	before := unreadActivity(t, owner)
	first := owner.request("PUT", path, map[string]any{"read": true}, 0, "")
	mustStatus(t, first, 200)
	repeat := owner.request("PUT", path, map[string]any{"read": true}, 0, "")
	mustStatus(t, repeat, 200)
	if first.Body.String() != repeat.Body.String() || unreadActivity(t, owner) != before-1 {
		t.Fatal("private read state is not durable/idempotent")
	}
	mustStatus(t, owner.request("PUT", path, map[string]any{"read": false}, 0, ""), 200)
	if unreadActivity(t, owner) != before {
		t.Fatal("private notice not restored to unread")
	}
	if _, e := integrationAdmin.Exec(context.Background(), "UPDATE infra.outbox SET delivered_at=NULL WHERE aggregate_id=$1 AND event_type='ModerationDecisionRecorded'", did); e != nil {
		t.Fatal(e)
	}
	if e := addEvent(context.Background(), dbgen.New(integrationAdmin), "MODERATION_DECISION", did, 1, "ModerationDecisionRecorded", map[string]any{}); e != nil {
		t.Fatal(e)
	}
	drainActivity(t, a)
	if len(activityFor(t, owner, did).Items) != 1 {
		t.Fatal("private decision redelivery duplicated activity")
	}
	appeal := takeAppeal(t, reviewer, newAppeal(t, owner, did))
	decideAppealTest(t, reviewer, appeal, "UPHELD")
	drainActivity(t, a)
	if got := activityFor(t, owner, appeal.ID).Items; len(got) != 1 || got[0].Kind != "APPEAL_OUTCOME" {
		t.Fatal("missing private appeal outcome", got)
	}
	for _, c := range []client{other, mod, reviewer} {
		if len(activityFor(t, c, appeal.ID).Items) != 0 {
			t.Fatal("appeal outcome delivered to staff/other user")
		}
	}
	if e := addEvent(context.Background(), dbgen.New(integrationAdmin), "APPEAL", appeal.ID, 3, "AppealOutcomeRecorded", map[string]any{}); e != nil {
		t.Fatal(e)
	}
	drainActivity(t, a)
	if len(activityFor(t, owner, appeal.ID).Items) != 1 {
		t.Fatal("appeal redelivery duplicated activity")
	}
	mustStatus(t, owner.request("DELETE", "posts/"+p.ID.String(), nil, 2, ""), 204)
	if len(activityFor(t, owner, did).Items) != 1 || len(activityFor(t, owner, appeal.ID).Items) != 1 {
		t.Fatal("deleted source hid private decision history")
	}
	mustStatus(t, owner.request("GET", "me/moderation-decisions/"+did.String(), nil, 0, ""), 200)
	mustStatus(t, owner.request("GET", "me/appeals/"+appeal.ID.String(), nil, 0, ""), 200)
	// Owner projections and notification RLS must also react to session revocation.
	ctx = scopedContext(owner, a.DB, vault.Grant{})
	if _, e := integrationAdmin.Exec(context.Background(), "UPDATE identity.session SET revoked_at=now() WHERE token_hash=$1", tokenHash(owner.cookie.Value)); e != nil {
		t.Fatal(e)
	}
	if e := a.store(ctx).QueryRow(ctx, "SELECT count(*) FROM social.activity_review_target").Scan(&count); e != nil || count != 0 {
		t.Fatal("revoked session retained private targets", e)
	}
	mustStatus(t, owner.request("GET", "me/activity?filter=MODERATION", nil, 0, ""), 401)
}
func TestPrivateReviewActivityConsentMutesAndCurrentRecipient(t *testing.T) {
	a := testApp(t)
	owner, mod, reviewer := login(t, a, 0), login(t, a, 2), login(t, a, 4)
	profileID := myProfileID(t, owner)
	t.Cleanup(func() {
		integrationAdmin.Exec(context.Background(), "UPDATE social.profile SET state='ACTIVE' WHERE id=$1", profileID)
		integrationAdmin.Exec(context.Background(), "DELETE FROM social.feed_preference WHERE profile_id=$1", profileID)
	})
	setReviewConsent(t, owner, false)
	p := initialPost(t, owner)
	did := restrictInitial(t, mod, p.ID, uuid.Nil)
	drainActivity(t, a)
	if len(activityFor(t, owner, did).Items) != 0 {
		t.Fatal("paused consent delivered private alert")
	}
	setReviewConsent(t, owner, true)
	drainActivity(t, a)
	if len(activityFor(t, owner, did).Items) != 0 {
		t.Fatal("skipped private alert backfilled")
	}
	// Mutes and blocks do not suppress the owner's own institutional review record.
	modID := myProfileID(t, mod)
	mute := map[string]any{"targetType": "PROFILE", "targetId": modID, "active": true}
	mustStatus(t, owner.request("PUT", "me/mutes", mute, 0, ""), 200)
	flag(t, owner, "blocks", modID, true)
	t.Cleanup(func() { flag(t, owner, "blocks", modID, false) })
	t.Cleanup(func() { mute["active"] = false; mustStatus(t, owner.request("PUT", "me/mutes", mute, 0, ""), 200) })
	p = initialPost(t, owner)
	did = restrictInitial(t, mod, p.ID, uuid.Nil)
	drainActivity(t, a)
	if len(activityFor(t, owner, did).Items) != 1 {
		t.Fatal("block/mute suppressed private review")
	}
	setReviewConsent(t, owner, false)
	if len(activityFor(t, owner, did).Items) != 0 || unreadActivity(t, owner) != 0 {
		t.Fatal("paused consent did not hide existing notices")
	}
	mustStatus(t, owner.request("GET", "me/moderation-decisions/"+did.String(), nil, 0, ""), 200)
	appeal := takeAppeal(t, reviewer, newAppeal(t, owner, did))
	decideAppealTest(t, reviewer, appeal, "REVERSED")
	drainActivity(t, a)
	setReviewConsent(t, owner, true)
	drainActivity(t, a)
	if len(activityFor(t, owner, appeal.ID).Items) != 0 || len(activityFor(t, owner, did).Items) != 1 {
		t.Fatal("consent resume replayed outcome or lost existing notice")
	}
	p = initialPost(t, owner)
	pendingID := restrictInitial(t, mod, p.ID, uuid.Nil)
	if _, e := integrationAdmin.Exec(context.Background(), "UPDATE social.profile SET state='SUSPENDED' WHERE id=$1", profileID); e != nil {
		t.Fatal(e)
	}
	ctx := scopedContext(owner, a.DB, vault.Grant{})
	var count int
	if e := a.store(ctx).QueryRow(ctx, "SELECT count(*) FROM social.activity_visible WHERE moderation_decision_id=$1", did).Scan(&count); e != nil || count != 0 {
		t.Fatal("inactive recipient retained notices", e)
	}
	drainActivity(t, a)
	if _, e := integrationAdmin.Exec(context.Background(), "UPDATE social.profile SET state='ACTIVE' WHERE id=$1", profileID); e != nil {
		t.Fatal(e)
	}
	if len(activityFor(t, owner, pendingID).Items) != 0 {
		t.Fatal("inactive recipient got queued notice")
	}
}
func TestPrivateReviewActivityRemovalAndNoHistoricalBackfill(t *testing.T) {
	a := testApp(t)
	owner, reporter, mod := login(t, a, 0), login(t, a, 1), login(t, a, 2)
	cleanContentReports(t, reporter)
	p := published(t, a, owner, mod, "Published activity removal thread "+uuid.NewString())
	comment := approvedReply(t, owner, mod, p.ID, nil)
	did := removedDecision(t, mod, reporter, "COMMENT", comment)
	drainActivity(t, a)
	if len(activityFor(t, owner, did).Items) != 1 || len(activityFor(t, reporter, did).Items) != 0 {
		t.Fatal("comment removal notice has wrong recipient")
	}
	mustStatus(t, owner.request("DELETE", "comments/"+comment.String(), nil, 3, ""), 204)
	if len(activityFor(t, owner, did).Items) != 1 {
		t.Fatal("deleted comment erased private alert")
	}
	report := submitContentReport(t, reporter, "POST", p.ID, 1)
	decideContentReport(t, mod, report, "DISMISS")
	drainActivity(t, a)
	var dismissed uuid.UUID
	if e := integrationAdmin.QueryRow(context.Background(), "SELECT id FROM social.moderation_decision WHERE moderation_case_id=$1", report.ID).Scan(&dismissed); e != nil {
		t.Fatal(e)
	}
	if len(activityFor(t, owner, dismissed).Items) != 0 {
		t.Fatal("dismissal sent an author notice")
	}
	var approved uuid.UUID
	if e := integrationAdmin.QueryRow(context.Background(), "SELECT d.id FROM social.moderation_decision d JOIN social.moderation_case m ON m.id=d.moderation_case_id WHERE m.post_id=$1 AND d.action='ALLOW' AND m.reporter_ref IS NULL", p.ID).Scan(&approved); e != nil {
		t.Fatal(e)
	}
	if got := activityFor(t, owner, approved).Items; len(got) != 1 || got[0].Kind != "PUBLICATION_APPROVAL" {
		t.Fatal("publication approval is missing", got)
	}
	mid, legacy := uuid.New(), uuid.New()
	if _, e := integrationAdmin.Exec(context.Background(), "INSERT INTO social.moderation_case(id,post_id,target_version,reason_code,grounds,state) VALUES($1,$2,1,'PUBLICATION_REVIEW','Historical synthetic review','DECIDED')", mid, p.ID); e != nil {
		t.Fatal(e)
	}
	if _, e := integrationAdmin.Exec(context.Background(), "INSERT INTO social.moderation_decision(id,moderation_case_id,sequence,action,rule_version,actor_ref,reason) VALUES($1,$2,1,'RESTRICT','legacy',$3,'Historical private note')", legacy, mid, DemoPrincipals[2]); e != nil {
		t.Fatal(e)
	}
	drainActivity(t, a)
	if len(activityFor(t, owner, legacy).Items) != 0 {
		t.Fatal("historical record generated retroactive alert")
	}
}
func TestPrivateReviewActivityPaginationAndFilterScope(t *testing.T) {
	a := testApp(t)
	owner, other, mod := login(t, a, 0), login(t, a, 1), login(t, a, 2)
	p := initialPost(t, owner)
	did := restrictInitial(t, mod, p.ID, uuid.Nil)
	drainActivity(t, a)
	at := time.Now().Add(time.Hour).UTC().Truncate(time.Microsecond)
	ids := map[uuid.UUID]bool{}
	for i := 0; i < 23; i++ {
		mid, d, nid := uuid.New(), uuid.New(), uuid.New()
		ids[nid] = false
		if _, e := integrationAdmin.Exec(context.Background(), "INSERT INTO social.moderation_case(id,post_id,target_version,reason_code,grounds,state) VALUES($1,$2,1,'PUBLICATION_REVIEW','Pagination synthetic review','DECIDED')", mid, p.ID); e != nil {
			t.Fatal(e)
		}
		if _, e := integrationAdmin.Exec(context.Background(), "INSERT INTO social.moderation_decision(id,moderation_case_id,sequence,action,rule_version,actor_ref,reason) VALUES($1,$2,1,'RESTRICT','test',$3,'Private pagination note')", d, mid, DemoPrincipals[2]); e != nil {
			t.Fatal(e)
		}
		if _, e := integrationAdmin.Exec(context.Background(), "INSERT INTO social.notification(id,recipient_id,event_id,channel,kind,source_version,moderation_decision_id,state,created_at) VALUES($1,$2,$3,'IN_APP','MODERATION_DECISION',1,$4,'SENT',$5)", nid, myProfileID(t, owner), uuid.New(), d, at); e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() {
			integrationAdmin.Exec(context.Background(), "DELETE FROM social.notification WHERE id=$1", nid)
			integrationAdmin.Exec(context.Background(), "DELETE FROM social.moderation_decision WHERE id=$1", d)
			integrationAdmin.Exec(context.Background(), "DELETE FROM social.moderation_case WHERE id=$1", mid)
		})
	}
	path := "me/activity?filter=MODERATION"
	w := owner.request("GET", path, nil, 0, "")
	mustStatus(t, w, 200)
	first := parsed[activityResult](t, w)
	if len(first.Items) != 20 || first.NextCursor == nil {
		t.Fatal("private notice page is unbounded/incomplete")
	}
	cursor := url.QueryEscape(*first.NextCursor)
	mustStatus(t, other.request("GET", path+"&cursor="+cursor, nil, 0, ""), 422)
	mustStatus(t, owner.request("GET", "me/activity?filter=SOCIAL&cursor="+cursor, nil, 0, ""), 422)
	mustStatus(t, owner.request("GET", path+"&cursor="+cursor+"x", nil, 0, ""), 422)
	c, e := a.profilePageCursor(*first.NextCursor, "activity:MODERATION", myProfileID(t, owner), myProfileID(t, owner))
	if e != nil {
		t.Fatal(e)
	}
	c.Expires = time.Now().Add(-time.Second).Unix()
	mustStatus(t, owner.request("GET", path+"&cursor="+url.QueryEscape(a.encodeProfileCursor(c)), nil, 0, ""), 410)
	for page := first; ; {
		for _, v := range page.Items {
			if v.Kind != "MODERATION_DECISION" && v.Kind != "PUBLICATION_APPROVAL" && v.Kind != "APPEAL_OUTCOME" && v.Kind != "CONTENT_REPORT_OUTCOME" {
				t.Fatal("mixed activity filter")
			}
			if seen, ok := ids[v.ID]; ok {
				if seen {
					t.Fatal("duplicate notice page")
				}
				ids[v.ID] = true
			}
		}
		if page.NextCursor == nil {
			break
		}
		w = owner.request("GET", path+"&cursor="+url.QueryEscape(*page.NextCursor), nil, 0, "")
		mustStatus(t, w, 200)
		page = parsed[activityResult](t, w)
	}
	for id, seen := range ids {
		if !seen {
			t.Fatal("missing notification", id)
		}
	}
	if len(activityFor(t, owner, did).Items) != 1 {
		t.Fatal("old record lost beyond newest page")
	}
}

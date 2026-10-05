package app

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/lavkushry/JanSetu-AI/services/backend/internal/store/dbgen"
	"github.com/lavkushry/JanSetu-AI/services/backend/internal/vault"
)

type activityResult struct {
	Items []struct {
		ID     uuid.UUID
		Kind   string
		ReadAt *time.Time
		Target struct{ ID uuid.UUID }
	}
	NextCursor *string
}

func drainActivity(t *testing.T, a *App) {
	t.Helper()
	for i := 0; i < 2000; i++ {
		worked, e := a.ProjectOnce(context.Background(), "activity-test")
		if e != nil {
			t.Fatal(e)
		}
		if !worked {
			return
		}
	}
	t.Fatal("outbox did not drain")
}
func activityFor(t *testing.T, c client, target uuid.UUID) activityResult {
	t.Helper()
	out := activityResult{}
	route := "me/activity"
	for {
		w := c.request("GET", route, nil, 0, "")
		mustStatus(t, w, 200)
		p := parsed[activityResult](t, w)
		for _, n := range p.Items {
			if n.Target.ID == target {
				out.Items = append(out.Items, n)
			}
		}
		if p.NextCursor == nil {
			return out
		}
		route = "me/activity?cursor=" + *p.NextCursor
	}
}
func unreadActivity(t *testing.T, c client) int64 {
	t.Helper()
	w := c.request("GET", "me/activity/summary", nil, 0, "")
	mustStatus(t, w, 200)
	return parsed[struct{ UnreadCount int64 }](t, w).UnreadCount
}
func approvedReply(t *testing.T, writer, mod client, post uuid.UUID, parent *uuid.UUID) uuid.UUID {
	t.Helper()
	w := writer.request("POST", "posts/"+post.String()+"/comments", CommentInput{Body: "Private candidate text must never be a notification preview", ParentID: parent}, 0, uuid.NewString())
	mustStatus(t, w, 201)
	cid := parsed[struct{ ID uuid.UUID }](t, w).ID
	review(t, mod, uuid.Nil, cid, 1)
	return cid
}

func TestActivityApprovalReadOwnershipAndLiveVisibility(t *testing.T) {
	a := testApp(t)
	owner, writer, mod := login(t, a, 0), login(t, a, 1), login(t, a, 2)
	p := published(t, a, owner, mod, "Activity approval and visibility fixture")
	pending := writer.request("POST", "posts/"+p.ID.String()+"/comments", CommentInput{Body: "Private candidate text must never be a notification preview"}, 0, uuid.NewString())
	mustStatus(t, pending, 201)
	cid := parsed[struct{ ID uuid.UUID }](t, pending).ID
	drainActivity(t, a)
	if len(activityFor(t, owner, p.ID).Items) != 0 {
		t.Fatal("pending reply generated an alert")
	}
	review(t, mod, uuid.Nil, cid, 1)
	drainActivity(t, a)
	items := activityFor(t, owner, p.ID).Items
	if len(items) != 1 || len(activityFor(t, writer, p.ID).Items) != 0 {
		t.Fatal("wrong reply recipient")
	}
	nid := items[0].ID
	path := "me/activity/" + nid.String() + "/read"
	raw := owner.request("GET", "me/activity", nil, 0, "").Body.String()
	if strings.Contains(raw, "Private candidate") || strings.Contains(raw, DemoPrincipals[1].String()) || strings.Contains(raw, "recipient_id") || strings.Contains(raw, "commentId") {
		t.Fatal("activity DTO leaked source or private fields")
	}
	mustStatus(t, client{app: a}.request("GET", "me/activity", nil, 0, ""), http.StatusUnauthorized)
	mustStatus(t, writer.request("PUT", path, map[string]any{"read": true}, 0, ""), 404)
	mustStatus(t, owner.request("PUT", path, map[string]any{}, 0, ""), 422)
	unread := unreadActivity(t, owner)
	first := owner.request("PUT", path, map[string]any{"read": true}, 0, "")
	mustStatus(t, first, 200)
	second := owner.request("PUT", path, map[string]any{"read": true}, 0, "")
	mustStatus(t, second, 200)
	if first.Body.String() != second.Body.String() {
		t.Fatal("read state retry changed its timestamp")
	}
	if unreadActivity(t, owner) != unread-1 {
		t.Fatal("read alert remained in unread count")
	}
	if activityFor(t, owner, p.ID).Items[0].ReadAt == nil {
		t.Fatal("read state did not persist")
	}
	mustStatus(t, owner.request("PUT", path, map[string]any{"read": false}, 0, ""), 200)
	if activityFor(t, owner, p.ID).Items[0].ReadAt != nil {
		t.Fatal("unread state did not persist")
	}
	if unreadActivity(t, owner) != unread {
		t.Fatal("unread alert missing from count")
	}
	// API and direct runtime queries must both enforce owner scope.
	var count int
	ctx := scopedContext(writer, a.DB, vault.Grant{})
	if e := a.store(ctx).QueryRow(ctx, "SELECT count(*) FROM social.notification WHERE id=$1", nid).Scan(&count); e != nil || count != 0 {
		t.Fatalf("foreign notification readable: %d %v", count, e)
	}
	if tag, e := a.store(ctx).Exec(ctx, "UPDATE social.notification SET read_at=now() WHERE id=$1", nid); e != nil || tag.RowsAffected() != 0 {
		t.Fatal("foreign read-state mutation escaped RLS", e)
	}
	deniedSQL(t, a.DB, "UPDATE social.notification SET recipient_id=$1 WHERE id=$2", myProfileID(t, writer), nid)
	deniedSQL(t, a.Worker, "SELECT body FROM social.comment")
	deniedSQL(t, a.Worker, "SELECT statement FROM ops.report")
	// Public edits must not send a second alert.
	mustStatus(t, writer.request("PATCH", "comments/"+cid.String(), map[string]any{"body": "Second private candidate must stay out of activity"}, 2, ""), 200)
	review(t, mod, uuid.Nil, cid, 2)
	drainActivity(t, a)
	if len(activityFor(t, owner, p.ID).Items) != 1 {
		t.Fatal("approved edit duplicated reply activity")
	}
	// Either-direction block suppresses reads, counts and state changes.
	writerID := myProfileID(t, writer)
	flag(t, writer, "blocks", myProfileID(t, owner), true)
	t.Cleanup(func() { flag(t, writer, "blocks", myProfileID(t, owner), false) })
	if len(activityFor(t, owner, p.ID).Items) != 0 {
		t.Fatal("blocked reply remained visible")
	}
	if unreadActivity(t, owner) != unread-1 {
		t.Fatal("blocked source leaked through unread count")
	}
	mustStatus(t, owner.request("PUT", path, map[string]any{"read": true}, 0, ""), 404)
	flag(t, writer, "blocks", myProfileID(t, owner), false)
	// Honor active mute and channel opt-out without exposing preferences to others.
	muteID := uuid.New()
	if _, e := integrationAdmin.Exec(context.Background(), "INSERT INTO social.mute(id,profile_id,muted_profile_id) VALUES($1,$2,$3)", muteID, myProfileID(t, owner), writerID); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { integrationAdmin.Exec(context.Background(), "DELETE FROM social.mute WHERE id=$1", muteID) })
	if len(activityFor(t, owner, p.ID).Items) != 0 {
		t.Fatal("muted reply remained visible")
	}
	if _, e := integrationAdmin.Exec(context.Background(), "UPDATE social.mute SET expires_at=now()-interval '1 second' WHERE id=$1", muteID); e != nil {
		t.Fatal(e)
	}
	if len(activityFor(t, owner, p.ID).Items) != 1 {
		t.Fatal("expired mute hid reply")
	}
	ownerID := myProfileID(t, owner)
	if _, e := integrationAdmin.Exec(context.Background(), "INSERT INTO social.feed_preference(profile_id,notification_channels,policy_version) VALUES($1,'{}','activity-test')", ownerID); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		integrationAdmin.Exec(context.Background(), "DELETE FROM social.feed_preference WHERE profile_id=$1 AND policy_version='activity-test'", ownerID)
	})
	if len(activityFor(t, owner, p.ID).Items) != 0 || unreadActivity(t, owner) != 0 {
		t.Fatal("in-app channel opt-out was ignored")
	}
	if e := a.store(ctx).QueryRow(ctx, "SELECT count(*) FROM social.feed_preference WHERE profile_id=$1", ownerID).Scan(&count); e != nil || count != 0 {
		t.Fatal("foreign preference escaped RLS", e)
	}
	if _, e := integrationAdmin.Exec(context.Background(), "DELETE FROM social.feed_preference WHERE profile_id=$1", ownerID); e != nil {
		t.Fatal(e)
	}
	mustStatus(t, writer.request("DELETE", "comments/"+cid.String(), nil, 4, ""), 204)
	if len(activityFor(t, owner, p.ID).Items) != 0 {
		t.Fatal("deleted reply retained an alert")
	}
}

func TestActivityPaginationDedupeAndReplyRecipients(t *testing.T) {
	a := testApp(t)
	owner, writer, mod := login(t, a, 0), login(t, a, 1), login(t, a, 2)
	p := published(t, a, owner, mod, "Activity pagination fixture")
	ids := threadComments(t, p.ID, myProfileID(t, writer), nil, 0, time.Now().Add(-time.Hour), 25)
	q := dbgen.New(integrationAdmin)
	for _, cid := range ids {
		if e := addEvent(context.Background(), q, "POST", p.ID, p.Version, "CommentPublished", map[string]any{"commentId": cid, "revision": 1}); e != nil {
			t.Fatal(e)
		}
	}
	drainActivity(t, a)
	if len(activityFor(t, owner, p.ID).Items) != 25 {
		t.Fatal("activity pagination lost rows")
	}
	w := owner.request("GET", "me/activity?filter=SOCIAL", nil, 0, "")
	mustStatus(t, w, 200)
	page := parsed[activityResult](t, w)
	if len(page.Items) != 20 || page.NextCursor == nil {
		t.Fatal("missing activity page")
	}
	mustStatus(t, writer.request("GET", "me/activity?filter=SOCIAL&cursor="+*page.NextCursor, nil, 0, ""), 422)
	mustStatus(t, owner.request("GET", "me/activity?filter=CASES&cursor="+*page.NextCursor, nil, 0, ""), 422)
	mustStatus(t, owner.request("GET", "me/activity?filter=INVALID", nil, 0, ""), 422)
	c, e := a.profilePageCursor(*page.NextCursor, "activity:SOCIAL", myProfileID(t, owner), myProfileID(t, owner))
	if e != nil {
		t.Fatal(e)
	}
	c.Expires = time.Now().Add(-time.Second).Unix()
	mustStatus(t, owner.request("GET", "me/activity?filter=SOCIAL&cursor="+a.encodeProfileCursor(c), nil, 0, ""), 410)
	// Both a redelivery and a second equivalent event remain duplicate safe.
	if _, e := integrationAdmin.Exec(context.Background(), "UPDATE infra.outbox SET delivered_at=NULL WHERE event_type='CommentPublished' AND payload->>'commentId'=$1", ids[0].String()); e != nil {
		t.Fatal(e)
	}
	if e := addEvent(context.Background(), q, "POST", p.ID, p.Version, "CommentPublished", map[string]any{"commentId": ids[0], "revision": 1}); e != nil {
		t.Fatal(e)
	}
	drainActivity(t, a)
	if len(activityFor(t, owner, p.ID).Items) != 25 {
		t.Fatal("duplicate delivery generated more activity")
	}
	approvedReply(t, mod, mod, p.ID, &ids[0])
	drainActivity(t, a)
	if len(activityFor(t, writer, p.ID).Items) != 1 || len(activityFor(t, owner, p.ID).Items) != 25 {
		t.Fatal("nested reply did not notify its direct parent author")
	}
	approvedReply(t, owner, mod, p.ID, nil)
	drainActivity(t, a)
	if len(activityFor(t, owner, p.ID).Items) != 25 {
		t.Fatal("self reply generated an alert")
	}
	// Make source unavailable between pages. No stale rows or unread-count leakage.
	flag(t, owner, "blocks", myProfileID(t, writer), true)
	t.Cleanup(func() { flag(t, owner, "blocks", myProfileID(t, writer), false) })
	w = owner.request("GET", "me/activity?filter=SOCIAL&cursor="+*page.NextCursor, nil, 0, "")
	mustStatus(t, w, 200)
	if len(parsed[activityResult](t, w).Items) != 0 {
		t.Fatal("later page kept blocked activity")
	}
}

func TestCaseActivityOnlyReviewedProgressForCurrentFollowers(t *testing.T) {
	a := testApp(t)
	resident, follower, coord, officer := login(t, a, 0), login(t, a, 1), login(t, a, 2), login(t, a, 3)
	w := resident.request("POST", "service-reports", ReportInput{ClientSubmissionID: uuid.New(), Statement: "PRIVATE original civic statement", LocationLabel: "Synthetic crossing", Category: "FOOTPATH", LanguageTag: "en-IN", PublicationPreference: "SANITIZED_RECEIPT"}, 0, uuid.NewString())
	mustStatus(t, w, 201)
	reportID := parsed[struct{ ID uuid.UUID }](t, w).ID
	w = coord.request("POST", "authority/reports/"+reportID.String()+"/triage", map[string]any{"agencyId": "30000000-0000-4000-8000-000000000001", "category": "FOOTPATH", "urgencyTier": 2, "reason": "Fictional restoration task assessed for local testing"}, 1, "")
	mustStatus(t, w, 201)
	cid := parsed[struct{ CaseID uuid.UUID }](t, w).CaseID
	publication := map[string]any{"title": "Reviewed crossing progress", "summary": "A fictional restoration task was proposed.", "area": "Indiranagar", "reviewed": true, "publicationVersion": 0, "reason": "Private synthetic publication review"}
	// Model a follow transaction that started before publication but waited to
	// mutate until afterwards. Transaction-start timestamps would backfill it.
	lateFollow, e := integrationAdmin.Begin(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	defer lateFollow.Rollback(context.Background())
	if _, e = lateFollow.Exec(context.Background(), "SELECT now()"); e != nil {
		t.Fatal(e)
	}
	w = coord.request("POST", "authority/cases/"+cid.String()+"/publications", publication, 1, "")
	mustStatus(t, w, 200)
	rid := parsed[struct{ ReceiptID uuid.UUID }](t, w).ReceiptID
	if e = dbgen.New(lateFollow).SetCaseFollow(context.Background(), dbgen.SetCaseFollowParams{ProfileID: myProfileID(t, follower), ReceiptID: rid}); e != nil {
		t.Fatal(e)
	}
	if e = lateFollow.Commit(context.Background()); e != nil {
		t.Fatal(e)
	}
	// Following after publication must not receive an old event waiting in the outbox.
	followPath := "case-receipts/" + rid.String() + "/follow"
	mustStatus(t, follower.request("PUT", followPath, map[string]any{"following": true}, 0, ""), 200)
	drainActivity(t, a)
	if len(activityFor(t, follower, rid).Items) != 0 {
		t.Fatal("new follow backfilled an earlier publication")
	}
	w = officer.request("GET", "authority/cases/"+cid.String(), nil, 0, "")
	mustStatus(t, w, 200)
	detail := parsed[struct {
		Obligations []struct {
			ID      uuid.UUID
			Version int64
		}
	}](t, w)
	mustStatus(t, officer.request("POST", "authority/obligations/"+detail.Obligations[0].ID.String()+"/accept", map[string]any{"summary": "PRIVATE agency work summary"}, detail.Obligations[0].Version, ""), 200)
	drainActivity(t, a)
	if len(activityFor(t, follower, rid).Items) != 0 {
		t.Fatal("private operational event generated public activity")
	}
	publication["publicationVersion"] = 1
	publication["summary"] = "The agency has accepted the fictional public restoration task."
	mustStatus(t, coord.request("POST", "authority/cases/"+cid.String()+"/publications", publication, 2, ""), 200)
	publication["publicationVersion"] = 2
	mustStatus(t, coord.request("POST", "authority/cases/"+cid.String()+"/publications", publication, 2, ""), 200)
	drainActivity(t, a)
	items := activityFor(t, follower, rid).Items
	if len(items) != 1 || items[0].Kind != "CASE_PROGRESS" {
		t.Fatal("published progress missing or duplicated")
	}
	w = follower.request("GET", "me/activity?filter=CASES", nil, 0, "")
	mustStatus(t, w, 200)
	for _, private := range []string{cid.String(), reportID.String(), "PRIVATE agency", "PRIVATE original", DemoPrincipals[0].String()} {
		if strings.Contains(w.Body.String(), private) {
			t.Fatal("public activity leaked", private)
		}
	}
	mustStatus(t, follower.request("PUT", followPath, map[string]any{"following": false}, 0, ""), 200)
	if len(activityFor(t, follower, rid).Items) != 0 {
		t.Fatal("unfollowed progress remained visible")
	}
	mustStatus(t, follower.request("PUT", "me/activity/"+items[0].ID.String()+"/read", map[string]any{"read": true}, 0, ""), 404)
	mustStatus(t, follower.request("PUT", followPath, map[string]any{"following": true}, 0, ""), 200)
	if len(activityFor(t, follower, rid).Items) != 0 {
		t.Fatal("refollow resurrected earlier activity")
	}
	// Stale publication versions cannot be projected against newer receipt state.
	if e := dbgen.New(a.Worker).DeliverCaseActivity(context.Background(), dbgen.DeliverCaseActivityParams{EventID: uuid.New(), ReceiptID: rid, SourceVersion: pgtype.Int8{Int64: 1, Valid: true}, EventTime: pgtype.Timestamptz{Time: time.Now(), Valid: true}}); e != nil {
		t.Fatal(e)
	}
	if len(activityFor(t, follower, rid).Items) != 0 {
		t.Fatal("stale projection generated activity")
	}
}

package app

import (
	"context"
	"errors"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lavkushry/JanSetu-AI/services/backend/internal/vault"
)

type contentReportResult struct {
	ID                      uuid.UUID
	Version, TargetRevision int64
	State, TargetState      string
	Target                  any
	Decision                *struct{ Action, Reason string }
}

func contentReporterPrincipal(t *testing.T, c client) uuid.UUID {
	t.Helper()
	var principal uuid.UUID
	if err := integrationAdmin.QueryRow(context.Background(), "SELECT principal_id FROM identity.session WHERE token_hash=$1", tokenHash(c.cookie.Value)).Scan(&principal); err != nil {
		t.Fatal(err)
	}
	return principal
}

func cleanContentReports(t *testing.T, reporter client) {
	t.Helper()
	pid := contentReporterPrincipal(t, reporter)
	t.Cleanup(func() {
		integrationAdmin.Exec(context.Background(), "DELETE FROM social.notification WHERE moderation_decision_id IN (SELECT d.id FROM social.moderation_decision d JOIN social.moderation_case m ON m.id=d.moderation_case_id WHERE m.reporter_ref=$1) OR appeal_id IN (SELECT a.id FROM social.appeal a JOIN social.moderation_decision d ON d.id=a.decision_id JOIN social.moderation_case m ON m.id=d.moderation_case_id WHERE m.reporter_ref=$1)", pid)
		integrationAdmin.Exec(context.Background(), "DELETE FROM social.appeal_decision WHERE appeal_id IN (SELECT id FROM social.appeal WHERE decision_id IN (SELECT d.id FROM social.moderation_decision d JOIN social.moderation_case m ON m.id=d.moderation_case_id WHERE m.reporter_ref=$1))", pid)
		integrationAdmin.Exec(context.Background(), "DELETE FROM social.appeal WHERE decision_id IN (SELECT d.id FROM social.moderation_decision d JOIN social.moderation_case m ON m.id=d.moderation_case_id WHERE m.reporter_ref=$1)", pid)
		integrationAdmin.Exec(context.Background(), "DELETE FROM social.moderation_decision WHERE moderation_case_id IN (SELECT id FROM social.moderation_case WHERE reporter_ref=$1)", pid)
		integrationAdmin.Exec(context.Background(), "DELETE FROM social.moderation_case WHERE reporter_ref=$1", pid)
	})
}
func reportInput(kind string, id uuid.UUID, revision int64) map[string]any {
	return map[string]any{"targetType": kind, "targetId": id, "targetRevision": revision, "reasonCode": "SPAM", "details": "Fictional private report details"}
}
func submitContentReport(t *testing.T, c client, kind string, id uuid.UUID, revision int64) contentReportResult {
	t.Helper()
	w := c.request("POST", "content-reports", reportInput(kind, id, revision), 0, uuid.NewString())
	mustStatus(t, w, 201)
	return parsed[contentReportResult](t, w)
}
func decideContentReport(t *testing.T, mod client, report contentReportResult, action string) {
	t.Helper()
	mustStatus(t, mod.request("POST", "moderation/content-reports/"+report.ID.String()+"/decisions", map[string]any{"action": action, "authorReason": "This content violates community policy", "reason": "Reviewed fictional community policy concern", "targetRevision": report.TargetRevision}, report.Version, ""), 200)
}

func TestContentReportsPublishedRevisionPrivacyOwnershipAndRetries(t *testing.T) {
	a := testApp(t)
	owner, writer, mod := login(t, a, 0), login(t, a, 1), login(t, a, 2)
	cleanContentReports(t, owner)
	p := published(t, a, writer, mod, "Published report fixture "+uuid.NewString())
	edit := writer.request("PATCH", "posts/"+p.ID.String(), map[string]any{"title": "Pending private title", "body": "Private pending draft must not appear in a content report", "languageTag": "en-IN", "mediaIds": []any{}, "submitForReview": true}, p.Version, "")
	mustStatus(t, edit, 200)
	key := uuid.NewString()
	input := reportInput("POST", p.ID, 1)
	var wg sync.WaitGroup
	ids := make(chan uuid.UUID, 6)
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := owner.request("POST", "content-reports", input, 0, key)
			mustStatus(t, w, 201)
			ids <- parsed[contentReportResult](t, w).ID
		}()
	}
	wg.Wait()
	close(ids)
	var reported uuid.UUID
	for id := range ids {
		if reported != uuid.Nil && reported != id {
			t.Fatal("retry created multiple reports")
		}
		reported = id
	}
	duplicate := submitContentReport(t, owner, "POST", p.ID, 1)
	if duplicate.ID != reported {
		t.Fatal("lifetime target-revision deduplication failed")
	}
	changed := reportInput("POST", p.ID, 1)
	changed["details"] = "Another concern"
	mustStatus(t, owner.request("POST", "content-reports", changed, 0, key), 409)
	mustStatus(t, owner.request("POST", "content-reports", changed, 0, uuid.NewString()), 409)
	w := owner.request("GET", "me/content-reports/"+reported.String(), nil, 0, "")
	mustStatus(t, w, 200)
	if !strings.Contains(w.Body.String(), p.Body) || strings.Contains(w.Body.String(), "Private pending draft") || strings.Contains(w.Body.String(), "Pending private title") {
		t.Fatal("report did not bind to published revision")
	}
	mustStatus(t, writer.request("GET", "me/content-reports/"+reported.String(), nil, 0, ""), 404)
	mustStatus(t, owner.request("GET", "moderation/content-reports", nil, 0, ""), 403)
	ctx := scopedContext(writer, a.DB, vault.Grant{})
	var count int
	if err := a.store(ctx).QueryRow(ctx, "SELECT count(*) FROM social.moderation_case WHERE id=$1", reported).Scan(&count); err != nil || count != 0 {
		t.Fatal("foreign report escaped RLS", err)
	}
	if tag, err := a.store(ctx).Exec(ctx, "UPDATE social.moderation_case SET version=version+1 WHERE id=$1", reported); err != nil || tag.RowsAffected() != 0 {
		t.Fatal("foreign report writable", err)
	}
	deniedSQL(t, a.DB, "UPDATE social.moderation_case SET reporter_ref=$1 WHERE id=$2", contentReporterPrincipal(t, writer), reported)
	deniedSQL(t, a.DB, "UPDATE social.moderation_decision SET reason='changed' WHERE moderation_case_id=$1", reported)
	deniedSQL(t, a.DB, "INSERT INTO social.moderation_case(id,post_id,target_version,reporter_ref,reason_code,grounds,state,created_at) VALUES($1,$2,1,$3,'SPAM','','OPEN',now())", uuid.New(), p.ID, contentReporterPrincipal(t, owner))
	ownerScope := scopedContext(owner, a.DB, vault.Grant{})
	if _, err := a.store(ownerScope).Exec(ownerScope, "INSERT INTO social.moderation_case(id,post_id,target_version,reporter_ref,reason_code,grounds,state) VALUES($1,$2,1,$3,'SPAM','','OPEN')", uuid.New(), p.ID, contentReporterPrincipal(t, writer)); err == nil {
		t.Fatal("forged reporter insert escaped RLS")
	}
	w = mod.request("GET", "moderation/content-reports", nil, 0, "")
	mustStatus(t, w, 200)
	if !strings.Contains(w.Body.String(), reported.String()) || strings.Contains(w.Body.String(), "Private pending draft") || strings.Contains(w.Body.String(), contentReporterPrincipal(t, owner).String()) {
		t.Fatal("moderation report projection leaked identity or draft")
	}
	w = mod.request("GET", "moderation", nil, 0, "")
	mustStatus(t, w, 200)
	if strings.Contains(w.Body.String(), reported.String()) {
		t.Fatal("abuse report entered publication review queue")
	}
	// A request that already captured a moderator actor must recheck the grant
	// once its command transaction begins, rather than relying on that snapshot.
	req := httptest.NewRequest("POST", "/v1/moderation/content-reports/"+reported.String()+"/decisions", strings.NewReader(string(jsonBytes(map[string]any{"action": "DISMISS", "reason": "Queued report decision fixture", "targetRevision": 1}))))
	req.SetPathValue("id", reported.String())
	req.Header.Set("If-Match", `"1"`)
	req.AddCookie(mod.cookie)
	req = a.requestScope(req, uuid.NewString())
	cachedActor, err := a.actor(req)
	if err != nil || cachedActor == nil {
		t.Fatal("moderator fixture unavailable", err)
	}
	modPrincipal := contentReporterPrincipal(t, mod)
	restore := func() {
		integrationAdmin.Exec(context.Background(), "UPDATE identity.platform_grant SET revoked_at=NULL WHERE principal_id=$1 AND role='PLATFORM_MODERATOR'", modPrincipal)
	}
	t.Cleanup(restore)
	if _, err := integrationAdmin.Exec(context.Background(), "UPDATE identity.platform_grant SET revoked_at=now() WHERE principal_id=$1 AND role='PLATFORM_MODERATOR'", modPrincipal); err != nil {
		t.Fatal(err)
	}
	_, _, err = a.contentReportDecision(httptest.NewRecorder(), req, cachedActor)
	var problem *Problem
	if !errors.As(err, &problem) || problem.Status != 403 {
		t.Fatal("queued decision ignored revoked moderator grant", err)
	}
	mustStatus(t, mod.request("GET", "moderation/content-reports", nil, 0, ""), 403)
	modScope := scopedContext(mod, a.DB, vault.Grant{})
	if err := a.store(modScope).QueryRow(modScope, "SELECT count(*) FROM social.moderation_case WHERE id=$1", reported).Scan(&count); err != nil || count != 0 {
		t.Fatal("revoked moderator retained report SQL access", err)
	}
	restore()
	mustStatus(t, mod.request("POST", "moderation/"+reported.String()+"/decisions", map[string]any{"action": "ALLOW", "reason": "Invalid route fixture", "targetRevision": 1}, 1, ""), 404)
	review(t, mod, p.ID, uuid.Nil, 2)
	w = owner.request("GET", "me/content-reports/"+reported.String(), nil, 0, "")
	mustStatus(t, w, 200)
	if v := parsed[contentReportResult](t, w); v.TargetState != "CHANGED" || v.Target != nil {
		t.Fatal("changed revision retained source preview")
	}
	mustStatus(t, mod.request("POST", "moderation/content-reports/"+reported.String()+"/decisions", map[string]any{"action": "REMOVE", "authorReason": "This content violates community policy", "reason": "Obsolete content must not be removed", "targetRevision": 1}, 1, ""), 409)
	decideContentReport(t, mod, duplicate, "DISMISS")
	mustStatus(t, mod.request("POST", "moderation/content-reports/"+reported.String()+"/decisions", map[string]any{"action": "DISMISS", "reason": "Repeated old decision", "targetRevision": 1}, 1, ""), 412)
	w = owner.request("GET", "me/content-reports/"+reported.String(), nil, 0, "")
	mustStatus(t, w, 200)
	if v := parsed[contentReportResult](t, w); v.State != "DECIDED" || v.Decision == nil || v.Decision.Action != "DISMISS" {
		t.Fatal("private outcome not retained")
	}
	mustStatus(t, owner.request("GET", "posts/"+p.ID.String(), nil, 0, ""), 200)
}

func TestContentReportRemovalRechecksSnapshotsAndPreventsRevival(t *testing.T) {
	a := testApp(t)
	author, reporter, mod := login(t, a, 0), login(t, a, 1), login(t, a, 2)
	cleanContentReports(t, reporter)
	cleanContentReports(t, author)
	p := published(t, a, author, mod, "Removal source "+uuid.NewString())
	reported := submitContentReport(t, reporter, "POST", p.ID, 1)
	w := author.request("PATCH", "posts/"+p.ID.String(), map[string]any{"title": "Pending edit", "body": "Pending edit cannot revive removed content", "submitForReview": true}, p.Version, "")
	mustStatus(t, w, 200)
	var pending uuid.UUID
	if err := integrationAdmin.QueryRow(context.Background(), "SELECT id FROM social.moderation_case WHERE post_id=$1 AND target_version=2 AND reporter_ref IS NULL", p.ID).Scan(&pending); err != nil {
		t.Fatal(err)
	}
	decideContentReport(t, mod, reported, "REMOVE")
	var removedVersion int64
	if err := integrationAdmin.QueryRow(context.Background(), "SELECT version FROM social.post WHERE id=$1", p.ID).Scan(&removedVersion); err != nil {
		t.Fatal(err)
	}
	mustStatus(t, author.request("PATCH", "posts/"+p.ID.String(), map[string]any{"title": "Removed post", "body": "No revival through rejected resubmission", "submitForReview": true}, removedVersion, ""), 412)
	mustStatus(t, reporter.request("GET", "posts/"+p.ID.String(), nil, 0, ""), 404)
	mustStatus(t, mod.request("POST", "moderation/"+pending.String()+"/decisions", map[string]any{"action": "ALLOW", "reason": "Cannot revive removed post", "targetRevision": 2}, 1, ""), 409)
	for _, c := range []client{author, reporter} {
		cursor := feedCursor{Viewer: myProfileID(t, c), Query: "HOME||" + uuid.Nil.String(), Expires: time.Now().Add(time.Minute).Unix(), Refs: []feedRef{{Type: "POST", ID: p.ID}}}
		w = c.request("GET", "feed?cursor="+a.encodeCursor(cursor), nil, 0, "")
		mustStatus(t, w, 200)
		if strings.Contains(w.Body.String(), p.ID.String()) {
			t.Fatal("removed post retained in old feed snapshot")
		}
		w = c.request("GET", "search?q="+url.QueryEscape(p.Body), nil, 0, "")
		mustStatus(t, w, 200)
		if strings.Contains(w.Body.String(), p.ID.String()) {
			t.Fatal("removed post remained discoverable")
		}
	}
	thread := published(t, a, author, mod, "Comment removal fixture")
	cid := approvedReply(t, reporter, mod, thread.ID, nil)
	child := approvedReply(t, author, mod, thread.ID, &cid)
	drainActivity(t, a)
	if len(activityFor(t, author, thread.ID).Items) != 1 {
		t.Fatal("reply fixture not delivered")
	}
	commentReport := submitContentReport(t, author, "COMMENT", cid, 1)
	var currentVersion int64
	if err := integrationAdmin.QueryRow(context.Background(), "SELECT version FROM social.comment WHERE id=$1", cid).Scan(&currentVersion); err != nil {
		t.Fatal(err)
	}
	w = reporter.request("PATCH", "comments/"+cid.String(), map[string]any{"body": "Unpublished removed comment edit", "languageTag": "en-IN"}, currentVersion, "")
	mustStatus(t, w, 200)
	if err := integrationAdmin.QueryRow(context.Background(), "SELECT id FROM social.moderation_case WHERE comment_id=$1 AND target_version=2 AND reporter_ref IS NULL", cid).Scan(&pending); err != nil {
		t.Fatal(err)
	}
	decideContentReport(t, mod, commentReport, "REMOVE")
	if err := integrationAdmin.QueryRow(context.Background(), "SELECT version FROM social.comment WHERE id=$1", cid).Scan(&removedVersion); err != nil {
		t.Fatal(err)
	}
	mustStatus(t, reporter.request("PATCH", "comments/"+cid.String(), map[string]any{"body": "No comment revival through resubmission"}, removedVersion, ""), 412)
	w = author.request("GET", "posts/"+thread.ID.String()+"/comments", nil, 0, "")
	mustStatus(t, w, 200)
	if strings.Contains(w.Body.String(), cid.String()+`","postId"`) || !strings.Contains(w.Body.String(), child.String()) || strings.Contains(w.Body.String(), "Unpublished removed comment edit") {
		t.Fatal("comment removal hid descendants or leaked candidate")
	}
	if len(activityFor(t, author, thread.ID).Items) != 0 {
		t.Fatal("removed comment alert retained")
	}
	mustStatus(t, mod.request("POST", "moderation/"+pending.String()+"/decisions", map[string]any{"action": "ALLOW", "reason": "Cannot revive removed comment", "targetRevision": 2}, 1, ""), 409)
	mustStatus(t, author.request("POST", "posts/"+thread.ID.String()+"/comments", CommentInput{Body: "Reply to removed source", ParentID: &cid}, 0, uuid.NewString()), 422)
}

func TestContentReportValidationUnavailableTargetsAndQuota(t *testing.T) {
	a := testApp(t)
	owner, writer, mod := login(t, a, 0), login(t, a, 1), login(t, a, 2)
	cleanContentReports(t, owner)
	p := published(t, a, writer, mod, "Validation report fixture")
	mustStatus(t, client{app: a}.request("POST", "content-reports", reportInput("POST", p.ID, 1), 0, uuid.NewString()), 401)
	mustStatus(t, owner.request("POST", "content-reports", map[string]any{"targetType": "POST", "targetId": p.ID, "reasonCode": "SPAM"}, 0, uuid.NewString()), 422)
	mustStatus(t, writer.request("POST", "content-reports", reportInput("POST", p.ID, 1), 0, uuid.NewString()), 422)
	for _, change := range []map[string]any{{"targetType": "PROFILE"}, {"targetId": uuid.Nil}, {"reasonCode": "BAD"}, {"reasonCode": "OTHER", "details": ""}, {"details": strings.Repeat("界", 1001)}} {
		b := reportInput("POST", p.ID, 1)
		for k, v := range change {
			b[k] = v
		}
		mustStatus(t, owner.request("POST", "content-reports", b, 0, uuid.NewString()), 422)
	}
	flag(t, writer, "blocks", myProfileID(t, owner), true)
	t.Cleanup(func() { flag(t, writer, "blocks", myProfileID(t, owner), false) })
	mustStatus(t, owner.request("POST", "content-reports", reportInput("POST", p.ID, 1), 0, uuid.NewString()), 404)
	flag(t, writer, "blocks", myProfileID(t, owner), false)
	report := submitContentReport(t, owner, "POST", p.ID, 1)
	mustStatus(t, writer.request("DELETE", "posts/"+p.ID.String(), nil, p.Version, ""), 204)
	w := owner.request("GET", "me/content-reports/"+report.ID.String(), nil, 0, "")
	mustStatus(t, w, 200)
	if v := parsed[contentReportResult](t, w); v.Target != nil || v.TargetState != "UNAVAILABLE" {
		t.Fatal("deleted source retained report preview")
	}
	decideContentReport(t, mod, report, "DISMISS")
	var first testPost
	for i := 0; i < 9; i++ {
		first = published(t, a, writer, mod, "Quota fixture "+uuid.NewString())
		submitContentReport(t, owner, "POST", first.ID, 1)
	}
	submitContentReport(t, owner, "POST", first.ID, 1) // Identical retries remain possible at the quota.
	next := published(t, a, writer, mod, "Over quota fixture")
	mustStatus(t, owner.request("POST", "content-reports", reportInput("POST", next.ID, 1), 0, uuid.NewString()), 429)
}

func TestContentReportPaginationCursorAndModeratorConflict(t *testing.T) {
	a := testApp(t)
	owner, writer, mod := login(t, a, 0), login(t, a, 1), login(t, a, 2)
	cleanContentReports(t, owner)
	principal := contentReporterPrincipal(t, owner)
	for i := 0; i < 25; i++ {
		p := published(t, a, writer, mod, "Report page fixture "+uuid.NewString())
		if _, err := integrationAdmin.Exec(context.Background(), "INSERT INTO social.moderation_case(id,post_id,target_version,reporter_ref,reason_code,grounds,state) VALUES($1,$2,1,$3,'SPAM','Fictional page fixture','OPEN')", uuid.New(), p.ID, principal); err != nil {
			t.Fatal(err)
		}
	}
	type page struct {
		Items      []contentReportResult
		NextCursor *string
	}
	w := owner.request("GET", "me/content-reports", nil, 0, "")
	mustStatus(t, w, 200)
	first := parsed[page](t, w)
	if len(first.Items) != 20 || first.NextCursor == nil {
		t.Fatal("report page cap or continuation missing")
	}
	mustStatus(t, writer.request("GET", "me/content-reports?cursor="+*first.NextCursor, nil, 0, ""), 422)
	w = owner.request("GET", "me/content-reports?cursor="+*first.NextCursor, nil, 0, "")
	mustStatus(t, w, 200)
	if next := parsed[page](t, w); len(next.Items) != 5 || next.NextCursor != nil {
		t.Fatal("report pagination lost rows")
	}
	c, err := a.profilePageCursor(*first.NextCursor, "content-reports", myProfileID(t, owner), myProfileID(t, owner))
	if err != nil {
		t.Fatal(err)
	}
	c.Expires = time.Now().Add(-time.Second).Unix()
	mustStatus(t, owner.request("GET", "me/content-reports?cursor="+a.encodeProfileCursor(c), nil, 0, ""), 410)
	w = mod.request("GET", "moderation/content-reports", nil, 0, "")
	mustStatus(t, w, 200)
	queue := parsed[page](t, w)
	if len(queue.Items) != 20 || queue.NextCursor == nil {
		t.Fatal("staff report queue is capped without pagination")
	}
	mustStatus(t, mod.request("GET", "me/content-reports?cursor="+*queue.NextCursor, nil, 0, ""), 422)
	if _, err := integrationAdmin.Exec(context.Background(), "UPDATE social.moderation_case SET created_at=now()-interval '2 hours' WHERE reporter_ref=$1", principal); err != nil {
		t.Fatal(err)
	}
	p := published(t, a, mod, mod, "Conflict of interest report fixture")
	conflict := submitContentReport(t, owner, "POST", p.ID, 1)
	mustStatus(t, mod.request("POST", "moderation/content-reports/"+conflict.ID.String()+"/decisions", map[string]any{"action": "DISMISS", "reason": "Cannot decide own content report", "targetRevision": 1}, 1, ""), 403)
}

func TestContentReportRequiresIndependentReviewer(t *testing.T) {
	a := testApp(t)
	reviewer, writer, reporter := login(t, a, 0), login(t, a, 1), login(t, a, 2)
	cleanContentReports(t, reporter)
	cleanContentReports(t, writer)
	p := published(t, a, writer, reporter, "Independent content reviewer fixture")
	report := submitContentReport(t, reporter, "POST", p.ID, 1)
	mustStatus(t, reporter.request("GET", "me/content-reports/"+report.ID.String(), nil, 0, ""), 200)
	for _, action := range []string{"DISMISS", "REMOVE"} {
		mustStatus(t, reporter.request("POST", "moderation/content-reports/"+report.ID.String()+"/decisions", map[string]any{"action": action, "authorReason": "This content violates community policy", "reason": "Reporter cannot decide their own complaint", "targetRevision": 1}, 1, ""), 403)
	}
	w := reporter.request("GET", "moderation/content-reports", nil, 0, "")
	mustStatus(t, w, 200)
	if strings.Contains(w.Body.String(), report.ID.String()) {
		t.Fatal("reporter can review their own complaint")
	}
	denyDecision := func(c client, reportID uuid.UUID) {
		t.Helper()
		ctx := scopedContext(c, a.DB, vault.Grant{})
		_, err := a.store(ctx).Exec(ctx, "INSERT INTO social.moderation_decision(id,moderation_case_id,sequence,action,rule_version,actor_ref,reason) VALUES($1,$2,1,'REMOVE','local-content-report-v1',$3,'Self-review must be denied')", uuid.New(), reportID, contentReporterPrincipal(t, c))
		if err == nil || !strings.Contains(err.Error(), "row-level security") {
			t.Fatal("self-review not rejected by decision RLS", err)
		}
	}
	denyDecision(reporter, report.ID)
	grantID := uuid.New()
	if _, err := integrationAdmin.Exec(context.Background(), "INSERT INTO identity.platform_grant(id,principal_id,role,valid_to) VALUES($1,$2,'PLATFORM_MODERATOR',now()+interval '1 day')", grantID, contentReporterPrincipal(t, reviewer)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		integrationAdmin.Exec(context.Background(), "DELETE FROM identity.platform_grant WHERE id=$1", grantID)
	})
	w = reviewer.request("GET", "moderation/content-reports", nil, 0, "")
	mustStatus(t, w, 200)
	if !strings.Contains(w.Body.String(), report.ID.String()) {
		t.Fatal("independent reviewer cannot see eligible report")
	}
	mustStatus(t, reviewer.request("GET", "me/content-reports/"+report.ID.String(), nil, 0, ""), 404)
	decideContentReport(t, reviewer, report, "DISMISS")
	w = reporter.request("GET", "me/content-reports/"+report.ID.String(), nil, 0, "")
	mustStatus(t, w, 200)
	if receipt := parsed[contentReportResult](t, w); receipt.Decision == nil || receipt.Decision.Action != "DISMISS" {
		t.Fatal("independent decision missing from reporter receipt")
	}
	owned := published(t, a, reviewer, reporter, "Moderator-authored conflict fixture")
	authorConflict := submitContentReport(t, writer, "POST", owned.ID, 1)
	mustStatus(t, reviewer.request("POST", "moderation/content-reports/"+authorConflict.ID.String()+"/decisions", map[string]any{"action": "DISMISS", "reason": "Author cannot decide complaints about their content", "targetRevision": 1}, 1, ""), 403)
	denyDecision(reviewer, authorConflict.ID)
	w = reviewer.request("GET", "moderation/content-reports", nil, 0, "")
	mustStatus(t, w, 200)
	if strings.Contains(w.Body.String(), authorConflict.ID.String()) {
		t.Fatal("target author can review the complaint")
	}
	decideContentReport(t, reporter, authorConflict, "DISMISS")
}

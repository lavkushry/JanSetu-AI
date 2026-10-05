package app

import (
	"context"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lavkushry/JanSetu-AI/services/backend/internal/vault"
)

type authorDecision struct {
	ID                          uuid.UUID
	Action, Reason, RuleVersion string
	Target                      struct {
		Type       string
		ID, PostID uuid.UUID
		Revision   int64
	}
}
type authorDecisionPage struct {
	Items      []authorDecision
	NextCursor *string
}

func restrictInitial(t *testing.T, mod client, post, comment uuid.UUID) uuid.UUID {
	t.Helper()
	var caseID, decisionID uuid.UUID
	if err := integrationAdmin.QueryRow(context.Background(), "SELECT id FROM social.moderation_case WHERE (post_id=$1 OR comment_id=$2) AND target_version=1 AND reporter_ref IS NULL", post, comment).Scan(&caseID); err != nil {
		t.Fatal(err)
	}
	input := map[string]any{"action": "RESTRICT", "reason": "Internal review marker must stay private", "targetRevision": 1}
	path := "moderation/" + caseID.String() + "/decisions"
	mustStatus(t, mod.request("POST", path, input, 1, ""), 422)
	input["authorReason"] = strings.Repeat("x", 1001)
	mustStatus(t, mod.request("POST", path, input, 1, ""), 422)
	input["authorReason"] = "😀😀😀"
	mustStatus(t, mod.request("POST", path, input, 1, ""), 422)
	input["authorReason"] = "Please remove the private contact details before resubmitting."
	mustStatus(t, mod.request("POST", path, input, 1, ""), 200)
	if err := integrationAdmin.QueryRow(context.Background(), "SELECT id FROM social.moderation_decision WHERE moderation_case_id=$1", caseID).Scan(&decisionID); err != nil {
		t.Fatal(err)
	}
	return decisionID
}
func initialPost(t *testing.T, owner client) testPost {
	t.Helper()
	w := owner.request("POST", "posts", PostInput{Kind: "SHORT", Body: "Initial privately rejected fixture " + uuid.NewString(), SubmitForReview: true}, 0, uuid.NewString())
	mustStatus(t, w, 201)
	return parsed[testPost](t, w)
}
func TestAuthorDecisionsPrivacyOwnershipAndSafePostResubmission(t *testing.T) {
	a := testApp(t)
	owner, other, mod := login(t, a, 0), login(t, a, 1), login(t, a, 2)
	p := initialPost(t, owner)
	d := restrictInitial(t, mod, p.ID, uuid.Nil)
	path := "me/moderation-decisions/" + d.String()
	w := owner.request("GET", path, nil, 0, "")
	mustStatus(t, w, 200)
	got := parsed[authorDecision](t, w)
	if got.Action != "RESTRICT" || got.Target.ID != p.ID || got.Target.Revision != 1 || !strings.Contains(got.Reason, "contact details") {
		t.Fatal("incorrect author decision", w.Body)
	}
	for _, marker := range []string{"Internal review marker", contentReporterPrincipal(t, mod).String(), "Initial privately rejected fixture", "actorRef", "grounds", "reporterRef"} {
		if strings.Contains(w.Body.String(), marker) {
			t.Fatal("private fields leaked", marker)
		}
	}
	for _, c := range []client{other, mod} {
		mustStatus(t, c.request("GET", path, nil, 0, ""), 404)
	}
	mustStatus(t, client{app: a}.request("GET", "me/moderation-decisions", nil, 0, ""), 401)
	mustStatus(t, client{app: a}.request("GET", path, nil, 0, ""), 401)
	ctx := scopedContext(owner, a.DB, vault.Grant{})
	var n int
	if err := a.store(ctx).QueryRow(ctx, "SELECT count(*) FROM social.moderation_decision WHERE id=$1", d).Scan(&n); err != nil || n != 0 {
		t.Fatal("author gained internal decision access", err)
	}
	if err := a.store(ctx).QueryRow(ctx, "SELECT count(*) FROM social.author_moderation_decision WHERE id=$1", d).Scan(&n); err != nil || n != 1 {
		t.Fatal("author projection inaccessible", err)
	}
	deniedSQL(t, a.DB, "UPDATE social.moderation_decision SET author_reason='Forged reason' WHERE id=$1", d)
	w = owner.request("GET", "posts/"+p.ID.String(), nil, 0, "")
	mustStatus(t, w, 200)
	current := parsed[struct {
		testPost
		Viewer    struct{ CanEdit bool }
		Candidate struct{ ReviewState string }
	}](t, w)
	if current.State != "HIDDEN" || !current.Viewer.CanEdit || current.Candidate.ReviewState != "REJECTED" {
		t.Fatal("initial rejection cannot be corrected", w.Body)
	}
	mustStatus(t, other.request("GET", "posts/"+p.ID.String(), nil, 0, ""), 404)
	input := map[string]any{"title": "Corrected post", "body": "Corrected initial post for a fresh review", "submitForReview": true}
	mustStatus(t, other.request("PATCH", "posts/"+p.ID.String(), input, current.Version, ""), 403)
	mustStatus(t, owner.request("PATCH", "posts/"+p.ID.String(), input, p.Version, ""), 412)
	w = owner.request("PATCH", "posts/"+p.ID.String(), input, current.Version, "")
	mustStatus(t, w, 200)
	next := parsed[testPost](t, w)
	if next.State != "PENDING" || next.CurrentRevision != 2 {
		t.Fatal("resubmission not fresh pending revision", w.Body)
	}
	mustStatus(t, other.request("GET", "posts/"+p.ID.String(), nil, 0, ""), 404)
	review(t, mod, p.ID, uuid.Nil, 2)
	w = owner.request("GET", "me/moderation-decisions", nil, 0, "")
	mustStatus(t, w, 200)
	approved := false
	for _, notice := range parsed[authorDecisionPage](t, w).Items {
		if notice.Target.ID == p.ID && notice.Target.Revision == 2 && notice.Action == "ALLOW" {
			approved = notice.Reason == "This revision was approved for publication."
		}
	}
	if !approved {
		t.Fatal("approval notice did not use a safe fixed reason", w.Body)
	}
	w = other.request("GET", "posts/"+p.ID.String(), nil, 0, "")
	mustStatus(t, w, 200)
	if !strings.Contains(w.Body.String(), "Corrected initial post") || strings.Contains(w.Body.String(), "privately rejected fixture") {
		t.Fatal("wrong revision published", w.Body)
	}
	var rejected string
	if err := integrationAdmin.QueryRow(context.Background(), "SELECT review_state FROM social.post_revision WHERE post_id=$1 AND revision=1", p.ID).Scan(&rejected); err != nil || rejected != "REJECTED" {
		t.Fatal("prior rejection changed", err)
	}
	currentPost := parsed[testPost](t, w)
	mustStatus(t, owner.request("DELETE", "posts/"+p.ID.String(), nil, currentPost.Version, ""), 204)
	mustStatus(t, owner.request("GET", path, nil, 0, ""), 200)
	// A revoked secret session cannot select records through the definer view.
	if _, err := integrationAdmin.Exec(context.Background(), "UPDATE identity.session SET revoked_at=now() WHERE token_hash=$1", tokenHash(owner.cookie.Value)); err != nil {
		t.Fatal(err)
	}
	if err := a.store(ctx).QueryRow(ctx, "SELECT count(*) FROM social.author_moderation_decision").Scan(&n); err != nil || n != 0 {
		t.Fatal("revoked session retained decisions", err)
	}
	mustStatus(t, owner.request("GET", path, nil, 0, ""), 401)
}
func TestRejectedInitialCommentResubmissionAndLiveParentRules(t *testing.T) {
	a := testApp(t)
	owner, writer, mod := login(t, a, 0), login(t, a, 1), login(t, a, 2)
	p := published(t, a, owner, mod, "Initial comment correction thread")
	create := func(parent *uuid.UUID) uuid.UUID {
		w := writer.request("POST", "posts/"+p.ID.String()+"/comments", CommentInput{Body: "Initial private rejected comment", ParentID: parent}, 0, uuid.NewString())
		mustStatus(t, w, 201)
		cid := parsed[struct{ ID uuid.UUID }](t, w).ID
		restrictInitial(t, mod, uuid.Nil, cid)
		return cid
	}
	cid := create(nil)
	path := "comments/" + cid.String()
	w := writer.request("PATCH", path, map[string]any{"body": "Corrected reply awaiting fresh approval"}, 2, "")
	mustStatus(t, w, 200)
	if parsed[struct{ CurrentRevision int64 }](t, w).CurrentRevision != 2 {
		t.Fatal("comment revision did not advance")
	}
	w = owner.request("GET", "posts/"+p.ID.String()+"/comments", nil, 0, "")
	mustStatus(t, w, 200)
	if strings.Contains(w.Body.String(), cid.String()) {
		t.Fatal("pending reply public")
	}
	review(t, mod, uuid.Nil, cid, 2)
	w = owner.request("GET", "posts/"+p.ID.String()+"/comments", nil, 0, "")
	mustStatus(t, w, 200)
	if !strings.Contains(w.Body.String(), "Corrected reply awaiting") || strings.Contains(w.Body.String(), "Initial private rejected") {
		t.Fatal("incorrect reply published")
	}
	// Resubmitting into a community uses current membership policy.
	var community uuid.UUID
	if err := integrationAdmin.QueryRow(context.Background(), "SELECT community_id FROM social.post WHERE id=$1", p.ID).Scan(&community); err != nil {
		t.Fatal(err)
	}
	pending := create(nil)
	writerID := myProfileID(t, writer)
	if _, err := integrationAdmin.Exec(context.Background(), "UPDATE social.community_member SET state='BANNED' WHERE community_id=$1 AND profile_id=$2", community, writerID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		integrationAdmin.Exec(context.Background(), "UPDATE social.community_member SET state='ACTIVE' WHERE community_id=$1 AND profile_id=$2", community, writerID)
	})
	mustStatus(t, writer.request("PATCH", "comments/"+pending.String(), map[string]any{"body": "Banned resubmission"}, 2, ""), 403)
	if _, err := integrationAdmin.Exec(context.Background(), "UPDATE social.community_member SET state='ACTIVE' WHERE community_id=$1 AND profile_id=$2", community, writerID); err != nil {
		t.Fatal(err)
	}
	parent := approvedReply(t, owner, mod, p.ID, nil)
	child := create(&parent)
	mustStatus(t, owner.request("DELETE", "comments/"+parent.String(), nil, 2, ""), 204)
	mustStatus(t, writer.request("PATCH", "comments/"+child.String(), map[string]any{"body": "Cannot resubmit to a deleted parent"}, 2, ""), 403)
	mustStatus(t, owner.request("DELETE", "posts/"+p.ID.String(), nil, p.Version, ""), 204)
	mustStatus(t, writer.request("PATCH", "comments/"+pending.String(), map[string]any{"body": "Cannot resubmit under a deleted thread"}, 2, ""), 403)
}
func TestAuthorDecisionPaginationLegacyFallbackAndReportIsolation(t *testing.T) {
	a := testApp(t)
	owner, other, mod := login(t, a, 0), login(t, a, 1), login(t, a, 2)
	p := published(t, a, owner, mod, "Private decision pagination source")
	at := time.Now().Add(time.Hour).UTC().Truncate(time.Microsecond)
	ids := map[uuid.UUID]bool{}
	for i := 0; i < 23; i++ {
		mid, did := uuid.New(), uuid.New()
		ids[did] = false
		if _, err := integrationAdmin.Exec(context.Background(), "INSERT INTO social.moderation_case(id,post_id,target_version,reason_code,grounds,state) VALUES($1,$2,$3,'PUBLICATION_REVIEW','Internal complaint marker','DECIDED')", mid, p.ID, i+10); err != nil {
			t.Fatal(err)
		}
		if _, err := integrationAdmin.Exec(context.Background(), "INSERT INTO social.moderation_decision(id,moderation_case_id,sequence,action,rule_version,actor_ref,reason,decided_at) VALUES($1,$2,1,'RESTRICT','legacy-test',$3,'Private internal reason marker',$4)", did, mid, contentReporterPrincipal(t, mod), at); err != nil {
			t.Fatal(err)
		}
	}
	path := "me/moderation-decisions"
	w := owner.request("GET", path, nil, 0, "")
	mustStatus(t, w, 200)
	first := parsed[authorDecisionPage](t, w)
	if len(first.Items) != 20 || first.NextCursor == nil {
		t.Fatal("missing decision page", w.Body)
	}
	cursor := url.QueryEscape(*first.NextCursor)
	mustStatus(t, other.request("GET", path+"?cursor="+cursor, nil, 0, ""), 422)
	mustStatus(t, owner.request("GET", "me/content-reports?cursor="+cursor, nil, 0, ""), 422)
	mustStatus(t, owner.request("GET", path+"?cursor="+cursor+"x", nil, 0, ""), 422)
	c, err := a.profilePageCursor(*first.NextCursor, "moderation-decisions", myProfileID(t, owner), myProfileID(t, owner))
	if err != nil {
		t.Fatal(err)
	}
	c.Expires = time.Now().Add(-time.Minute).Unix()
	mustStatus(t, owner.request("GET", path+"?cursor="+url.QueryEscape(a.encodeProfileCursor(c)), nil, 0, ""), 410)
	for page := first; ; {
		for _, d := range page.Items {
			if seen, ok := ids[d.ID]; ok {
				if seen {
					t.Fatal("duplicate keyset decision")
				}
				ids[d.ID] = true
				if !strings.Contains(d.Reason, "No author-facing reason") {
					t.Fatal("unsafe legacy fallback", d)
				}
			}
		}
		if page.NextCursor == nil {
			break
		}
		w = owner.request("GET", path+"?cursor="+url.QueryEscape(*page.NextCursor), nil, 0, "")
		mustStatus(t, w, 200)
		if strings.Contains(w.Body.String(), "Private internal") || strings.Contains(w.Body.String(), p.Body) {
			t.Fatal("private history leaked internal note or source")
		}
		page = parsed[authorDecisionPage](t, w)
	}
	for id, seen := range ids {
		if !seen {
			t.Fatal("omitted decision", id)
		}
	}
	cleanContentReports(t, other)
	report := submitContentReport(t, other, "POST", p.ID, 1)
	decideContentReport(t, mod, report, "DISMISS")
	var dismissed uuid.UUID
	if err := integrationAdmin.QueryRow(context.Background(), "SELECT id FROM social.moderation_decision WHERE moderation_case_id=$1", report.ID).Scan(&dismissed); err != nil {
		t.Fatal(err)
	}
	mustStatus(t, owner.request("GET", path+"/"+dismissed.String(), nil, 0, ""), 404)
	// A separate published target is removed with private and shared notes separated.
	removed := published(t, a, owner, mod, "Removed decision source "+uuid.NewString())
	report = submitContentReport(t, other, "POST", removed.ID, 1)
	input := map[string]any{"action": "REMOVE", "reason": "Private reporter contact marker", "targetRevision": 1}
	decisionPath := "moderation/content-reports/" + report.ID.String() + "/decisions"
	mustStatus(t, mod.request("POST", decisionPath, input, 1, ""), 422)
	input["authorReason"] = "Please follow the community rule on private information."
	mustStatus(t, mod.request("POST", decisionPath, input, 1, ""), 200)
	var did uuid.UUID
	if err := integrationAdmin.QueryRow(context.Background(), "SELECT id FROM social.moderation_decision WHERE moderation_case_id=$1", report.ID).Scan(&did); err != nil {
		t.Fatal(err)
	}
	w = owner.request("GET", path+"/"+did.String(), nil, 0, "")
	mustStatus(t, w, 200)
	if strings.Contains(w.Body.String(), "Private reporter") || strings.Contains(w.Body.String(), "Fictional private report") || !strings.Contains(w.Body.String(), "community rule") {
		t.Fatal("unsafe removal decision", w.Body)
	}
	ctx := scopedContext(owner, a.DB, vault.Grant{})
	var n int
	if err := a.store(ctx).QueryRow(ctx, "SELECT count(*) FROM social.moderation_case WHERE id=$1", report.ID).Scan(&n); err != nil || n != 0 {
		t.Fatal("author sees reporter complaint", err)
	}
	// Removal of any previously published target cannot be undone by editing.
	var version int64
	if err := integrationAdmin.QueryRow(context.Background(), "SELECT version FROM social.post WHERE id=$1", removed.ID).Scan(&version); err != nil {
		t.Fatal(err)
	}
	mustStatus(t, owner.request("PATCH", "posts/"+removed.ID.String(), map[string]any{"title": "Removed post", "body": "Forbidden revival", "submitForReview": true}, version, ""), 412)
}

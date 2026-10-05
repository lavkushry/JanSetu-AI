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

type appealResponse struct {
	ID, DecisionID         uuid.UUID
	State, Grounds         string
	Version, TargetVersion int64
	AssignedToMe           bool
	RestorationReason      string
	Outcome                *struct{ Result, Reason, RestorationState, RestorationReason string }
	Original               struct {
		Target         struct{ Revision int64 }
		InternalReason string
	}
	Preview *struct{ Body string }
}

func newAppeal(t *testing.T, owner client, d uuid.UUID) appealResponse {
	t.Helper()
	w := owner.request("POST", "moderation/decisions/"+d.String()+"/appeals", map[string]any{"grounds": "Please independently review this fictional decision."}, 0, uuid.NewString())
	mustStatus(t, w, 201)
	v := parsed[appealResponse](t, w)
	t.Cleanup(func() {
		integrationAdmin.Exec(context.Background(), "DELETE FROM social.appeal_decision WHERE appeal_id=$1", v.ID)
		integrationAdmin.Exec(context.Background(), "DELETE FROM social.appeal WHERE id=$1", v.ID)
	})
	return v
}
func appealContext(t *testing.T, reviewer client, v appealResponse) appealResponse {
	t.Helper()
	w := reviewer.request("GET", "moderation/appeals/"+v.ID.String(), nil, 0, "")
	mustStatus(t, w, 200)
	return parsed[appealResponse](t, w)
}
func takeAppeal(t *testing.T, reviewer client, v appealResponse) appealResponse {
	t.Helper()
	mustStatus(t, reviewer.request("POST", "moderation/appeals/"+v.ID.String()+"/claim", nil, v.Version, ""), 200)
	return appealContext(t, reviewer, v)
}
func appealInput(v appealResponse, result string) map[string]any {
	return map[string]any{"result": result, "authorReason": "Independent review confirms this fictional appeal outcome.", "targetRevision": v.Original.Target.Revision, "targetVersion": v.TargetVersion}
}
func decideAppealTest(t *testing.T, reviewer client, v appealResponse, result string) appealResponse {
	t.Helper()
	w := reviewer.request("POST", "moderation/appeals/"+v.ID.String()+"/decisions", appealInput(v, result), v.Version, "")
	mustStatus(t, w, 200)
	return parsed[appealResponse](t, w)
}
func removedDecision(t *testing.T, mod, reporter client, kind string, target uuid.UUID) uuid.UUID {
	t.Helper()
	report := submitContentReport(t, reporter, kind, target, 1)
	decideContentReport(t, mod, report, "REMOVE")
	var did uuid.UUID
	if e := integrationAdmin.QueryRow(context.Background(), "SELECT id FROM social.moderation_decision WHERE moderation_case_id=$1", report.ID).Scan(&did); e != nil {
		t.Fatal(e)
	}
	return did
}
func TestAppealsIndependencePrivacyRetriesAndExactPostRestoration(t *testing.T) {
	a := testApp(t)
	owner, other, original, reviewer := login(t, a, 0), login(t, a, 1), login(t, a, 2), login(t, a, 4)
	p := initialPost(t, owner)
	did := restrictInitial(t, original, p.ID, uuid.Nil)
	path := "moderation/decisions/" + did.String() + "/appeals"
	for _, c := range []client{other, original} {
		mustStatus(t, c.request("POST", path, map[string]any{"grounds": "Independent review requested"}, 0, uuid.NewString()), 404)
	}
	mustStatus(t, client{app: a}.request("POST", path, map[string]any{"grounds": "Independent review requested"}, 0, uuid.NewString()), 401)
	for _, grounds := range []string{"😀😀😀", strings.Repeat("x", 1001), "    "} {
		mustStatus(t, owner.request("POST", path, map[string]any{"grounds": grounds}, 0, uuid.NewString()), 422)
	}
	mustStatus(t, owner.request("POST", path, map[string]any{"grounds": "Missing creation key"}, 0, ""), 422)
	v := newAppeal(t, owner, did)
	retry := owner.request("POST", path, map[string]any{"grounds": v.Grounds}, 0, uuid.NewString())
	mustStatus(t, retry, 201)
	if parsed[appealResponse](t, retry).ID != v.ID {
		t.Fatal("duplicate appeal")
	}
	mustStatus(t, owner.request("POST", path, map[string]any{"grounds": "Different subsequent grounds"}, 0, uuid.NewString()), 409)
	for _, c := range []client{other, original, reviewer} {
		mustStatus(t, c.request("GET", "me/appeals/"+v.ID.String(), nil, 0, ""), 404)
	}
	for _, c := range []client{original} {
		mustStatus(t, c.request("GET", "moderation/appeals/"+v.ID.String(), nil, 0, ""), 404)
		mustStatus(t, c.request("POST", "moderation/appeals/"+v.ID.String()+"/claim", nil, 1, ""), 404)
	}
	mustStatus(t, owner.request("GET", "moderation/appeals", nil, 0, ""), 403)
	w := owner.request("GET", "me/appeals/"+v.ID.String(), nil, 0, "")
	mustStatus(t, w, 200)
	for _, secret := range []string{"Internal review marker", "Initial privately rejected fixture", contentReporterPrincipal(t, reviewer).String(), "reviewerRef", "actorRef", "internalReason"} {
		if strings.Contains(w.Body.String(), secret) {
			t.Fatal("private receipt leaked", secret)
		}
	}
	v = appealContext(t, reviewer, v)
	if v.Preview == nil || !strings.Contains(v.Preview.Body, "Initial privately rejected fixture") || !strings.Contains(v.Original.InternalReason, "Internal review") {
		t.Fatal("missing staff context")
	}
	mustStatus(t, reviewer.request("POST", "moderation/appeals/"+v.ID.String()+"/decisions", appealInput(v, "REVERSED"), 1, ""), 412)
	v = takeAppeal(t, reviewer, v)
	mustStatus(t, reviewer.request("POST", "moderation/appeals/"+v.ID.String()+"/claim", nil, 1, ""), 412)
	if !v.AssignedToMe || v.Version != 2 {
		t.Fatal("claim not recorded")
	}
	result := decideAppealTest(t, reviewer, v, "REVERSED")
	if result.State != "REVERSED" || result.Outcome == nil || result.Outcome.RestorationState != "RESTORED" || result.Version != 3 {
		t.Fatal("incorrect restoration", result)
	}
	w = other.request("GET", "posts/"+p.ID.String(), nil, 0, "")
	mustStatus(t, w, 200)
	if !strings.Contains(w.Body.String(), "Initial privately rejected fixture") {
		t.Fatal("exact original absent")
	}
	mustStatus(t, reviewer.request("POST", "moderation/appeals/"+v.ID.String()+"/decisions", appealInput(v, "UPHELD"), 3, ""), 404)
	deniedSQL(t, a.DB, "UPDATE social.appeal_decision SET author_reason='Forged reason' WHERE appeal_id=$1", v.ID)
	deniedSQL(t, a.DB, "DELETE FROM social.appeal_decision WHERE appeal_id=$1", v.ID)
	// Owners cannot forge reviewer assignments, outcomes or another author's appeal.
	ctx := scopedContext(owner, a.DB, vault.Grant{})
	if _, e := a.store(ctx).Exec(ctx, "INSERT INTO social.appeal_decision(id,appeal_id,result,author_reason,restoration_state,restoration_reason,reviewer_ref) VALUES($1,$2,'UPHELD','Forged result','UNCHANGED','NONE',$3)", uuid.New(), v.ID, contentReporterPrincipal(t, owner)); e == nil {
		t.Fatal("owner forged outcome")
	}
	var n int
	if e := a.store(scopedContext(original, a.DB, vault.Grant{})).QueryRow(scopedContext(original, a.DB, vault.Grant{}), "SELECT count(*) FROM social.appeal WHERE id=$1", v.ID).Scan(&n); e != nil || n != 0 {
		t.Fatal("original moderator read own appeal", e)
	}
	if _, e := integrationAdmin.Exec(context.Background(), "UPDATE social.appeal SET state='REVIEWING',reviewer_ref=$1,version=version+1 WHERE id=$2", contentReporterPrincipal(t, original), v.ID); e == nil {
		t.Fatal("independence trigger bypass")
	}
}
func TestAppealUpheldAndLaterRevisionCannotBePublished(t *testing.T) {
	a := testApp(t)
	owner, other, original, reviewer := login(t, a, 0), login(t, a, 1), login(t, a, 2), login(t, a, 4)
	for _, changed := range []bool{false, true} {
		p := initialPost(t, owner)
		did := restrictInitial(t, original, p.ID, uuid.Nil)
		v := takeAppeal(t, reviewer, newAppeal(t, owner, did))
		result := "UPHELD"
		if changed {
			w := owner.request("PATCH", "posts/"+p.ID.String(), map[string]any{"title": "Later candidate", "body": "Later private candidate must never be published by the appeal", "submitForReview": true}, 2, "")
			mustStatus(t, w, 200)
			mustStatus(t, reviewer.request("POST", "moderation/appeals/"+v.ID.String()+"/decisions", appealInput(v, "REVERSED"), v.Version, ""), 409)
			v = appealContext(t, reviewer, v)
			if v.RestorationReason != "TARGET_CHANGED" {
				t.Fatal("changed revision is restorable")
			}
			result = "REVERSED"
		}
		got := decideAppealTest(t, reviewer, v, result)
		if got.Outcome == nil {
			t.Fatal("no appeal outcome")
		}
		if changed && got.Outcome.RestorationReason != "TARGET_CHANGED" {
			t.Fatal("false restoration")
		}
		if !changed && got.Outcome.RestorationState != "UNCHANGED" {
			t.Fatal("upheld changed visibility")
		}
		mustStatus(t, other.request("GET", "posts/"+p.ID.String(), nil, 0, ""), 404)
	}
}
func TestAppealRemovalAndCommentRestoration(t *testing.T) {
	a := testApp(t)
	owner, reporter, original, reviewer := login(t, a, 0), login(t, a, 1), login(t, a, 2), login(t, a, 4)
	cleanContentReports(t, reporter)
	p := published(t, a, owner, original, "Independent restoration thread "+uuid.NewString())
	// Previously published removals restore their exact approved revision.
	did := removedDecision(t, original, reporter, "POST", p.ID)
	v := takeAppeal(t, reviewer, newAppeal(t, owner, did))
	got := decideAppealTest(t, reviewer, v, "REVERSED")
	if got.Outcome.RestorationState != "RESTORED" {
		t.Fatal("removed post not restored")
	}
	mustStatus(t, reporter.request("GET", "posts/"+p.ID.String(), nil, 0, ""), 200)
	cid := approvedReply(t, owner, original, p.ID, nil)
	did = removedDecision(t, original, reporter, "COMMENT", cid)
	v = takeAppeal(t, reviewer, newAppeal(t, owner, did))
	got = decideAppealTest(t, reviewer, v, "REVERSED")
	if got.Outcome.RestorationState != "RESTORED" {
		t.Fatal("removed reply not restored")
	}
	// Initial rejected comments publish once and enqueue the existing reply activity.
	w := reporter.request("POST", "posts/"+p.ID.String()+"/comments", CommentInput{Body: "Initial appeal restored reply"}, 0, uuid.NewString())
	mustStatus(t, w, 201)
	cid = parsed[struct{ ID uuid.UUID }](t, w).ID
	did = restrictInitial(t, original, uuid.Nil, cid)
	v = takeAppeal(t, reviewer, newAppeal(t, reporter, did))
	got = decideAppealTest(t, reviewer, v, "REVERSED")
	if got.Outcome.RestorationState != "RESTORED" {
		t.Fatal("initial reply not restored")
	}
	var events int
	if e := integrationAdmin.QueryRow(context.Background(), "SELECT count(*) FROM infra.outbox WHERE event_type='CommentPublished' AND payload->>'commentId'=$1", cid.String()).Scan(&events); e != nil || events != 1 {
		t.Fatal("reply publication duplicated or omitted", events, e)
	}
}
func TestAppealCurrentPolicyAndIndependentGrant(t *testing.T) {
	a := testApp(t)
	owner, writer, original, reviewer := login(t, a, 0), login(t, a, 1), login(t, a, 2), login(t, a, 4)
	p := published(t, a, owner, original, "Policy restoration thread "+uuid.NewString())
	parent := approvedReply(t, owner, original, p.ID, nil)
	for _, mode := range []string{"PARENT_UNAVAILABLE", "THREAD_BLOCKED", "POSTING_NOT_ALLOWED", "TARGET_UNAVAILABLE", "GRANT_REVOKED"} {
		parentID := &parent
		if mode == "THREAD_BLOCKED" {
			parentID = nil
		}
		w := writer.request("POST", "posts/"+p.ID.String()+"/comments", CommentInput{Body: "Policy appeal fixture " + mode, ParentID: parentID}, 0, uuid.NewString())
		mustStatus(t, w, 201)
		cid := parsed[struct{ ID uuid.UUID }](t, w).ID
		did := restrictInitial(t, original, uuid.Nil, cid)
		v := takeAppeal(t, reviewer, newAppeal(t, writer, did))
		var community uuid.UUID
		integrationAdmin.QueryRow(context.Background(), "SELECT community_id FROM social.post WHERE id=$1", p.ID).Scan(&community)
		switch mode {
		case "PARENT_UNAVAILABLE", "THREAD_BLOCKED":
			flag(t, writer, "blocks", myProfileID(t, owner), true)
			t.Cleanup(func() { flag(t, writer, "blocks", myProfileID(t, owner), false) })
		case "POSTING_NOT_ALLOWED":
			integrationAdmin.Exec(context.Background(), "UPDATE social.community_member SET state='BANNED' WHERE community_id=$1 AND profile_id=$2", community, myProfileID(t, writer))
		case "TARGET_UNAVAILABLE":
			integrationAdmin.Exec(context.Background(), "UPDATE social.post SET state='HIDDEN',version=version+1 WHERE id=$1", p.ID)
		case "GRANT_REVOKED":
			integrationAdmin.Exec(context.Background(), "UPDATE identity.platform_grant SET revoked_at=now() WHERE principal_id=$1 AND role='PLATFORM_MODERATOR'", DemoPrincipals[4])
		}
		if mode == "GRANT_REVOKED" {
			mustStatus(t, reviewer.request("POST", "moderation/appeals/"+v.ID.String()+"/decisions", appealInput(v, "REVERSED"), v.Version, ""), 403)
			integrationAdmin.Exec(context.Background(), "UPDATE identity.platform_grant SET revoked_at=NULL WHERE principal_id=$1 AND role='PLATFORM_MODERATOR'", DemoPrincipals[4])
			v = appealContext(t, reviewer, v)
			got := decideAppealTest(t, reviewer, v, "UPHELD")
			if got.Outcome == nil {
				t.Fatal("lost claimed appeal")
			}
		} else {
			v = appealContext(t, reviewer, v)
			expectedReason := mode
			if mode == "THREAD_BLOCKED" {
				expectedReason = "TARGET_UNAVAILABLE"
			}
			if v.RestorationReason != expectedReason {
				t.Fatal("current policy bypass", mode, v.RestorationReason)
			}
			got := decideAppealTest(t, reviewer, v, "REVERSED")
			if got.Outcome.RestorationState != "NOT_RESTORED" {
				t.Fatal("policy changed visibility")
			}
		}
		switch mode {
		case "PARENT_UNAVAILABLE", "THREAD_BLOCKED":
			flag(t, writer, "blocks", myProfileID(t, owner), false)
		case "POSTING_NOT_ALLOWED":
			integrationAdmin.Exec(context.Background(), "UPDATE social.community_member SET state='ACTIVE' WHERE community_id=$1 AND profile_id=$2", community, myProfileID(t, writer))
		case "TARGET_UNAVAILABLE":
			integrationAdmin.Exec(context.Background(), "UPDATE social.post SET state='PUBLISHED',version=version+1 WHERE id=$1", p.ID)
		}
	}
}
func TestAppealPagesQuotaAndOwnerScope(t *testing.T) {
	a := testApp(t)
	owner, other, original, reviewer := login(t, a, 0), login(t, a, 1), login(t, a, 2), login(t, a, 4)
	p := initialPost(t, owner)
	did := restrictInitial(t, original, p.ID, uuid.Nil)
	pid := contentReporterPrincipal(t, owner)
	at := time.Now().Add(time.Hour).UTC().Truncate(time.Microsecond)
	ids := map[uuid.UUID]bool{}
	// Different historical decisions, equal timestamps: exercise UUID tie-breaking.
	for i := 0; i < 23; i++ {
		mid, d, aid := uuid.New(), uuid.New(), uuid.New()
		ids[aid] = false
		if _, e := integrationAdmin.Exec(context.Background(), "INSERT INTO social.moderation_case(id,post_id,target_version,reason_code,grounds,state) VALUES($1,$2,1,'PUBLICATION_REVIEW','Private original note','DECIDED')", mid, p.ID); e != nil {
			t.Fatal(e)
		}
		if _, e := integrationAdmin.Exec(context.Background(), "INSERT INTO social.moderation_decision(id,moderation_case_id,sequence,action,rule_version,actor_ref,reason) VALUES($1,$2,1,'RESTRICT','test',$3,'Private internal note')", d, mid, DemoPrincipals[2]); e != nil {
			t.Fatal(e)
		}
		if _, e := integrationAdmin.Exec(context.Background(), "INSERT INTO social.appeal(id,decision_id,appellant_ref,grounds,state,created_at) VALUES($1,$2,$3,'Pagination appeal grounds','OPEN',$4)", aid, d, pid, at); e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() {
			integrationAdmin.Exec(context.Background(), "DELETE FROM social.appeal WHERE id=$1", aid)
			integrationAdmin.Exec(context.Background(), "DELETE FROM social.moderation_decision WHERE id=$1", d)
			integrationAdmin.Exec(context.Background(), "DELETE FROM social.moderation_case WHERE id=$1", mid)
		})
	}
	path := "me/appeals"
	w := owner.request("GET", path, nil, 0, "")
	mustStatus(t, w, 200)
	type page struct {
		Items      []appealResponse
		NextCursor *string
	}
	first := parsed[page](t, w)
	if len(first.Items) != 20 || first.NextCursor == nil {
		t.Fatal("missing page")
	}
	cursor := url.QueryEscape(*first.NextCursor)
	mustStatus(t, other.request("GET", path+"?cursor="+cursor, nil, 0, ""), 422)
	mustStatus(t, reviewer.request("GET", "moderation/appeals?cursor="+cursor, nil, 0, ""), 422)
	mustStatus(t, owner.request("GET", path+"?cursor="+cursor+"x", nil, 0, ""), 422)
	c, e := a.profilePageCursor(*first.NextCursor, "appeals", myProfileID(t, owner), myProfileID(t, owner))
	if e != nil {
		t.Fatal(e)
	}
	c.Expires = time.Now().Add(-time.Minute).Unix()
	mustStatus(t, owner.request("GET", path+"?cursor="+url.QueryEscape(a.encodeProfileCursor(c)), nil, 0, ""), 410)
	for current := first; ; {
		for _, v := range current.Items {
			if seen, ok := ids[v.ID]; ok {
				if seen {
					t.Fatal("duplicate page item")
				}
				ids[v.ID] = true
			}
		}
		if current.NextCursor == nil {
			break
		}
		w = owner.request("GET", path+"?cursor="+url.QueryEscape(*current.NextCursor), nil, 0, "")
		mustStatus(t, w, 200)
		current = parsed[page](t, w)
	}
	for id, seen := range ids {
		if !seen {
			t.Fatal("missing appeal", id)
		}
	}
	mustStatus(t, owner.request("POST", "moderation/decisions/"+did.String()+"/appeals", map[string]any{"grounds": "Quota must reject this new appeal"}, 0, uuid.NewString()), 429)
	w = reviewer.request("GET", "moderation/appeals", nil, 0, "")
	mustStatus(t, w, 200)
	if len(parsed[page](t, w).Items) != 20 {
		t.Fatal("staff page unbounded")
	}
}

func TestAppealRejectedEditAndSeparateRemovalAreIndependent(t *testing.T) {
	a := testApp(t)
	owner, reporter, original, reviewer := login(t, a, 0), login(t, a, 1), login(t, a, 2), login(t, a, 4)
	cleanContentReports(t, reporter)
	for _, mode := range []string{"RESTORED", "OTHER_REMOVAL", "DELETED", "LATER_CANDIDATE"} {
		p := published(t, a, owner, original, "Published original "+uuid.NewString())
		candidate := "Rejected revision two " + uuid.NewString()
		mustStatus(t, owner.request("PATCH", "posts/"+p.ID.String(), map[string]any{"title": "Reviewed edit", "body": candidate, "submitForReview": true}, p.Version, ""), 200)
		var mid, did uuid.UUID
		if e := integrationAdmin.QueryRow(context.Background(), "SELECT id FROM social.moderation_case WHERE post_id=$1 AND target_version=2 AND reporter_ref IS NULL", p.ID).Scan(&mid); e != nil {
			t.Fatal(e)
		}
		mustStatus(t, original.request("POST", "moderation/"+mid.String()+"/decisions", map[string]any{"action": "RESTRICT", "reason": "Internal rejected edit reason", "authorReason": "Please review the fictional edit policy", "targetRevision": 2}, 1, ""), 200)
		if e := integrationAdmin.QueryRow(context.Background(), "SELECT id FROM social.moderation_decision WHERE moderation_case_id=$1", mid).Scan(&did); e != nil {
			t.Fatal(e)
		}
		v := takeAppeal(t, reviewer, newAppeal(t, owner, did))
		ctx := scopedContext(reviewer, a.DB, vault.Grant{})
		// Even an assigned reviewer cannot insert an outcome without closing its claim.
		if _, e := a.store(ctx).Exec(ctx, "INSERT INTO social.appeal_decision(id,appeal_id,result,author_reason,restoration_state,restoration_reason,reviewer_ref) VALUES($1,$2,'UPHELD','Incomplete result','UNCHANGED','NONE',$3)", uuid.New(), v.ID, DemoPrincipals[4]); e == nil {
			t.Fatal("partial outcome committed")
		}
		if mode == "OTHER_REMOVAL" || mode == "LATER_CANDIDATE" {
			removedDecision(t, original, reporter, "POST", p.ID)
		}
		if mode == "LATER_CANDIDATE" {
			// The removal appeal for revision one must not publish revision two either.
			var removed uuid.UUID
			integrationAdmin.QueryRow(context.Background(), "SELECT d.id FROM social.moderation_decision d JOIN social.moderation_case m ON m.id=d.moderation_case_id WHERE m.post_id=$1 AND d.action='REMOVE'", p.ID).Scan(&removed)
			removal := takeAppeal(t, reviewer, newAppeal(t, owner, removed))
			if removal.RestorationReason != "TARGET_CHANGED" {
				t.Fatal("removal can publish later candidate")
			}
			got := decideAppealTest(t, reviewer, removal, "REVERSED")
			if got.Outcome.RestorationState != "NOT_RESTORED" {
				t.Fatal("later candidate published")
			}
		}
		if mode == "DELETED" {
			var version int64
			integrationAdmin.QueryRow(context.Background(), "SELECT version FROM social.post WHERE id=$1", p.ID).Scan(&version)
			mustStatus(t, owner.request("DELETE", "posts/"+p.ID.String(), nil, version, ""), 204)
		}
		v = appealContext(t, reviewer, v)
		got := decideAppealTest(t, reviewer, v, "REVERSED")
		if mode == "RESTORED" {
			if got.Outcome.RestorationState != "RESTORED" {
				t.Fatal("rejected edit not restored")
			}
			w := reporter.request("GET", "posts/"+p.ID.String(), nil, 0, "")
			mustStatus(t, w, 200)
			if !strings.Contains(w.Body.String(), candidate) {
				t.Fatal("wrong exact edit published")
			}
		} else {
			if got.Outcome.RestorationState != "NOT_RESTORED" || got.Outcome.RestorationReason != "TARGET_UNAVAILABLE" {
				t.Fatal("another removal/deletion bypassed", mode, got)
			}
		}
	}
}

func TestAppealReporterConflictAndClaimsCannotBeStolen(t *testing.T) {
	a := testApp(t)
	owner, original, reviewer, third := login(t, a, 0), login(t, a, 2), login(t, a, 4), login(t, a, 3)
	grant := uuid.New()
	if _, e := integrationAdmin.Exec(context.Background(), "INSERT INTO identity.platform_grant(id,principal_id,role,valid_to) VALUES($1,$2,'PLATFORM_MODERATOR',now()+interval '1 hour')", grant, DemoPrincipals[3]); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		integrationAdmin.Exec(context.Background(), "DELETE FROM identity.platform_grant WHERE id=$1", grant)
	})
	p := initialPost(t, owner)
	did := restrictInitial(t, original, p.ID, uuid.Nil)
	v := newAppeal(t, owner, did)
	mustStatus(t, third.request("GET", "moderation/appeals/"+v.ID.String(), nil, 0, ""), 200)
	v = takeAppeal(t, reviewer, v)
	mustStatus(t, third.request("GET", "moderation/appeals/"+v.ID.String(), nil, 0, ""), 404)
	mustStatus(t, third.request("POST", "moderation/appeals/"+v.ID.String()+"/claim", nil, v.Version, ""), 404)
	ctx := scopedContext(owner, a.DB, vault.Grant{})
	if _, e := a.store(ctx).Exec(ctx, "INSERT INTO social.appeal(id,decision_id,appellant_ref,grounds,state) VALUES($1,$2,$3,'Forged appellant grounds','OPEN')", uuid.New(), did, DemoPrincipals[1]); e == nil {
		t.Fatal("forged appellant")
	}
	publishedPost := published(t, a, owner, original, "Reporter independence "+uuid.NewString())
	cleanContentReports(t, reviewer)
	did = removedDecision(t, original, reviewer, "POST", publishedPost.ID)
	v = newAppeal(t, owner, did)
	mustStatus(t, reviewer.request("GET", "moderation/appeals/"+v.ID.String(), nil, 0, ""), 404)
	mustStatus(t, reviewer.request("POST", "moderation/appeals/"+v.ID.String()+"/claim", nil, 1, ""), 404)
	v = takeAppeal(t, third, v)
	if decideAppealTest(t, third, v, "UPHELD").Outcome.RestorationState != "UNCHANGED" {
		t.Fatal("reporter appeal changed publication")
	}
	// Positive publication decisions cannot be appealed.
	var approved uuid.UUID
	if e := integrationAdmin.QueryRow(context.Background(), "SELECT d.id FROM social.moderation_decision d JOIN social.moderation_case m ON m.id=d.moderation_case_id WHERE m.post_id=$1 AND d.action='ALLOW'", publishedPost.ID).Scan(&approved); e != nil {
		t.Fatal(e)
	}
	mustStatus(t, owner.request("POST", "moderation/decisions/"+approved.String()+"/appeals", map[string]any{"grounds": "Cannot appeal an approval"}, 0, uuid.NewString()), 422)
}

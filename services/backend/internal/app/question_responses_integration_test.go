package app

import (
	"context"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lavkushry/JanSetu-AI/services/backend/internal/vault"
)

type questionPost struct {
	testPost
	SelectedResponse *struct {
		CommentID        uuid.UUID
		PostRevision     int64
		CommentRevision  int64
		Body, SelectedBy string
		Author           struct{ ID uuid.UUID }
	}
	Viewer struct{ CanSelectResponse bool }
}

func questionFixture(t *testing.T, owner, mod client) questionPost {
	t.Helper()
	community := uuid.MustParse("50000000-0000-4000-8000-000000000001")
	w := owner.request("POST", "posts", PostInput{Kind: "QUESTION", CommunityID: &community, Title: ptr("Fictional neighbourhood question"), Body: "Where is the fictional community meeting?", SubmitForReview: true}, 0, uuid.NewString())
	mustStatus(t, w, 201)
	p := parsed[questionPost](t, w)
	review(t, mod, p.ID, uuid.Nil, 1)
	return readQuestion(t, owner, p.ID)
}
func readQuestion(t *testing.T, c client, id uuid.UUID) questionPost {
	t.Helper()
	w := c.request("GET", "posts/"+id.String(), nil, 0, "")
	mustStatus(t, w, 200)
	return parsed[questionPost](t, w)
}
func responseRequest(c client, p questionPost, comment *uuid.UUID, revisions ...int64) *httptest.ResponseRecorder {
	input := map[string]any{"commentId": comment}
	if comment != nil {
		revision := int64(1)
		if len(revisions) > 0 {
			revision = revisions[0]
		}
		input["commentRevision"] = revision
	}
	return c.request("PUT", "posts/"+p.ID.String()+"/selected-response", input, p.Version, "")
}
func chooseResponse(t *testing.T, c client, p questionPost, comment *uuid.UUID, revisions ...int64) questionPost {
	t.Helper()
	w := responseRequest(c, p, comment, revisions...)
	mustStatus(t, w, 200)
	result := parsed[struct {
		CommentID *uuid.UUID
		Version   int64
	}](t, w)
	current := readQuestion(t, c, p.ID)
	if result.Version != current.Version || (comment == nil) != (result.CommentID == nil) || comment != nil && *result.CommentID != *comment {
		t.Fatal("choice result mismatch", w.Body)
	}
	return current
}

func TestQuestionResponsePermissionsDesiredStateAndConcurrency(t *testing.T) {
	a := testApp(t)
	owner, writer, mod := login(t, a, 0), login(t, a, 1), login(t, a, 2)
	anon := client{app: a}
	p := questionFixture(t, owner, mod)
	ids := threadComments(t, p.ID, myProfileID(t, writer), nil, 0, time.Now().UTC(), 2)
	path := "posts/" + p.ID.String() + "/selected-response"
	mustStatus(t, owner.request("PUT", path, map[string]any{}, p.Version, ""), 422)
	mustStatus(t, owner.request("PUT", path, map[string]any{"commentId": ids[0]}, p.Version, ""), 422)
	mustStatus(t, owner.request("PUT", path, map[string]any{"commentId": nil}, 0, ""), 428)
	mustStatus(t, owner.request("PUT", path, map[string]any{"commentId": "invalid"}, p.Version, ""), 422)
	mustStatus(t, anon.request("PUT", path, map[string]any{"commentId": ids[0], "commentRevision": 1}, p.Version, ""), 401)
	mustStatus(t, responseRequest(writer, p, &ids[0]), 403)
	// A platform moderation grant alone is not community authority.
	mustStatus(t, responseRequest(mod, p, &ids[0]), 403)
	other := published(t, a, owner, mod, "Other-thread response fixture")
	foreign := threadComments(t, other.ID, myProfileID(t, writer), nil, 0, time.Now().UTC(), 1)[0]
	mustStatus(t, responseRequest(owner, p, &foreign), 422)
	mustStatus(t, responseRequest(owner, questionPost{testPost: other}, &foreign), 422)
	pending := owner.request("POST", "posts/"+p.ID.String()+"/comments", CommentInput{Body: "Unapproved reply"}, 0, uuid.NewString())
	mustStatus(t, pending, 201)
	pendingID := parsed[struct{ ID uuid.UUID }](t, pending).ID
	mustStatus(t, responseRequest(owner, p, &pendingID), 422)
	var wg sync.WaitGroup
	results := make(chan *httptest.ResponseRecorder, 2)
	for _, cid := range ids {
		wg.Add(1)
		go func(cid uuid.UUID) { defer wg.Done(); results <- responseRequest(owner, p, &cid) }(cid)
	}
	wg.Wait()
	close(results)
	success, stale := 0, 0
	for w := range results {
		if w.Code == 200 {
			success++
		} else if w.Code == 412 {
			stale++
		} else {
			t.Fatal(w.Code, w.Body)
		}
	}
	if success != 1 || stale != 1 {
		t.Fatal("lost update allowed", success, stale)
	}
	p = readQuestion(t, owner, p.ID)
	if p.SelectedResponse == nil || p.Version != 3 || !p.Viewer.CanSelectResponse {
		t.Fatal("missing choice", p)
	}
	public := readQuestion(t, anon, p.ID)
	if public.SelectedResponse == nil || public.SelectedResponse.SelectedBy != "AUTHOR" || public.Viewer.CanSelectResponse {
		t.Fatal("public choice metadata", public)
	}
	selected := p.SelectedResponse.CommentID
	noop := chooseResponse(t, owner, p, &selected)
	if noop.Version != p.Version {
		t.Fatal("identical choice changed version")
	}
	p = chooseResponse(t, owner, p, nil)
	if p.SelectedResponse != nil {
		t.Fatal("choice not cleared")
	}
	noop = chooseResponse(t, owner, p, nil)
	if noop.Version != p.Version {
		t.Fatal("empty clear changed version")
	}
	var events int
	if e := integrationAdmin.QueryRow(context.Background(), `SELECT count(*) FROM infra.outbox WHERE aggregate_id=$1 AND event_type='HelpfulResponseChanged'`, p.ID).Scan(&events); e != nil || events != 2 {
		t.Fatal("no-op emitted duplicate event", events, e)
	}
	// Both API and runtime SQL require actual same-community authority.
	scope := scopedContext(writer, a.DB, vault.Grant{})
	if _, e := a.store(scope).Exec(scope, `INSERT INTO social.selected_response(post_id,comment_id,selected_by,post_revision,comment_revision) VALUES($1,$2,$3,1,1)`, p.ID, ids[0], myProfileID(t, writer)); e == nil {
		t.Fatal("foreign choice escaped RLS")
	}
	ownerScope := scopedContext(owner, a.DB, vault.Grant{})
	if _, e := a.store(ownerScope).Exec(ownerScope, `INSERT INTO social.selected_response(post_id,comment_id,selected_by,post_revision,comment_revision) VALUES($1,$2,$3,1,1)`, p.ID, foreign, myProfileID(t, owner)); e == nil {
		t.Fatal("cross-thread choice escaped database enforcement")
	}
	deniedSQL(t, a.DB, `UPDATE social.selected_response SET selected_at=now() WHERE post_id=$1`, p.ID)
	community := uuid.MustParse("50000000-0000-4000-8000-000000000001")
	writerID := myProfileID(t, writer)
	// A resident cannot manufacture the scoped role on which selection relies.
	if _, e := a.store(scope).Exec(scope, `UPDATE social.community_member SET role='MODERATOR' WHERE community_id=$1 AND profile_id=$2`, community, writerID); e == nil {
		t.Fatal("runtime could provision moderator authority")
	}
	if _, e := a.store(scope).Exec(scope, `INSERT INTO social.community_member(community_id,profile_id,role,state) VALUES($1,$2,'MODERATOR','ACTIVE') ON CONFLICT(community_id,profile_id) DO UPDATE SET role='MODERATOR'`, community, writerID); e == nil {
		t.Fatal("forged moderator upsert accepted")
	}
	if _, e := a.store(scope).Exec(scope, `DELETE FROM social.community_member WHERE community_id=$1 AND profile_id=$2`, community, writerID); e == nil {
		t.Fatal("runtime could erase a membership/ban")
	}
	newCommunity := uuid.New()
	if _, e := integrationAdmin.Exec(context.Background(), `INSERT INTO social.community(id,slug,title,scope_kind,visibility,rules_body,state) VALUES($1,$2,'Fictional role fixture','TOPIC','PUBLIC','Be constructive','ACTIVE')`, newCommunity, "role-fixture-"+newCommunity.String()); e != nil {
		t.Fatal(e)
	}
	if _, e := a.store(scope).Exec(scope, `INSERT INTO social.community_member(community_id,profile_id,role,state) VALUES($1,$2,'MODERATOR','ACTIVE')`, newCommunity, writerID); e == nil {
		t.Fatal("forged moderator insert accepted")
	}
	mustStatus(t, writer.request("PUT", "communities/"+newCommunity.String()+"/membership", map[string]any{"joined": true, "rulesRevision": 1}, 0, ""), 200)
	if tag, e := a.store(scope).Exec(scope, `UPDATE social.community_member SET state='LEFT' WHERE community_id=$1 AND profile_id=$2`, community, myProfileID(t, owner)); e != nil || tag.RowsAffected() != 0 {
		t.Fatal("foreign membership changed", e)
	}
	if _, e := integrationAdmin.Exec(context.Background(), `UPDATE social.community_member SET role='MODERATOR' WHERE community_id=$1 AND profile_id=$2`, community, writerID); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		integrationAdmin.Exec(context.Background(), `UPDATE social.community_member SET role='MEMBER',state='ACTIVE' WHERE community_id=$1 AND profile_id=$2`, community, writerID)
	})
	if !readQuestion(t, writer, p.ID).Viewer.CanSelectResponse {
		t.Fatal("scoped moderator missing control")
	}
	// Join/leave preserves a provisioned role; neither state command can replace it.
	mustStatus(t, writer.request("PUT", "communities/"+community.String()+"/membership", map[string]any{"joined": false, "rulesRevision": 1}, 0, ""), 200)
	mustStatus(t, writer.request("PUT", "communities/"+community.String()+"/membership", map[string]any{"joined": true, "rulesRevision": 1}, 0, ""), 200)
	if !readQuestion(t, writer, p.ID).Viewer.CanSelectResponse {
		t.Fatal("membership command discarded provisioned role")
	}
	p = chooseResponse(t, writer, p, &ids[1])
	if p.SelectedResponse.SelectedBy != "COMMUNITY_MODERATOR" {
		t.Fatal("misattributed moderator choice")
	}
	if _, e := integrationAdmin.Exec(context.Background(), `UPDATE social.community_member SET state='LEFT' WHERE community_id=$1 AND profile_id=$2`, community, writerID); e != nil {
		t.Fatal(e)
	}
	mustStatus(t, responseRequest(writer, p, nil), 403)
	if readQuestion(t, writer, p.ID).Viewer.CanSelectResponse {
		t.Fatal("revoked scope retained control")
	}
	if _, e := integrationAdmin.Exec(context.Background(), `UPDATE social.community_member SET state='BANNED' WHERE community_id=$1 AND profile_id=$2`, community, writerID); e != nil {
		t.Fatal(e)
	}
	mustStatus(t, writer.request("PUT", "communities/"+community.String()+"/membership", map[string]any{"joined": true, "rulesRevision": 1}, 0, ""), 403)
	if tag, e := a.store(scope).Exec(scope, `UPDATE social.community_member SET state='ACTIVE' WHERE community_id=$1 AND profile_id=$2`, community, writerID); e != nil || tag.RowsAffected() != 0 {
		t.Fatal("runtime removed own ban", e)
	}
}

func TestQuestionResponseRevisionBindingAndVisibility(t *testing.T) {
	a := testApp(t)
	owner, writer, mod := login(t, a, 0), login(t, a, 1), login(t, a, 2)
	anon := client{app: a}
	p := questionFixture(t, owner, mod)
	writerID, ownerID := myProfileID(t, writer), myProfileID(t, owner)
	cid := threadComments(t, p.ID, writerID, nil, 0, time.Now().UTC(), 1)[0]
	p = chooseResponse(t, owner, p, &cid)
	assertSelected := func(c client, want bool) {
		t.Helper()
		if (readQuestion(t, c, p.ID).SelectedResponse != nil) != want {
			t.Fatal("selection visibility", want)
		}
	}
	mustStatus(t, writer.request("PATCH", "comments/"+cid.String(), map[string]any{"body": "PRIVATE PENDING ANSWER", "languageTag": "en-IN"}, 1, ""), 200)
	w := anon.request("GET", "posts/"+p.ID.String(), nil, 0, "")
	if strings.Contains(w.Body.String(), "PRIVATE PENDING ANSWER") {
		t.Fatal("helpful summary leaked pending answer")
	}
	assertSelected(anon, true)
	review(t, mod, uuid.Nil, cid, 2)
	assertSelected(anon, false)
	// Comment approval changes no post version, so If-Match alone is insufficient.
	mustStatus(t, responseRequest(owner, p, &cid, 1), 412)
	assertSelected(anon, false)
	// The old reply's helpful label cannot attach to newly approved text.
	p = chooseResponse(t, owner, p, &cid, 2)
	if p.SelectedResponse.CommentRevision != 2 {
		t.Fatal("reply not rebound")
	}
	mustStatus(t, owner.request("PATCH", "posts/"+p.ID.String(), map[string]any{"title": "Changed question", "body": "A revised fictional question", "submitForReview": true}, p.Version, ""), 200)
	p = readQuestion(t, owner, p.ID)
	assertSelected(anon, true)
	review(t, mod, p.ID, uuid.Nil, 2)
	p = readQuestion(t, owner, p.ID)
	assertSelected(anon, false)
	p = chooseResponse(t, owner, p, &cid, 2)
	if p.SelectedResponse.PostRevision != 2 {
		t.Fatal("question not rebound")
	}
	flag(t, mod, "blocks", writerID, true)
	t.Cleanup(func() {
		flag(t, mod, "blocks", writerID, false)
		flag(t, owner, "blocks", writerID, false)
		flag(t, writer, "blocks", ownerID, false)
	})
	assertSelected(mod, false)
	assertSelected(anon, true)
	flag(t, mod, "blocks", writerID, false)
	flag(t, owner, "blocks", writerID, true)
	assertSelected(anon, false)
	mustStatus(t, responseRequest(owner, p, &cid), 422)
	flag(t, owner, "blocks", writerID, false)
	flag(t, writer, "blocks", ownerID, true)
	assertSelected(anon, false)
	flag(t, writer, "blocks", ownerID, false)
	if _, e := integrationAdmin.Exec(context.Background(), `UPDATE social.profile SET state='DEACTIVATED' WHERE id=$1`, writerID); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		integrationAdmin.Exec(context.Background(), `UPDATE social.profile SET state='ACTIVE' WHERE id=$1`, writerID)
	})
	assertSelected(anon, false)
	if _, e := integrationAdmin.Exec(context.Background(), `UPDATE social.profile SET state='ACTIVE' WHERE id=$1`, writerID); e != nil {
		t.Fatal(e)
	}
	assertSelected(anon, true)
	mustStatus(t, writer.request("DELETE", "comments/"+cid.String(), nil, 3, ""), 204)
	assertSelected(anon, false)
	mustStatus(t, responseRequest(owner, p, &cid), 422)
	p = chooseResponse(t, owner, p, nil)
	cid = threadComments(t, p.ID, writerID, nil, 0, time.Now().UTC(), 1)[0]
	p = chooseResponse(t, owner, p, &cid)
	cleanContentReports(t, owner)
	report := submitContentReport(t, owner, "COMMENT", cid, 1)
	decideContentReport(t, mod, report, "REMOVE")
	assertSelected(anon, false)
	mustStatus(t, responseRequest(owner, p, &cid), 422)
	// Public hydration checks sources again even through a saved feed cursor.
	cursor := feedCursor{Viewer: uuid.Nil, Query: "HOME||" + uuid.Nil.String(), Expires: time.Now().Add(time.Minute).Unix(), Refs: []feedRef{{Type: "POST", ID: p.ID}}}
	w = anon.request("GET", "feed?cursor="+a.encodeCursor(cursor), nil, 0, "")
	mustStatus(t, w, 200)
	feed := parsed[struct{ Items []struct{ Post questionPost } }](t, w)
	if len(feed.Items) != 1 || feed.Items[0].Post.ID != p.ID || feed.Items[0].Post.SelectedResponse != nil {
		t.Fatal("feed retained revoked helpful summary")
	}
	mustStatus(t, owner.request("DELETE", "posts/"+p.ID.String(), nil, p.Version, ""), 204)
	assertSelected(anon, false)
	mustStatus(t, responseRequest(owner, p, nil), 422)
}

func TestQuestionResponseLaterPageAndCommunityAccess(t *testing.T) {
	a := testApp(t)
	owner, writer, mod := login(t, a, 0), login(t, a, 1), login(t, a, 2)
	anon := client{app: a}
	p := questionFixture(t, owner, mod)
	at := time.Now().Add(-time.Hour).UTC()
	parents := threadComments(t, p.ID, myProfileID(t, owner), nil, 0, at, 20)
	cid := threadComments(t, p.ID, myProfileID(t, writer), &parents[0], 1, at.Add(time.Minute), 1)[0]
	p = chooseResponse(t, owner, p, &cid)
	w := anon.request("GET", "posts/"+p.ID.String()+"/comments", nil, 0, "")
	mustStatus(t, w, 200)
	page := parsed[commentPageResult](t, w)
	if len(page.Items) != 20 || page.NextCursor == nil || strings.Contains(w.Body.String(), cid.String()) {
		t.Fatal("fixture is not on a later page")
	}
	if readQuestion(t, anon, p.ID).SelectedResponse.CommentID != cid {
		t.Fatal("later-page helpful summary missing")
	}
	mustStatus(t, owner.request("DELETE", "comments/"+parents[0].String(), nil, 1, ""), 204)
	if readQuestion(t, anon, p.ID).SelectedResponse.CommentID != cid {
		t.Fatal("independent descendant choice lost")
	}
	// A direct SQL reader also loses helpful metadata when the community is hidden.
	community := uuid.MustParse("50000000-0000-4000-8000-000000000001")
	t.Cleanup(func() {
		integrationAdmin.Exec(context.Background(), `UPDATE social.community SET visibility='PUBLIC',state='ACTIVE' WHERE id=$1`, community)
	})
	scope := scopedContext(mod, a.DB, vault.Grant{})
	for _, update := range []string{"state='FROZEN'", "state='ACTIVE',visibility='PRIVATE'"} {
		if _, e := integrationAdmin.Exec(context.Background(), "UPDATE social.community SET "+update+" WHERE id=$1", community); e != nil {
			t.Fatal(e)
		}
		mustStatus(t, anon.request("GET", "posts/"+p.ID.String(), nil, 0, ""), 404)
		mustStatus(t, responseRequest(owner, p, &cid), 404)
		var count int
		if e := a.store(scope).QueryRow(scope, "SELECT count(*) FROM social.selected_response WHERE post_id=$1", p.ID).Scan(&count); e != nil || count != 0 {
			t.Fatal("hidden community selection escaped RLS", count, e)
		}
	}
}

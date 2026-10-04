package app

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

type profilePostResult struct {
	Items []struct {
		ID        uuid.UUID `json:"id"`
		State     string    `json:"state"`
		Candidate any       `json:"candidate"`
	} `json:"items"`
	NextCursor *string `json:"nextCursor"`
}
type blockedResult struct {
	Items []struct {
		ProfileID uuid.UUID `json:"profileId"`
		Profile   any       `json:"profile"`
	} `json:"items"`
	NextCursor *string `json:"nextCursor"`
}

func myProfileID(t *testing.T, c client) uuid.UUID {
	t.Helper()
	r := c.request("GET", "me", nil, 0, "")
	mustStatus(t, r, 200)
	return parsed[struct{ Profile struct{ ID uuid.UUID } }](t, r).Profile.ID
}
func flag(t *testing.T, c client, kind string, target uuid.UUID, enabled bool) {
	t.Helper()
	mustStatus(t, c.request("PUT", "me/"+kind+"/"+target.String(), map[string]any{"enabled": enabled}, 0, ""), 200)
}

func TestPublicProfilePublicationRelationshipsAndPrivacy(t *testing.T) {
	a := testApp(t)
	owner, other, mod := login(t, a, 0), login(t, a, 1), login(t, a, 2)
	anon := client{app: a}
	pid, oid := myProfileID(t, owner), myProfileID(t, other)
	path := "profiles/" + pid.String()
	r := anon.request("GET", path, nil, 0, "")
	mustStatus(t, r, 200)
	detail := parsed[map[string]any](t, r)
	public := detail["profile"].(map[string]any)
	if len(public) != 5 || public["handle"] == nil || public["bio"] == nil || public["joinedAt"] == nil {
		t.Fatal("profile allowlist changed", public)
	}
	for _, secret := range []string{"principal", "provider", "roles", "agencies", "reportAlias", "session", "authorization"} {
		if strings.Contains(r.Body.String(), secret) {
			t.Fatal("private account metadata in public profile", secret)
		}
	}
	p := published(t, a, owner, mod, "Approved text for the public profile boundary")
	community := uuid.MustParse("50000000-0000-4000-8000-000000000001")
	body := PostInput{Kind: "DISCUSSION", CommunityID: &community, Title: ptr("A private pending profile test"), Body: "PRIVATE UNPUBLISHED PROFILE CONTENT", SubmitForReview: true}
	mustStatus(t, owner.request("POST", "posts", body, 0, uuid.NewString()), 201)
	body.Body = "PRIVATE PENDING EDIT CANDIDATE"
	mustStatus(t, owner.request("PATCH", "posts/"+p.ID.String(), map[string]any{"title": body.Title, "body": body.Body, "languageTag": "en-IN", "mediaIds": []uuid.UUID{}, "submitForReview": true}, p.Version, ""), 200)
	for _, c := range []client{anon, owner, other} {
		r = c.request("GET", path+"/posts", nil, 0, "")
		mustStatus(t, r, 200)
		if strings.Contains(r.Body.String(), "PRIVATE") || !strings.Contains(r.Body.String(), "Approved text for the public profile boundary") {
			t.Fatal("profile publication boundary", r.Body)
		}
		for _, post := range parsed[profilePostResult](t, r).Items {
			if post.State != "PUBLISHED" || post.Candidate != nil {
				t.Fatal("private candidate or unpublished post in timeline", post)
			}
		}
	}
	flag(t, other, "following", pid, true)
	flag(t, owner, "following", oid, true)
	r = other.request("GET", path, nil, 0, "")
	mustStatus(t, r, 200)
	if !parsed[struct{ Viewer struct{ Following bool } }](t, r).Viewer.Following {
		t.Fatal("follow missing")
	}
	flag(t, other, "blocks", pid, true)
	t.Cleanup(func() { flag(t, other, "blocks", pid, false); flag(t, owner, "blocks", oid, false) })
	for _, cpath := range []string{path, path + "/posts"} {
		mustStatus(t, other.request("GET", cpath, nil, 0, ""), 404)
	}
	mustStatus(t, owner.request("GET", "profiles/"+oid.String(), nil, 0, ""), 404)
	mustStatus(t, other.request("PUT", "me/following/"+pid.String(), map[string]any{"enabled": true}, 0, ""), 403)
	r = other.request("GET", "search?q=Ananya", nil, 0, "")
	mustStatus(t, r, 200)
	if strings.Contains(r.Body.String(), pid.String()) {
		t.Fatal("blocked profile in search")
	}
	mustStatus(t, anon.request("GET", "me/blocks", nil, 0, ""), 401)
	blocked := other.request("GET", "me/blocks?profileId="+pid.String(), nil, 0, "")
	mustStatus(t, blocked, 200)
	items := parsed[blockedResult](t, blocked).Items
	if len(items) != 1 || items[0].ProfileID != pid {
		t.Fatal("block list not owner-scoped", items)
	}
	ownBlocks := owner.request("GET", "me/blocks", nil, 0, "")
	mustStatus(t, ownBlocks, 200)
	if strings.Contains(ownBlocks.Body.String(), oid.String()) {
		t.Fatal("incoming block exposed as own setting")
	}
	flag(t, other, "blocks", pid, false)
	flag(t, other, "blocks", pid, false)
	for _, pair := range []struct {
		c      client
		target uuid.UUID
	}{{other, pid}, {owner, oid}} {
		r = pair.c.request("GET", "profiles/"+pair.target.String(), nil, 0, "")
		mustStatus(t, r, 200)
		if parsed[struct{ Viewer struct{ Following bool } }](t, r).Viewer.Following {
			t.Fatal("unblock restored follows")
		}
	}
	flag(t, owner, "blocks", oid, true)
	ctx := context.Background()
	if _, e := integrationAdmin.Exec(ctx, `UPDATE social.profile SET state='DEACTIVATED' WHERE id=$1`, oid); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _, _ = integrationAdmin.Exec(ctx, `UPDATE social.profile SET state='ACTIVE' WHERE id=$1`, oid) })
	r = owner.request("GET", "me/blocks", nil, 0, "")
	mustStatus(t, r, 200)
	for _, item := range parsed[blockedResult](t, r).Items {
		if item.ProfileID == oid && item.Profile != nil {
			t.Fatal("inactive public details exposed")
		}
	}
	mustStatus(t, anon.request("GET", "profiles/"+oid.String(), nil, 0, ""), 404)
	flag(t, owner, "blocks", oid, false)
	flag(t, owner, "following", oid, false)
	mustStatus(t, owner.request("PUT", "me/following/"+oid.String(), map[string]any{"enabled": true}, 0, ""), 404)
}

// Independent public profiles with no principal/session data are sufficient to
// seed pagination fixtures. Reads still use the actual restricted API login.
func profileFixture(t *testing.T, n int) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	pid := uuid.New()
	tx, e := integrationAdmin.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, `INSERT INTO social.profile(id,handle,display_name,state) VALUES($1,$2,'Fictional pagination member','ACTIVE')`, pid, "test_"+strings.ReplaceAll(pid.String(), "-", "")[:20]); e != nil {
		t.Fatal(e)
	}
	for i := 0; i < n; i++ {
		mid := uuid.New()
		when := time.Now().Add(-time.Duration(i+1) * time.Minute)
		if _, e = tx.Exec(ctx, `INSERT INTO social.post(id,author_id,kind,state) VALUES($1,$2,'SHORT','PENDING')`, mid, pid); e != nil {
			t.Fatal(e)
		}
		if _, e = tx.Exec(ctx, `INSERT INTO social.post_revision(post_id,revision,body,language_tag,review_state) VALUES($1,1,$2,'en-IN','APPROVED')`, mid, fmt.Sprintf("Fictional profile pagination post %d", i)); e != nil {
			t.Fatal(e)
		}
		if _, e = tx.Exec(ctx, `UPDATE social.post SET state='PUBLISHED',published_revision=1,published_at=$2 WHERE id=$1`, mid, when); e != nil {
			t.Fatal(e)
		}
	}
	if e = tx.Commit(ctx); e != nil {
		t.Fatal(e)
	}
	return pid
}
func TestProfilePaginationRechecksCurrentVisibility(t *testing.T) {
	a := testApp(t)
	anon := client{app: a}
	viewer := login(t, a, 1)
	pid := profileFixture(t, 25)
	path := "profiles/" + pid.String() + "/posts"
	r := viewer.request("GET", path, nil, 0, "")
	mustStatus(t, r, 200)
	first := parsed[profilePostResult](t, r)
	if len(first.Items) != 20 || first.NextCursor == nil {
		t.Fatal(first)
	}
	page2 := path + "?cursor=" + url.QueryEscape(*first.NextCursor)
	mustStatus(t, anon.request("GET", page2, nil, 0, ""), 422)
	mustStatus(t, viewer.request("GET", "profiles/"+uuid.NewString()+"/posts?cursor="+url.QueryEscape(*first.NextCursor), nil, 0, ""), 422)
	flag(t, viewer, "blocks", pid, true)
	mustStatus(t, viewer.request("GET", page2, nil, 0, ""), 404)
	flag(t, viewer, "blocks", pid, false)
	r = viewer.request("GET", page2, nil, 0, "")
	mustStatus(t, r, 200)
	second := parsed[profilePostResult](t, r)
	if len(second.Items) != 5 || second.NextCursor != nil {
		t.Fatal(second)
	}
	seen := map[uuid.UUID]bool{}
	for _, p := range first.Items {
		seen[p.ID] = true
	}
	for _, p := range second.Items {
		if seen[p.ID] {
			t.Fatal("duplicate timeline item")
		}
	}
	if _, e := integrationAdmin.Exec(context.Background(), `UPDATE social.post SET state='DELETED' WHERE id=$1`, second.Items[0].ID); e != nil {
		t.Fatal(e)
	}
	r = viewer.request("GET", page2, nil, 0, "")
	mustStatus(t, r, 200)
	if strings.Contains(r.Body.String(), second.Items[0].ID.String()) {
		t.Fatal("cursor retained removed post")
	}
}
func TestBlockPaginationIsOwnerBoundAndIncludesInactiveTargets(t *testing.T) {
	a := testApp(t)
	owner, other := login(t, a, 0), login(t, a, 1)
	for i := 0; i < 23; i++ {
		pid := profileFixture(t, 0)
		flag(t, owner, "blocks", pid, true)
		t.Cleanup(func() { flag(t, owner, "blocks", pid, false) })
	}
	r := owner.request("GET", "me/blocks", nil, 0, "")
	mustStatus(t, r, 200)
	first := parsed[blockedResult](t, r)
	if len(first.Items) != 20 || first.NextCursor == nil {
		t.Fatal(first)
	}
	path := "me/blocks?cursor=" + url.QueryEscape(*first.NextCursor)
	mustStatus(t, other.request("GET", path, nil, 0, ""), 422)
	r = owner.request("GET", path, nil, 0, "")
	mustStatus(t, r, 200)
	second := parsed[blockedResult](t, r)
	if len(second.Items) != 3 || second.NextCursor != nil {
		t.Fatal(second)
	}
	for _, p := range first.Items {
		for _, q := range second.Items {
			if p.ProfileID == q.ProfileID {
				t.Fatal("duplicate block page row")
			}
		}
	}
}

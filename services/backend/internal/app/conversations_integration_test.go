package app

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

type commentPageResult struct {
	Items []struct {
		ID        uuid.UUID  `json:"id"`
		ParentID  *uuid.UUID `json:"parentId"`
		State     string     `json:"state"`
		Body      *string    `json:"body"`
		Author    any        `json:"author"`
		Candidate any        `json:"candidate"`
	} `json:"items"`
	NextCursor *string `json:"nextCursor"`
	ExpiresAt  string  `json:"expiresAt"`
}

// Bulk fixtures belong to the isolated integration database, never the running
// application. Equal timestamps exercise the UUID part of the keyset order.
func threadComments(t *testing.T, post, author uuid.UUID, parent *uuid.UUID, depth int16, at time.Time, count int) []uuid.UUID {
	t.Helper()
	ctx := context.Background()
	tx, e := integrationAdmin.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(ctx)
	ids := make([]uuid.UUID, count)
	for i := range ids {
		ids[i] = uuid.New()
		body := fmt.Sprintf("Fictional conversation fixture %s", ids[i])
		if _, e = tx.Exec(ctx, `INSERT INTO social.comment(id,post_id,parent_id,author_id,body,depth,state,created_at) VALUES($1,$2,$3,$4,$5,$6,'PENDING',$7)`, ids[i], post, parent, author, body, depth, at); e != nil {
			t.Fatal(e)
		}
		if _, e = tx.Exec(ctx, `INSERT INTO social.comment_revision(comment_id,version,body,language_tag,review_state) VALUES($1,1,$2,'en-IN','APPROVED')`, ids[i], body); e != nil {
			t.Fatal(e)
		}
		if _, e = tx.Exec(ctx, `UPDATE social.comment SET state='PUBLISHED',published_version=1 WHERE id=$1`, ids[i]); e != nil {
			t.Fatal(e)
		}
	}
	if e = tx.Commit(ctx); e != nil {
		t.Fatal(e)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i].String() < ids[j].String() })
	return ids
}

func TestCommentPaginationBeyond200AndCursorBinding(t *testing.T) {
	a := testApp(t)
	owner, mod := login(t, a, 0), login(t, a, 2)
	anon := client{app: a}
	p := published(t, a, owner, mod, "A fictional large conversation")
	pid := myProfileID(t, owner)
	at := time.Now().Add(-time.Hour).UTC().Truncate(time.Microsecond)
	ids := threadComments(t, p.ID, pid, nil, 0, at, 220)
	path := "posts/" + p.ID.String() + "/comments"
	r := anon.request("GET", path, nil, 0, "")
	mustStatus(t, r, 200)
	first := parsed[commentPageResult](t, r)
	if len(first.Items) != 20 || first.NextCursor == nil {
		t.Fatal("first page", r.Body)
	}
	cursor := url.QueryEscape(*first.NextCursor)
	mustStatus(t, owner.request("GET", path+"?cursor="+cursor, nil, 0, ""), 422)
	other := published(t, a, owner, mod, "A different fictional conversation")
	mustStatus(t, anon.request("GET", "posts/"+other.ID.String()+"/comments?cursor="+cursor, nil, 0, ""), 422)
	mustStatus(t, anon.request("GET", "profiles/"+pid.String()+"/posts?cursor="+cursor, nil, 0, ""), 422)
	mustStatus(t, anon.request("GET", path+"?cursor="+cursor+"x", nil, 0, ""), 422)
	mustStatus(t, anon.request("GET", path+"?cursor="+strings.Repeat("a", 1001), nil, 0, ""), 422)
	expired := a.encodeProfileCursor(profileCursor{Kind: "comments", Target: p.ID, Expires: time.Now().Add(-time.Minute).Unix(), Before: at, ID: ids[19]})
	mustStatus(t, anon.request("GET", path+"?cursor="+url.QueryEscape(expired), nil, 0, ""), 410)
	page := first
	seen := map[uuid.UUID]bool{}
	index, pages := 0, 0
	for {
		pages++
		if len(page.Items) > 20 || page.ExpiresAt != first.ExpiresAt {
			t.Fatal("page bound/deadline changed")
		}
		for _, c := range page.Items {
			if seen[c.ID] || index >= len(ids) || c.ID != ids[index] {
				t.Fatal("duplicate or unstable ordering", index, c.ID)
			}
			seen[c.ID] = true
			index++
		}
		if page.NextCursor == nil {
			break
		}
		if pages > 15 {
			t.Fatal("pagination did not terminate")
		}
		r = anon.request("GET", path+"?cursor="+url.QueryEscape(*page.NextCursor), nil, 0, "")
		mustStatus(t, r, 200)
		page = parsed[commentPageResult](t, r)
	}
	if index != 220 || pages != 11 {
		t.Fatal("old 200-comment cutoff remains", index, pages)
	}
}

func TestCommentPagesRecheckBlocksAndReplyParents(t *testing.T) {
	a := testApp(t)
	viewer, writer, mod := login(t, a, 0), login(t, a, 1), login(t, a, 2)
	p := published(t, a, writer, mod, "A fictional visibility-change conversation")
	writerID, modID := myProfileID(t, writer), myProfileID(t, mod)
	at := time.Now().Add(-time.Hour).UTC().Truncate(time.Microsecond)
	threadComments(t, p.ID, writerID, nil, 0, at, 20)
	parents := threadComments(t, p.ID, modID, nil, 0, at.Add(time.Minute), 5)
	path := "posts/" + p.ID.String() + "/comments"
	r := viewer.request("GET", path, nil, 0, "")
	mustStatus(t, r, 200)
	first := parsed[commentPageResult](t, r)
	if first.NextCursor == nil {
		t.Fatal("no continuation")
	}
	next := path + "?cursor=" + url.QueryEscape(*first.NextCursor)
	flag(t, viewer, "blocks", modID, true)
	t.Cleanup(func() {
		flag(t, viewer, "blocks", modID, false)
		flag(t, mod, "blocks", myProfileID(t, viewer), false)
		flag(t, viewer, "blocks", writerID, false)
	})
	r = viewer.request("GET", next, nil, 0, "")
	mustStatus(t, r, 200)
	if len(parsed[commentPageResult](t, r).Items) != 0 {
		t.Fatal("block change did not suppress later comments", r.Body)
	}
	input := CommentInput{Body: "A known-ID reply must still check visibility", ParentID: &parents[0]}
	mustStatus(t, viewer.request("POST", path, input, 0, uuid.NewString()), 422)
	flag(t, viewer, "blocks", modID, false)
	flag(t, mod, "blocks", myProfileID(t, viewer), true)
	mustStatus(t, viewer.request("POST", path, input, 0, uuid.NewString()), 422)
	flag(t, mod, "blocks", myProfileID(t, viewer), false)
	r = viewer.request("GET", next, nil, 0, "")
	mustStatus(t, r, 200)
	if len(parsed[commentPageResult](t, r).Items) != 5 {
		t.Fatal("eligible comments did not return")
	}
	if _, e := integrationAdmin.Exec(context.Background(), `UPDATE social.profile SET state='DEACTIVATED' WHERE id=$1`, modID); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		_, _ = integrationAdmin.Exec(context.Background(), `UPDATE social.profile SET state='ACTIVE' WHERE id=$1`, modID)
	})
	mustStatus(t, viewer.request("POST", path, input, 0, uuid.NewString()), 422)
	r = viewer.request("GET", next, nil, 0, "")
	mustStatus(t, r, 200)
	if len(parsed[commentPageResult](t, r).Items) != 0 {
		t.Fatal("inactive comment author remained visible")
	}
	flag(t, viewer, "blocks", writerID, true)
	mustStatus(t, viewer.request("GET", next, nil, 0, ""), 404)
}

func TestCommentLaterPageCandidatesAndDeletedParent(t *testing.T) {
	a := testApp(t)
	owner, other, mod := login(t, a, 0), login(t, a, 1), login(t, a, 2)
	anon := client{app: a}
	p := published(t, a, owner, mod, "A fictional candidate and ancestry conversation")
	ownerID, otherID := myProfileID(t, owner), myProfileID(t, other)
	at := time.Now().Add(-time.Hour).UTC().Truncate(time.Microsecond)
	threadComments(t, p.ID, ownerID, nil, 0, at, 20)
	parent := threadComments(t, p.ID, otherID, nil, 0, at.Add(time.Minute), 1)[0]
	child := threadComments(t, p.ID, ownerID, &parent, 1, at.Add(2*time.Minute), 1)[0]
	path := "posts/" + p.ID.String() + "/comments"
	mustStatus(t, other.request("POST", path, CommentInput{Body: "PRIVATE NEW COMMENT"}, 0, uuid.NewString()), 201)
	mustStatus(t, owner.request("PATCH", "comments/"+child.String(), map[string]any{"body": "PRIVATE EDIT CANDIDATE", "languageTag": "en-IN"}, 1, ""), 200)
	mustStatus(t, other.request("DELETE", "comments/"+parent.String(), nil, 1, ""), 204)
	for _, c := range []client{anon, owner, other} {
		r := c.request("GET", path, nil, 0, "")
		mustStatus(t, r, 200)
		first := parsed[commentPageResult](t, r)
		if first.NextCursor == nil {
			t.Fatal("missing second page")
		}
		r = c.request("GET", path+"?cursor="+url.QueryEscape(*first.NextCursor), nil, 0, "")
		mustStatus(t, r, 200)
		if c.cookie == anon.cookie && strings.Contains(r.Body.String(), "PRIVATE") {
			t.Fatal("visitor saw private revisions", r.Body)
		}
		if c.cookie == owner.cookie && strings.Contains(r.Body.String(), "PRIVATE NEW COMMENT") || c.cookie == other.cookie && strings.Contains(r.Body.String(), "PRIVATE EDIT CANDIDATE") {
			t.Fatal("another author's private revision was exposed", r.Body)
		}
		for _, comment := range parsed[commentPageResult](t, r).Items {
			if comment.ID == parent && (comment.Body != nil || comment.Author != nil || comment.Candidate != nil || comment.State != "DELETED") {
				t.Fatal("deleted parent retained content", comment)
			}
			if comment.ID == child && (comment.ParentID == nil || *comment.ParentID != parent || comment.Body == nil || strings.Contains(*comment.Body, "PRIVATE")) {
				t.Fatal("published reply ancestry/body lost", comment)
			}
		}
		if !strings.Contains(r.Body.String(), child.String()) || !strings.Contains(r.Body.String(), parent.String()) {
			t.Fatal("later-page parent/reply missing")
		}
		if c.cookie == owner.cookie && !strings.Contains(r.Body.String(), "PRIVATE EDIT CANDIDATE") {
			t.Fatal("owner cannot recover private edit")
		}
		if c.cookie == other.cookie && !strings.Contains(r.Body.String(), "PRIVATE NEW COMMENT") {
			t.Fatal("owner cannot see pending comment")
		}
	}
	deepest := child
	for depth := int16(2); depth <= 20; depth++ {
		deepest = threadComments(t, p.ID, ownerID, &deepest, depth, at.Add(time.Duration(depth)*time.Minute), 1)[0]
	}
	mustStatus(t, owner.request("POST", path, CommentInput{Body: "Reply depth remains bounded", ParentID: &deepest}, 0, uuid.NewString()), 422)
}

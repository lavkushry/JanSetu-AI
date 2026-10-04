package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/lavkushry/JanSetu-AI/services/backend/internal/platform"
	"github.com/lavkushry/JanSetu-AI/services/backend/internal/store/dbgen"
	"github.com/pressly/goose/v3"
)

var integrationApp *App

func TestMain(m *testing.M) {
	if os.Getenv("JANSETU_INTEGRATION") != "1" {
		os.Exit(m.Run())
	}
	code := func() int {
		ctx := context.Background()
		cfg, e := platform.Load()
		if e != nil {
			fmt.Fprintln(os.Stderr, e)
			return 1
		}
		admin, e := pgx.Connect(ctx, cfg.DatabaseURL)
		if e != nil {
			fmt.Fprintln(os.Stderr, "start the local Postgres service before integration tests")
			return 1
		}
		defer admin.Close(ctx)
		name := "jansetu_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
		names := []string{name, name + "_vault"}
		for _, n := range names {
			if _, e = admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{n}.Sanitize()); e != nil {
				fmt.Fprintln(os.Stderr, e)
				return 1
			}
			defer admin.Exec(ctx, "DROP DATABASE "+pgx.Identifier{n}.Sanitize()+" WITH (FORCE)")
		}
		dirs := []string{"../../../../db/migrations", "../../../../db/vault"}
		dsns := []string{}
		goose.SetLogger(goose.NopLogger())
		_ = goose.SetDialect("postgres")
		for i, n := range names {
			u, _ := url.Parse(cfg.DatabaseURL)
			u.Path = "/" + n
			dsn := u.String()
			dsns = append(dsns, dsn)
			db, e := sql.Open("pgx", dsn)
			if e != nil {
				return 1
			}
			e = goose.Up(db, filepath.Clean(dirs[i]))
			db.Close()
			if e != nil {
				fmt.Fprintln(os.Stderr, e)
				return 1
			}
		}
		cfg.DatabaseURL = dsns[0]
		cfg.VaultURL = dsns[1]
		db, e := platform.Pool(ctx, cfg.DatabaseURL)
		if e != nil {
			return 1
		}
		defer db.Close()
		vault, e := platform.Pool(ctx, cfg.VaultURL)
		if e != nil {
			return 1
		}
		defer vault.Close()
		seed, e := os.ReadFile("../../../../db/seed/local.sql")
		if e != nil {
			return 1
		}
		if _, e = db.Exec(ctx, string(seed)); e != nil {
			fmt.Fprintln(os.Stderr, e)
			return 1
		}
		integrationApp = New(db, vault, cfg)
		return m.Run()
	}()
	os.Exit(code)
}
func testApp(t *testing.T) *App {
	t.Helper()
	if integrationApp == nil {
		t.Skip("set JANSETU_INTEGRATION=1 to test against isolated PostgreSQL databases")
	}
	return integrationApp
}

type client struct {
	app    *App
	cookie *http.Cookie
}

func (c client) request(method, path string, body any, version int64, key string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "/v1/"+path, bytes.NewReader(jsonBytes(body)))
	r.Header.Set("X-JanSetu-CSRF", "1")
	if key != "" {
		r.Header.Set("Idempotency-Key", key)
	}
	if version > 0 {
		r.Header.Set("If-Match", fmt.Sprintf(`"%d"`, version))
	}
	if c.cookie != nil {
		r.AddCookie(c.cookie)
	}
	w := httptest.NewRecorder()
	c.app.Handler().ServeHTTP(w, r)
	return w
}
func login(t *testing.T, a *App, index int) client {
	t.Helper()
	c := client{app: a}
	w := c.request("POST", "dev/session", map[string]any{"principalId": DemoPrincipals[index]}, 0, "")
	mustStatus(t, w, 200)
	c.cookie = w.Result().Cookies()[0]
	return c
}
func mustStatus(t *testing.T, w *httptest.ResponseRecorder, status int) {
	t.Helper()
	if w.Code != status {
		t.Fatalf("wanted %d, got %d: %s", status, w.Code, w.Body.String())
	}
}
func parsed[T any](t *testing.T, w *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if e := json.Unmarshal(w.Body.Bytes(), &v); e != nil {
		t.Fatal(e)
	}
	return v
}

type testPost struct {
	ID              uuid.UUID `json:"id"`
	State           string    `json:"state"`
	Version         int64     `json:"version"`
	CurrentRevision int64     `json:"currentRevision"`
	Body            string    `json:"body"`
}
type reviewItem struct {
	ID             uuid.UUID  `json:"id"`
	PostID         *uuid.UUID `json:"postId"`
	CommentID      *uuid.UUID `json:"commentId"`
	Version        int64      `json:"version"`
	TargetRevision int64      `json:"targetRevision"`
}

func review(t *testing.T, moderator client, post, comment uuid.UUID, revision int64) {
	t.Helper()
	w := moderator.request("GET", "moderation", nil, 0, "")
	mustStatus(t, w, 200)
	items := parsed[struct {
		Items []reviewItem `json:"items"`
	}](t, w)
	for _, m := range items.Items {
		if (m.PostID != nil && *m.PostID == post || m.CommentID != nil && *m.CommentID == comment) && m.TargetRevision == revision {
			w = moderator.request("POST", "moderation/"+m.ID.String()+"/decisions", map[string]any{"action": "ALLOW", "reason": "Constructive synthetic community discussion", "targetRevision": revision}, m.Version, "")
			mustStatus(t, w, 200)
			return
		}
	}
	t.Fatal("review not found")
}
func published(t *testing.T, a *App, owner, moderator client, body string) testPost {
	t.Helper()
	community := uuid.MustParse("50000000-0000-4000-8000-000000000001")
	w := owner.request("POST", "posts", PostInput{Kind: "DISCUSSION", CommunityID: &community, Title: ptr("A synthetic community discussion"), Body: body, LanguageTag: "en-IN", SubmitForReview: true}, 0, uuid.NewString())
	mustStatus(t, w, 201)
	p := parsed[testPost](t, w)
	review(t, moderator, p.ID, uuid.Nil, 1)
	w = owner.request("GET", "posts/"+p.ID.String(), nil, 0, "")
	mustStatus(t, w, 200)
	return parsed[testPost](t, w)
}

func TestSocialPublicationAndDesiredState(t *testing.T) {
	a := testApp(t)
	owner := login(t, a, 0)
	resident := login(t, a, 1)
	mod := login(t, a, 2)
	anon := client{app: a}
	community := uuid.MustParse("50000000-0000-4000-8000-000000000001")
	body := PostInput{Kind: "DISCUSSION", CommunityID: &community, Title: ptr("Concurrency test discussion"), Body: "A fictional discussion for our neighbourhood.", SubmitForReview: true}
	key := uuid.NewString()
	responses := make(chan *httptest.ResponseRecorder, 8)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); responses <- owner.request("POST", "posts", body, 0, key) }()
	}
	wg.Wait()
	close(responses)
	var p testPost
	for w := range responses {
		mustStatus(t, w, 201)
		next := parsed[testPost](t, w)
		if p.ID != uuid.Nil && next.ID != p.ID {
			t.Fatal("duplicate creation")
		}
		p = next
	}
	mustStatus(t, anon.request("GET", "posts/"+p.ID.String(), nil, 0, ""), 404)
	changed := body
	changed.Body = "Different content using the same key"
	mustStatus(t, owner.request("POST", "posts", changed, 0, key), 409)
	edit := map[string]any{"title": "Updated discussion", "body": "An improved fictional discussion.", "languageTag": "en-IN", "mediaIds": []string{}, "submitForReview": true}
	w := owner.request("PATCH", "posts/"+p.ID.String(), edit, p.Version, "")
	mustStatus(t, w, 200)
	p = parsed[testPost](t, w)
	queue := parsed[struct {
		Items []reviewItem `json:"items"`
	}](t, mod.request("GET", "moderation", nil, 0, ""))
	for _, m := range queue.Items {
		if m.PostID != nil && *m.PostID == p.ID && m.TargetRevision == 1 {
			mustStatus(t, mod.request("POST", "moderation/"+m.ID.String()+"/decisions", map[string]any{"action": "ALLOW", "reason": "Obsolete revision must be rejected", "targetRevision": 1}, m.Version, ""), 409)
		}
	}
	review(t, mod, p.ID, uuid.Nil, 2)
	w = anon.request("GET", "posts/"+p.ID.String(), nil, 0, "")
	mustStatus(t, w, 200)
	p = parsed[testPost](t, w)
	if p.Body != "An improved fictional discussion." {
		t.Fatal("wrong published revision")
	}
	edit["body"] = "Third revision remains private until approved."
	mustStatus(t, owner.request("PATCH", "posts/"+p.ID.String(), edit, p.Version, ""), 200)
	visible := parsed[testPost](t, anon.request("GET", "posts/"+p.ID.String(), nil, 0, ""))
	if visible.Body != p.Body {
		t.Fatal("unapproved edit leaked")
	}
	mustStatus(t, owner.request("PATCH", "posts/"+p.ID.String(), edit, p.Version, ""), 412)
	mustStatus(t, owner.request("PUT", "posts/"+p.ID.String()+"/vote", map[string]any{"value": 1}, 0, ""), 403)
	for i := 0; i < 3; i++ {
		mustStatus(t, resident.request("PUT", "posts/"+p.ID.String()+"/vote", map[string]any{"value": 1}, 0, ""), 200)
	}
	mustStatus(t, resident.request("PUT", "posts/"+p.ID.String()+"/bookmark", map[string]any{"enabled": true}, 0, ""), 200)
	saved := resident.request("GET", "me/bookmarks", nil, 0, "")
	mustStatus(t, saved, 200)
	if !strings.Contains(saved.Body.String(), p.ID.String()) {
		t.Fatal("bookmark missing")
	}
	for i := 0; i < 200; i++ {
		worked, e := a.ProjectOnce(context.Background(), "test-worker")
		if e != nil {
			t.Fatal(e)
		}
		if !worked {
			break
		}
	}
	var count int
	if e := a.DB.QueryRow(context.Background(), "SELECT up_count FROM social.post_stats WHERE post_id=$1", p.ID).Scan(&count); e != nil || count != 1 {
		t.Fatalf("desired vote count: %d %v", count, e)
	}
	mustStatus(t, resident.request("PUT", "posts/"+p.ID.String()+"/vote", map[string]any{"value": 0}, 0, ""), 200)
	mustStatus(t, resident.request("PUT", "me/blocks/"+ownerProfile().String(), map[string]any{"enabled": true}, 0, ""), 200)
	mustStatus(t, resident.request("GET", "posts/"+p.ID.String(), nil, 0, ""), 404)
	mustStatus(t, resident.request("PUT", "posts/"+p.ID.String()+"/vote", map[string]any{"value": 0}, 0, ""), 200)
	mustStatus(t, resident.request("PUT", "posts/"+p.ID.String()+"/bookmark", map[string]any{"enabled": false}, 0, ""), 200)
	mustStatus(t, resident.request("PUT", "me/following/"+ownerProfile().String(), map[string]any{"enabled": true}, 0, ""), 403)
	mustStatus(t, resident.request("PUT", "me/blocks/"+ownerProfile().String(), map[string]any{"enabled": false}, 0, ""), 200)
}
func ownerProfile() uuid.UUID { return uuid.MustParse("20000000-0000-4000-8000-000000000001") }
func TestNullablePublishedTitleDoesNotExposeEdit(t *testing.T) {
	a := testApp(t)
	owner := login(t, a, 0)
	mod := login(t, a, 2)
	w := owner.request("POST", "posts", PostInput{Kind: "SHORT", Body: "A public fictional short update", SubmitForReview: true}, 0, uuid.NewString())
	mustStatus(t, w, 201)
	p := parsed[testPost](t, w)
	review(t, mod, p.ID, uuid.Nil, 1)
	p = parsed[testPost](t, owner.request("GET", "posts/"+p.ID.String(), nil, 0, ""))
	w = owner.request("PATCH", "posts/"+p.ID.String(), map[string]any{"title": "A private unapproved title", "body": "A private unapproved body", "languageTag": "en-IN", "mediaIds": []string{}, "submitForReview": true}, p.Version, "")
	mustStatus(t, w, 200)
	w = (client{app: a}).request("GET", "posts/"+p.ID.String(), nil, 0, "")
	mustStatus(t, w, 200)
	visible := parsed[struct {
		Title *string
		Body  string
	}](t, w)
	if visible.Title != nil || visible.Body != "A public fictional short update" {
		t.Fatal("nullable public title fell back to private candidate")
	}
}
func TestCommentAncestryAndTombstones(t *testing.T) {
	a := testApp(t)
	owner := login(t, a, 0)
	other := login(t, a, 1)
	mod := login(t, a, 2)
	p := published(t, a, owner, mod, "A fictional post for comment boundaries.")
	otherPost := published(t, a, owner, mod, "Another fictional post for comment boundaries.")
	w := other.request("POST", "posts/"+p.ID.String()+"/comments", CommentInput{Body: "A constructive fictional comment", LanguageTag: "en-IN"}, 0, uuid.NewString())
	mustStatus(t, w, 201)
	comment := parsed[struct {
		ID      uuid.UUID
		Version int64
	}](t, w)
	review(t, mod, uuid.Nil, comment.ID, 1)
	mustStatus(t, owner.request("POST", "posts/"+otherPost.ID.String()+"/comments", CommentInput{Body: "Cross-post reply must fail", ParentID: &comment.ID}, 0, uuid.NewString()), 422)
	w = owner.request("POST", "posts/"+p.ID.String()+"/comments", CommentInput{Body: "A child reply preserves its parent", ParentID: &comment.ID}, 0, uuid.NewString())
	mustStatus(t, w, 201)
	child := parsed[struct{ ID uuid.UUID }](t, w)
	review(t, mod, uuid.Nil, child.ID, 1)
	mustStatus(t, other.request("DELETE", "comments/"+comment.ID.String(), nil, 2, ""), 204)
	mustStatus(t, owner.request("POST", "posts/"+p.ID.String()+"/comments", CommentInput{Body: "Deleted parents cannot receive new replies", ParentID: &comment.ID}, 0, uuid.NewString()), 422)
	w = other.request("GET", "posts/"+p.ID.String()+"/comments", nil, 0, "")
	mustStatus(t, w, 200)
	if !strings.Contains(w.Body.String(), child.ID.String()) || !strings.Contains(w.Body.String(), `"DELETED"`) {
		t.Fatal("tombstone lost thread descendants")
	}
}

func TestPrivateReportAndStaffLifecycle(t *testing.T) {
	a := testApp(t)
	resident := login(t, a, 0)
	stranger := login(t, a, 1)
	coordinator := login(t, a, 2)
	officer := login(t, a, 3)
	verifier := login(t, a, 4)
	input := ReportInput{ClientSubmissionID: uuid.New(), Statement: "A fictional broken footpath. Private marker " + uuid.NewString(), LocationLabel: "Fictional crossing near 12th Main", Category: "FOOTPATH", LanguageTag: "en-IN", PublicationPreference: "SANITIZED_RECEIPT"}
	w := resident.request("POST", "service-reports", input, 0, uuid.NewString())
	mustStatus(t, w, 201)
	ack := parsed[struct {
		ID         uuid.UUID
		ReceivedAt string
	}](t, w)
	late := resident.request("POST", "service-reports", input, 0, uuid.NewString())
	mustStatus(t, late, 201)
	if parsed[struct{ ID uuid.UUID }](t, late).ID != ack.ID {
		t.Fatal("lifetime submission dedupe failed")
	}
	changed := input
	changed.Statement += " Changed"
	mustStatus(t, resident.request("POST", "service-reports", changed, 0, uuid.NewString()), 409)
	mustStatus(t, stranger.request("GET", "my-reports/"+ack.ID.String(), nil, 0, ""), 404)
	mustStatus(t, resident.request("GET", "my-reports/"+ack.ID.String(), nil, 0, ""), 200)
	agency := uuid.MustParse("30000000-0000-4000-8000-000000000001")
	triage := map[string]any{"agencyId": agency, "category": "FOOTPATH", "urgencyTier": 2, "reason": "Synthetic accessibility issue requires restoration"}
	mustStatus(t, resident.request("POST", "authority/reports/"+ack.ID.String()+"/triage", triage, 1, ""), 403)
	w = coordinator.request("POST", "authority/reports/"+ack.ID.String()+"/triage", triage, 1, "")
	mustStatus(t, w, 201)
	created := parsed[struct {
		CaseID          uuid.UUID
		FirstReportedAt string
	}](t, w)
	if created.FirstReportedAt != ack.ReceivedAt {
		t.Fatal("triage reset the original report age")
	}
	cid := created.CaseID
	publication := map[string]any{"title": "Footpath access near the neighbourhood crossing", "summary": "An accessibility issue was assessed and a restoration task was proposed.", "area": "Indiranagar", "reviewed": true}
	w = coordinator.request("POST", "authority/cases/"+cid.String()+"/publications", publication, 1, "")
	mustStatus(t, w, 200)
	rid := parsed[struct{ ReceiptID uuid.UUID }](t, w).ReceiptID
	if rid == cid {
		t.Fatal("public receipt shares operational ID")
	}
	detail := func(c client) struct {
		Version     int64
		State       string
		Obligations []struct {
			ID      uuid.UUID
			Version int64
			State   string
		}
	} {
		w := c.request("GET", "authority/cases/"+cid.String(), nil, 0, "")
		mustStatus(t, w, 200)
		return parsed[struct {
			Version     int64
			State       string
			Obligations []struct {
				ID      uuid.UUID
				Version int64
				State   string
			}
		}](t, w)
	}
	for _, route := range []string{"accept", "start", "completion-claims"} {
		d := detail(officer)
		o := d.Obligations[0]
		w = officer.request("POST", "authority/obligations/"+o.ID.String()+"/"+route, map[string]any{"summary": "Synthetic work performed and recorded by the agency"}, o.Version, "")
		mustStatus(t, w, 200)
	}
	d := detail(verifier)
	if d.State != "VERIFICATION_PENDING" {
		t.Fatal("completion claim prematurely resolved case")
	}
	public := resident.request("GET", "case-receipts/"+rid.String(), nil, 0, "")
	mustStatus(t, public, 200)
	if parsed[struct{ State string }](t, public).State != "OPEN" {
		t.Fatal("operational state bypassed publication review")
	}
	verification := map[string]any{"obligationId": d.Obligations[0].ID, "result": "VERIFIED", "reason": "Independent fictional inspection confirmed restored pedestrian access"}
	mustStatus(t, officer.request("POST", "authority/cases/"+cid.String()+"/verification-decisions", verification, d.Version, ""), 403)
	mustStatus(t, verifier.request("POST", "authority/cases/"+cid.String()+"/verification-decisions", verification, d.Version, ""), 200)
	d = detail(coordinator)
	if d.State != "RESOLVED" {
		t.Fatal("verified task did not resolve case")
	}
	mustStatus(t, coordinator.request("POST", "authority/cases/"+cid.String()+"/publications", publication, d.Version, ""), 200)
	public = resident.request("GET", "case-receipts/"+rid.String(), nil, 0, "")
	mustStatus(t, public, 200)
	if !strings.Contains(public.Body.String(), `"RESOLVED"`) || strings.Contains(public.Body.String(), input.Statement) || strings.Contains(public.Body.String(), cid.String()) || strings.Contains(public.Body.String(), DemoPrincipals[0].String()) {
		t.Fatal("public projection leaked private details or wrong progress")
	}
	input.ClientSubmissionID = uuid.New()
	input.PublicationPreference = "PRIVATE"
	w = resident.request("POST", "service-reports", input, 0, uuid.NewString())
	mustStatus(t, w, 201)
	privateID := parsed[struct{ ID uuid.UUID }](t, w).ID
	w = coordinator.request("POST", "authority/reports/"+privateID.String()+"/triage", triage, 1, "")
	mustStatus(t, w, 201)
	privateCase := parsed[struct{ CaseID uuid.UUID }](t, w).CaseID
	mustStatus(t, coordinator.request("POST", "authority/cases/"+privateCase.String()+"/publications", publication, 1, ""), 403)
}
func TestAuthorizationRevocationAndLeaseFencing(t *testing.T) {
	a := testApp(t)
	mod := login(t, a, 2)
	ctx := context.Background()
	_, e := a.DB.Exec(ctx, "UPDATE identity.platform_grant SET revoked_at=now() WHERE principal_id=$1 AND role='PLATFORM_MODERATOR'", DemoPrincipals[2])
	if e != nil {
		t.Fatal(e)
	}
	mustStatus(t, mod.request("GET", "moderation", nil, 0, ""), 403)
	_, _ = a.DB.Exec(ctx, "UPDATE identity.platform_grant SET revoked_at=NULL WHERE principal_id=$1 AND role='PLATFORM_MODERATOR'", DemoPrincipals[2])
	q := dbgen.New(a.DB)
	eid := uuid.New()
	if e = addEvent(ctx, q, "CASE", uuid.New(), 1, "SyntheticFenceTest", map[string]any{}); e != nil {
		t.Fatal(e)
	}
	oldToken := uuid.New()
	owner := pgtype.Text{String: "old-worker", Valid: true}
	claim, e := q.ClaimEvent(ctx, dbgen.ClaimEventParams{LeaseOwner: owner, LeaseToken: &oldToken})
	if e != nil {
		t.Fatal(e)
	}
	eid = claim.ID
	_, _ = a.DB.Exec(ctx, "UPDATE infra.outbox SET lease_until=now()-interval '1 second' WHERE id=$1", eid)
	freshToken := uuid.New()
	freshOwner := pgtype.Text{String: "new-worker", Valid: true}
	_, e = q.ClaimEvent(ctx, dbgen.ClaimEventParams{LeaseOwner: freshOwner, LeaseToken: &freshToken})
	if e != nil {
		t.Fatal(e)
	}
	_, e = q.LockClaim(ctx, dbgen.LockClaimParams{ID: eid, LeaseToken: &oldToken, LeaseOwner: owner})
	if e != pgx.ErrNoRows {
		t.Fatal("stale worker retained lease authority")
	}
	if e = q.CompleteEvent(ctx, dbgen.CompleteEventParams{ID: eid, LeaseToken: &oldToken, LeaseOwner: owner}); e != nil {
		t.Fatal(e)
	}
	var delivered bool
	if e = a.DB.QueryRow(ctx, "SELECT delivered_at IS NOT NULL FROM infra.outbox WHERE id=$1", eid).Scan(&delivered); e != nil || delivered {
		t.Fatal("stale worker acknowledged reclaimed event")
	}
}

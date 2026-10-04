package app

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/lavkushry/JanSetu-AI/services/backend/internal/store/dbgen"
)

type publicProfileView struct {
	ID          uuid.UUID `json:"id"`
	Handle      string    `json:"handle"`
	DisplayName string    `json:"displayName"`
	Bio         string    `json:"bio"`
	JoinedAt    time.Time `json:"joinedAt"`
}

func publicProfileViewOf(p dbgen.PublicProfileRow) publicProfileView {
	return publicProfileView{ID: p.ID, Handle: p.Handle, DisplayName: p.DisplayName, Bio: p.Bio, JoinedAt: p.CreatedAt.Time}
}
func (a *App) publicProfile(w http.ResponseWriter, r *http.Request, actor *Actor) (any, int, error) {
	pid, e := id(r, "id")
	if e != nil {
		return nil, 0, e
	}
	p, e := dbgen.New(a.store(r.Context())).PublicProfile(r.Context(), dbgen.PublicProfileParams{ProfileID: pid, ViewerID: actorID(actor)})
	if e != nil {
		return nil, 0, e
	}
	return map[string]any{"profile": publicProfileViewOf(p), "viewer": map[string]any{"self": pid == actorID(actor), "following": p.Following, "muted": p.Muted}}, 200, nil
}

// Compact keyset cursors bind the owner/viewer, endpoint, target and deadline.
// Unlike feed snapshots, profile and block pages have no total-row cap.
type profileCursor struct {
	Viewer  uuid.UUID `json:"v"`
	Kind    string    `json:"k"`
	Target  uuid.UUID `json:"t"`
	Expires int64     `json:"e"`
	Before  time.Time `json:"b"`
	ID      uuid.UUID `json:"i"`
}

func (a *App) encodeProfileCursor(c profileCursor) string {
	b := jsonBytes(c)
	h := hmac.New(sha256.New, a.cursorKey)
	h.Write(b)
	return base64.RawURLEncoding.EncodeToString(b) + "." + base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}
func (a *App) profilePageCursor(raw, kind string, viewer, target uuid.UUID) (profileCursor, error) {
	c := profileCursor{Viewer: viewer, Kind: kind, Target: target, Expires: time.Now().Add(5 * time.Minute).Unix()}
	if raw == "" {
		return c, nil
	}
	if len(raw) > 1000 {
		return c, invalid("Invalid page cursor")
	}
	parts := strings.Split(raw, ".")
	if len(parts) != 2 {
		return c, invalid("Invalid page cursor")
	}
	b, e := base64.RawURLEncoding.DecodeString(parts[0])
	if e != nil {
		return c, invalid("Invalid page cursor")
	}
	sig, e := base64.RawURLEncoding.DecodeString(parts[1])
	if e != nil {
		return c, invalid("Invalid page cursor")
	}
	h := hmac.New(sha256.New, a.cursorKey)
	h.Write(b)
	if !hmac.Equal(sig, h.Sum(nil)) || json.Unmarshal(b, &c) != nil || c.Kind != kind || c.Viewer != viewer || c.Target != target || c.Before.IsZero() || c.ID == uuid.Nil {
		return c, invalid("Invalid page cursor")
	}
	if c.Expires <= time.Now().Unix() {
		return c, failure(410, "CURSOR_EXPIRED", "Refresh this page to continue")
	}
	return c, nil
}
func (a *App) profilePosts(w http.ResponseWriter, r *http.Request, actor *Actor) (any, int, error) {
	pid, e := id(r, "id")
	if e != nil {
		return nil, 0, e
	}
	c, e := a.profilePageCursor(r.URL.Query().Get("cursor"), "profile-posts", actorID(actor), pid)
	if e != nil {
		return nil, 0, e
	}
	q := dbgen.New(a.store(r.Context()))
	if _, e = q.PublicProfile(r.Context(), dbgen.PublicProfileParams{ProfileID: pid, ViewerID: actorID(actor)}); e != nil {
		return nil, 0, e
	}
	rows, e := q.ProfilePostPage(r.Context(), dbgen.ProfilePostPageParams{ProfileID: &pid, ViewerID: actorID(actor), HasCursor: !c.Before.IsZero(), BeforeTime: pgtype.Timestamptz{Time: c.Before, Valid: true}, BeforeID: c.ID})
	if e != nil {
		return nil, 0, e
	}
	items := []any{}
	for _, p := range rows[:min(20, len(rows))] {
		data, err := postData(r.Context(), q, p.ID, actor, false)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, 0, err
		}
		var post map[string]any
		if err = json.Unmarshal(data, &post); err != nil {
			return nil, 0, err
		}
		// Hydration rechecks publication and blocks. A public timeline excludes
		// private edit candidates even when its author views their own profile.
		author, ok := post["author"].(map[string]any)
		if post["state"] != "PUBLISHED" || !ok || author["id"] != pid.String() {
			continue
		}
		post["candidate"] = nil
		items = append(items, post)
	}
	var next any
	if len(rows) > 20 {
		last := rows[19]
		c.Before = last.PublishedAt.Time
		c.ID = last.ID
		next = a.encodeProfileCursor(c)
	}
	return map[string]any{"items": items, "nextCursor": next, "expiresAt": time.Unix(c.Expires, 0).UTC().Format(time.RFC3339)}, 200, nil
}
func (a *App) blockedPeople(w http.ResponseWriter, r *http.Request, actor *Actor) (any, int, error) {
	if e := require(actor); e != nil {
		return nil, 0, e
	}
	c, e := a.profilePageCursor(r.URL.Query().Get("cursor"), "blocks", actor.ProfileID, actor.ProfileID)
	if e != nil {
		return nil, 0, e
	}
	rows, e := dbgen.New(a.store(r.Context())).BlockedPeoplePage(r.Context(), dbgen.BlockedPeoplePageParams{ViewerID: actor.ProfileID, HasCursor: !c.Before.IsZero(), BeforeTime: pgtype.Timestamptz{Time: c.Before, Valid: true}, BeforeID: c.ID})
	if e != nil {
		return nil, 0, e
	}
	items := []any{}
	for _, b := range rows[:min(20, len(rows))] {
		var profile any
		if b.Available {
			profile = map[string]any{"id": b.BlockedID, "handle": b.Handle, "displayName": b.DisplayName}
		}
		items = append(items, map[string]any{"profileId": b.BlockedID, "profile": profile, "blockedAt": b.CreatedAt.Time})
	}
	var next any
	if len(rows) > 20 {
		last := rows[19]
		c.Before = last.CreatedAt.Time
		c.ID = last.BlockedID
		next = a.encodeProfileCursor(c)
	}
	return map[string]any{"items": items, "nextCursor": next, "expiresAt": time.Unix(c.Expires, 0).UTC().Format(time.RFC3339)}, 200, nil
}

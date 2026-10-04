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
	"github.com/lavkushry/JanSetu-AI/services/backend/internal/store/dbgen"
)

func (a *App) capabilities(w http.ResponseWriter, r *http.Request, _ *Actor) (any, int, error) {
	visionStatus, visionNote := "PLANNED", "Image recognition is not enabled."
	if a.Config.VisionBinary != "" {
		visionStatus, visionNote = "EVALUATING", "Local YOLOX object candidates only. Potholes, leaks, waste, damage, and agency routing are unsupported. Review results yourself."
	}
	return map[string]any{"synthetic": true, "mediaUpload": a.Media != nil && a.Files != nil, "ocr": false,
		"uploads": map[string]any{"mimeTypes": []string{"image/jpeg", "image/png", "image/webp"}, "maxBytes": 10485760, "maxAttachments": 4, "maxPixels": 12000000, "purpose": "REPORT", "adapter": "LOCAL_PRIVATE_MULTIPART", "partSize": 2097152, "maxParts": 5},
		"analysisCapabilities": []any{
			map[string]any{"kind": "OCR", "status": "EVALUATING", "languageTags": []string{"en-IN", "en-US", "en-GB", "en"}, "note": "Local Tesseract English preview; review every word. Production language evaluation is pending."},
			map[string]any{"kind": "QUALITY", "status": "EVALUATING", "languageTags": []string{}, "note": "Resolution check only; no blur, lighting, or truth assessment."},
			map[string]any{"kind": "ISSUE_DETECTION", "status": visionStatus, "languageTags": []string{}, "note": visionNote},
			map[string]any{"kind": "REDACTION", "status": "PLANNED", "languageTags": []string{}, "note": "Private photos are not approved for public publication."}}, "voice": false, "protectedIntake": false, "uiLanguages": []string{"en-IN"}, "textLanguages": "Unicode text accepted; language-specific AI readiness is not claimed"}, 200, nil
}
func communityJSON(c dbgen.CommunitiesRow) map[string]any {
	return map[string]any{"id": c.ID, "slug": c.Slug, "title": c.Title, "description": c.Description, "languageTag": c.LanguageTag, "visibility": c.Visibility, "state": c.State, "rules": c.RulesBody, "rulesRevision": c.RulesRevision, "members": c.Members, "following": c.Following, "membershipState": c.MembershipState, "version": c.Version}
}
func (a *App) communities(w http.ResponseWriter, r *http.Request, actor *Actor) (any, int, error) {
	rows, e := dbgen.New(a.store(r.Context())).Communities(r.Context(), actorID(actor))
	items := []any{}
	for _, c := range rows {
		items = append(items, communityJSON(c))
	}
	return map[string]any{"items": items}, 200, e
}
func (a *App) community(w http.ResponseWriter, r *http.Request, actor *Actor) (any, int, error) {
	cid, e := id(r, "id")
	if e != nil {
		return nil, 0, e
	}
	rows, e := dbgen.New(a.store(r.Context())).Communities(r.Context(), actorID(actor))
	if e != nil {
		return nil, 0, e
	}
	for _, c := range rows {
		if c.ID == cid {
			return communityJSON(c), 200, nil
		}
	}
	return nil, 0, unavailable()
}
func (a *App) membership(w http.ResponseWriter, r *http.Request, actor *Actor) (any, int, error) {
	cid, e := id(r, "id")
	if e != nil {
		return nil, 0, e
	}
	var b struct {
		Joined        bool  `json:"joined"`
		RulesRevision int64 `json:"rulesRevision"`
	}
	if e = decodeRequired(r, &b, "joined"); e != nil {
		return nil, 0, e
	}
	e = a.transaction(r.Context(), actor, func(q *dbgen.Queries) error {
		c, e := q.LockCommunity(r.Context(), cid)
		if e != nil {
			return unavailable()
		}
		if c.State != "ACTIVE" || c.Visibility == "PRIVATE" {
			return forbidden()
		}
		if b.Joined && b.RulesRevision != int64(c.RulesRevision) {
			return conflict()
		}
		existing, err := q.Membership(r.Context(), dbgen.MembershipParams{CommunityID: cid, ProfileID: actor.ProfileID})
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if existing.State == "BANNED" {
			return forbidden()
		}
		state := "LEFT"
		if b.Joined {
			state = "ACTIVE"
		}
		return q.SetMembership(r.Context(), dbgen.SetMembershipParams{CommunityID: cid, ProfileID: actor.ProfileID, Role: "MEMBER", State: state})
	})
	return map[string]any{"joined": b.Joined}, 200, e
}
func (a *App) communityFollow(w http.ResponseWriter, r *http.Request, actor *Actor) (any, int, error) {
	cid, e := id(r, "id")
	if e != nil {
		return nil, 0, e
	}
	var b struct {
		Following bool `json:"following"`
	}
	if e = decodeRequired(r, &b, "following"); e != nil {
		return nil, 0, e
	}
	e = a.transaction(r.Context(), actor, func(q *dbgen.Queries) error {
		c, e := q.LockCommunity(r.Context(), cid)
		if e != nil {
			return unavailable()
		}
		if c.State != "ACTIVE" || c.Visibility == "PRIVATE" {
			return forbidden()
		}
		if b.Following {
			return q.SetCommunityFollow(r.Context(), dbgen.SetCommunityFollowParams{ProfileID: actor.ProfileID, CommunityID: cid})
		}
		return q.DeleteCommunityFollow(r.Context(), dbgen.DeleteCommunityFollowParams{ProfileID: actor.ProfileID, CommunityID: cid})
	})
	return map[string]any{"following": b.Following}, 200, e
}

type feedRef struct {
	Type string    `json:"t"`
	ID   uuid.UUID `json:"i"`
}
type feedCursor struct {
	Viewer  uuid.UUID `json:"v"`
	Query   string    `json:"q"`
	Expires int64     `json:"e"`
	Offset  int       `json:"o"`
	Refs    []feedRef `json:"r"`
}

func (a *App) encodeCursor(c feedCursor) string {
	data := jsonBytes(c)
	m := hmac.New(sha256.New, a.cursorKey)
	m.Write(data)
	return base64.RawURLEncoding.EncodeToString(data) + "." + base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}
func (a *App) decodeCursor(raw string, viewer uuid.UUID, query string) (feedCursor, error) {
	var c feedCursor
	if len(raw) > 40000 {
		return c, invalid("Invalid feed cursor")
	}
	parts := strings.Split(raw, ".")
	if len(parts) != 2 {
		return c, invalid("Invalid feed cursor")
	}
	b, e := base64.RawURLEncoding.DecodeString(parts[0])
	if e != nil {
		return c, invalid("Invalid feed cursor")
	}
	sig, e := base64.RawURLEncoding.DecodeString(parts[1])
	if e != nil {
		return c, invalid("Invalid feed cursor")
	}
	m := hmac.New(sha256.New, a.cursorKey)
	m.Write(b)
	if !hmac.Equal(sig, m.Sum(nil)) || json.Unmarshal(b, &c) != nil || c.Viewer != viewer || c.Query != query || c.Offset < 0 || c.Offset > len(c.Refs) || len(c.Refs) > 400 {
		return c, invalid("Invalid feed cursor")
	}
	if c.Expires < time.Now().Unix() {
		return c, failure(410, "CURSOR_EXPIRED", "Refresh the feed to continue")
	}
	return c, nil
}
func (a *App) feed(w http.ResponseWriter, r *http.Request, actor *Actor) (any, int, error) {
	return a.feedPage(r, actor, false)
}
func (a *App) bookmarks(w http.ResponseWriter, r *http.Request, actor *Actor) (any, int, error) {
	if e := require(actor); e != nil {
		return nil, 0, e
	}
	return a.feedPage(r, actor, true)
}
func (a *App) feedPage(r *http.Request, actor *Actor, saved bool) (any, int, error) {
	mode := strings.ToUpper(r.URL.Query().Get("mode"))
	if mode == "" {
		mode = "HOME"
	}
	if mode != "HOME" && mode != "FOLLOWING" && mode != "NEARBY" && mode != "UNRESOLVED" && mode != "RESOLVED" {
		return nil, 0, invalid("Choose a supported feed")
	}
	if mode == "FOLLOWING" {
		if e := require(actor); e != nil {
			return nil, 0, e
		}
	}
	sortMode := r.URL.Query().Get("sort")
	if sortMode != "" && sortMode != "new" && sortMode != "top" {
		return nil, 0, invalid("Choose a supported order")
	}
	cid := uuid.Nil
	var e error
	if s := r.URL.Query().Get("communityId"); s != "" {
		cid, e = uuid.Parse(s)
		if e != nil {
			return nil, 0, invalid("Invalid community")
		}
	}
	query := mode + "|" + sortMode + "|" + cid.String()
	if saved {
		query += "|saved"
	}
	q := dbgen.New(a.store(r.Context()))
	var c feedCursor
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		c, e = a.decodeCursor(raw, actorID(actor), query)
		if e != nil {
			return nil, 0, e
		}
	} else {
		c = feedCursor{Viewer: actorID(actor), Query: query, Expires: time.Now().Add(5 * time.Minute).Unix(), Refs: []feedRef{}}
		posts := []uuid.UUID{}
		if mode != "UNRESOLVED" && mode != "RESOLVED" {
			posts, e = q.FeedPostIDs(r.Context(), dbgen.FeedPostIDsParams{CommunityID: cid, ViewerID: actorID(actor), SavedOnly: saved, FollowingOnly: mode == "FOLLOWING", SearchText: "", SortTop: sortMode == "top"})
			if e != nil {
				return nil, 0, e
			}
		}
		receipts := []dbgen.SocialCaseReceipt{}
		if !saved && cid == uuid.Nil {
			receipts, e = q.Receipts(r.Context(), "")
			if e != nil {
				return nil, 0, e
			}
		}
		ri := 0
		eligible := []dbgen.SocialCaseReceipt{}
		for _, v := range receipts {
			if mode == "UNRESOLVED" && v.PublicState == "RESOLVED" || mode == "RESOLVED" && v.PublicState != "RESOLVED" {
				continue
			}
			if mode == "FOLLOWING" {
				yes, err := q.CaseFollowing(r.Context(), dbgen.CaseFollowingParams{ProfileID: actorID(actor), ReceiptID: v.ID})
				if err != nil {
					return nil, 0, err
				}
				if !yes {
					continue
				}
			}
			eligible = append(eligible, v)
		}
		// Community votes affect social post ordering only. Service progress retains
		// urgency-first, oldest-report-first order in its dedicated feeds.
		for i, p := range posts {
			c.Refs = append(c.Refs, feedRef{"POST", p})
			if i%3 == 2 && ri < len(eligible) {
				c.Refs = append(c.Refs, feedRef{"CASE_RECEIPT", eligible[ri].ID})
				ri++
			}
		}
		for ; ri < len(eligible); ri++ {
			c.Refs = append(c.Refs, feedRef{"CASE_RECEIPT", eligible[ri].ID})
		}
	}
	items := []any{}
	for c.Offset < len(c.Refs) && len(items) < 20 {
		ref := c.Refs[c.Offset]
		c.Offset++
		if ref.Type == "POST" {
			data, err := postData(r.Context(), q, ref.ID, actor, false)
			if errors.Is(err, pgx.ErrNoRows) {
				continue
			}
			if err != nil {
				return nil, 0, err
			}
			var post struct {
				State string `json:"state"`
			}
			if err = json.Unmarshal(data, &post); err != nil {
				return nil, 0, err
			}
			if post.State != "PUBLISHED" {
				continue
			}
			items = append(items, map[string]any{"type": "POST", "post": data})
		} else {
			data, err := a.receiptData(r, q, ref.ID, actor, false)
			if errors.Is(err, pgx.ErrNoRows) {
				continue
			}
			if err != nil {
				return nil, 0, err
			}
			items = append(items, map[string]any{"type": "CASE_RECEIPT", "receipt": data})
		}
	}
	var next any
	if c.Offset < len(c.Refs) {
		next = a.encodeCursor(c)
	}
	return map[string]any{"items": items, "nextCursor": next, "expiresAt": time.Unix(c.Expires, 0).UTC().Format(time.RFC3339), "mode": mode}, 200, nil
}
func (a *App) search(w http.ResponseWriter, r *http.Request, actor *Actor) (any, int, error) {
	term := strings.TrimSpace(r.URL.Query().Get("q"))
	if !textValid(term, 2, 120) {
		return nil, 0, invalid("Search with 2 to 120 characters")
	}
	q := dbgen.New(a.store(r.Context()))
	ids, e := q.FeedPostIDs(r.Context(), dbgen.FeedPostIDsParams{ViewerID: actorID(actor), CommunityID: uuid.Nil, SearchText: term})
	if e != nil {
		return nil, 0, e
	}
	items := []any{}
	for _, pid := range ids {
		p, e := postData(r.Context(), q, pid, actor, false)
		if errors.Is(e, pgx.ErrNoRows) {
			continue
		}
		if e != nil {
			return nil, 0, e
		}
		items = append(items, map[string]any{"type": "POST", "post": p})
	}
	receipts, e := q.Receipts(r.Context(), term)
	if e != nil {
		return nil, 0, e
	}
	for _, v := range receipts {
		d, e := a.receiptData(r, q, v.ID, actor, false)
		if e != nil {
			return nil, 0, e
		}
		items = append(items, map[string]any{"type": "CASE_RECEIPT", "receipt": d})
	}
	communities, e := q.Communities(r.Context(), actorID(actor))
	if e != nil {
		return nil, 0, e
	}
	matches := []any{}
	for _, c := range communities {
		if strings.Contains(strings.ToLower(c.Title+" "+c.Description), strings.ToLower(term)) {
			matches = append(matches, communityJSON(c))
		}
	}
	profileIDs, e := q.SearchProfiles(r.Context(), dbgen.SearchProfilesParams{ViewerID: actorID(actor), SearchText: strings.TrimPrefix(term, "@")})
	if e != nil {
		return nil, 0, e
	}
	profiles := []any{}
	for _, pid := range profileIDs {
		p, err := q.PublicProfile(r.Context(), dbgen.PublicProfileParams{ProfileID: pid, ViewerID: actorID(actor)})
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, 0, err
		}
		profiles = append(profiles, publicProfileViewOf(p))
	}
	return map[string]any{"items": items, "communities": matches, "profiles": profiles, "nextCursor": nil}, 200, nil
}

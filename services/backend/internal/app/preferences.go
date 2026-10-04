package app

import (
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/lavkushry/JanSetu-AI/services/backend/internal/store/dbgen"
)

func (a *App) notificationPreferences(w http.ResponseWriter, r *http.Request, actor *Actor) (any, int, error) {
	if e := require(actor); e != nil {
		return nil, 0, e
	}
	p, e := dbgen.New(a.store(r.Context())).NotificationPreference(r.Context(), actor.ProfileID)
	return map[string]any{"inApp": p.InApp, "version": p.Version}, 200, e
}
func (a *App) saveNotificationPreferences(w http.ResponseWriter, r *http.Request, actor *Actor) (any, int, error) {
	version, e := expected(r)
	if e != nil {
		return nil, 0, e
	}
	var b struct {
		InApp bool `json:"inApp"`
	}
	if e = decodeRequired(r, &b, "inApp"); e != nil {
		return nil, 0, e
	}
	var result any
	e = a.transaction(r.Context(), actor, func(q *dbgen.Queries) error {
		if err := q.EnsureNotificationPreference(r.Context(), actor.ProfileID); err != nil {
			return err
		}
		p, err := q.LockNotificationPreference(r.Context(), actor.ProfileID)
		if err != nil {
			return err
		}
		if p.Version != version {
			return conflict()
		}
		if p.InApp == b.InApp {
			result = map[string]any{"inApp": p.InApp, "version": p.Version}
			return nil
		}
		saved, err := q.SaveNotificationPreference(r.Context(), dbgen.SaveNotificationPreferenceParams{ViewerID: actor.ProfileID, InApp: b.InApp})
		result = map[string]any{"inApp": saved.InApp, "version": saved.Version}
		return err
	})
	return result, 200, e
}

func (a *App) setMute(w http.ResponseWriter, r *http.Request, actor *Actor) (any, int, error) {
	var b struct {
		TargetType string     `json:"targetType"`
		TargetID   uuid.UUID  `json:"targetId"`
		Active     bool       `json:"active"`
		ExpiresAt  *time.Time `json:"expiresAt"`
	}
	if e := decodeRequired(r, &b, "targetType", "targetId", "active"); e != nil {
		return nil, 0, e
	}
	if (b.TargetType != "PROFILE" && b.TargetType != "COMMUNITY") || b.TargetID == uuid.Nil {
		return nil, 0, invalid("Choose a person or community to mute")
	}
	var expires pgtype.Timestamptz
	e := a.transaction(r.Context(), actor, func(q *dbgen.Queries) error {
		if !b.Active {
			return q.DeleteMute(r.Context(), dbgen.DeleteMuteParams{ViewerID: actor.ProfileID, TargetType: b.TargetType, TargetID: b.TargetID})
		}
		if b.ExpiresAt != nil {
			now := time.Now()
			if !b.ExpiresAt.After(now) || b.ExpiresAt.After(now.Add(365*24*time.Hour)) {
				return invalid("Choose a future mute expiry within one year")
			}
			expires = pgtype.Timestamptz{Time: *b.ExpiresAt, Valid: true}
		}
		if b.TargetType == "PROFILE" {
			if b.TargetID == actor.ProfileID {
				return invalid("Choose another person")
			}
			if _, err := q.PublicProfile(r.Context(), dbgen.PublicProfileParams{ProfileID: b.TargetID, ViewerID: actor.ProfileID}); err != nil {
				return err
			}
			return q.SetProfileMute(r.Context(), dbgen.SetProfileMuteParams{ID: uuid.New(), ProfileID: actor.ProfileID, MutedProfileID: &b.TargetID, ExpiresAt: expires})
		}
		community, err := q.LockCommunity(r.Context(), b.TargetID)
		if err != nil {
			return err
		}
		if community.State != "ACTIVE" || community.Visibility == "PRIVATE" {
			return unavailable()
		}
		return q.SetCommunityMute(r.Context(), dbgen.SetCommunityMuteParams{ID: uuid.New(), ProfileID: actor.ProfileID, MutedCommunityID: &b.TargetID, ExpiresAt: expires})
	})
	return map[string]any{"targetType": b.TargetType, "targetId": b.TargetID, "active": b.Active, "expiresAt": timestamp(expires)}, 200, e
}

func (a *App) mutes(w http.ResponseWriter, r *http.Request, actor *Actor) (any, int, error) {
	if e := require(actor); e != nil {
		return nil, 0, e
	}
	c, e := a.profilePageCursor(r.URL.Query().Get("cursor"), "mutes", actor.ProfileID, actor.ProfileID)
	if e != nil {
		return nil, 0, e
	}
	rows, e := dbgen.New(a.store(r.Context())).MutePage(r.Context(), dbgen.MutePageParams{ViewerID: actor.ProfileID, HasCursor: !c.Before.IsZero(), BeforeTime: pgtype.Timestamptz{Time: c.Before, Valid: true}, BeforeID: c.ID})
	if e != nil {
		return nil, 0, e
	}
	items := []any{}
	for _, m := range rows[:min(20, len(rows))] {
		kind := "PROFILE"
		targetID := m.MutedProfileID
		var target any
		if m.MutedProfileID != nil && m.DisplayName != "" {
			target = map[string]any{"id": m.MutedProfileID, "label": m.DisplayName, "handle": m.Handle}
		}
		if m.MutedCommunityID != nil {
			kind = "COMMUNITY"
			targetID = m.MutedCommunityID
			if m.CommunityTitle != "" {
				target = map[string]any{"id": m.MutedCommunityID, "label": m.CommunityTitle, "handle": m.CommunitySlug}
			}
		}
		items = append(items, map[string]any{"id": m.ID, "targetType": kind, "targetId": targetID, "target": target, "createdAt": timestamp(m.CreatedAt), "expiresAt": timestamp(m.ExpiresAt), "active": m.Active.Bool})
	}
	var next any
	if len(rows) > 20 {
		last := rows[19]
		c.Before = last.CreatedAt.Time
		c.ID = last.ID
		next = a.encodeProfileCursor(c)
	}
	return map[string]any{"items": items, "nextCursor": next, "expiresAt": time.Unix(c.Expires, 0).UTC().Format(time.RFC3339)}, 200, nil
}

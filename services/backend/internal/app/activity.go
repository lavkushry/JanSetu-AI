package app

import (
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/lavkushry/JanSetu-AI/services/backend/internal/store/dbgen"
)

func (a *App) activity(w http.ResponseWriter, r *http.Request, actor *Actor) (any, int, error) {
	if e := require(actor); e != nil {
		return nil, 0, e
	}
	filter := r.URL.Query().Get("filter")
	if filter == "" {
		filter = "ALL"
	}
	if filter != "ALL" && filter != "SOCIAL" && filter != "CASES" {
		return nil, 0, invalid("Choose an activity filter")
	}
	c, e := a.profilePageCursor(r.URL.Query().Get("cursor"), "activity:"+filter, actor.ProfileID, actor.ProfileID)
	if e != nil {
		return nil, 0, e
	}
	rows, e := dbgen.New(a.store(r.Context())).ActivityPage(r.Context(), dbgen.ActivityPageParams{ViewerID: actor.ProfileID, Filter: filter, HasCursor: !c.Before.IsZero(), BeforeTime: pgtype.Timestamptz{Time: c.Before, Valid: true}, BeforeID: c.ID})
	if e != nil {
		return nil, 0, e
	}
	items := []any{}
	for _, n := range rows[:min(20, len(rows))] {
		var author any
		message := "Reviewed public progress was updated."
		target := map[string]any{"kind": "RECEIPT", "id": n.ReceiptID, "title": n.Title.String}
		if n.Kind == "REPLY" {
			author = map[string]any{"id": n.ActorID, "displayName": n.DisplayName.String, "handle": n.Handle.String}
			target = map[string]any{"kind": "POST", "id": n.PostID, "title": "Conversation"}
			message = "Replied to your conversation."
		}
		items = append(items, map[string]any{"id": n.ID, "kind": n.Kind, "createdAt": timestamp(n.CreatedAt), "readAt": timestamp(n.ReadAt), "actor": author, "message": message, "target": target})
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

func (a *App) activitySummary(w http.ResponseWriter, r *http.Request, actor *Actor) (any, int, error) {
	if e := require(actor); e != nil {
		return nil, 0, e
	}
	count, e := dbgen.New(a.store(r.Context())).ActivityUnread(r.Context(), actor.ProfileID)
	return map[string]any{"unreadCount": count}, 200, e
}

func (a *App) activityRead(w http.ResponseWriter, r *http.Request, actor *Actor) (any, int, error) {
	nid, e := id(r, "id")
	if e != nil {
		return nil, 0, e
	}
	var b struct {
		Read bool `json:"read"`
	}
	if e = decodeRequired(r, &b, "read"); e != nil {
		return nil, 0, e
	}
	var result dbgen.SetActivityReadRow
	e = a.transaction(r.Context(), actor, func(q *dbgen.Queries) error {
		var err error
		result, err = q.SetActivityRead(r.Context(), dbgen.SetActivityReadParams{ID: nid, ViewerID: actor.ProfileID, Read: b.Read})
		return err
	})
	return map[string]any{"id": result.ID, "readAt": timestamp(result.ReadAt)}, 200, e
}

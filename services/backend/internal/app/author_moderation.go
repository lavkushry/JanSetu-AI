package app

import (
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/lavkushry/JanSetu-AI/services/backend/internal/store/dbgen"
)

// This DTO is deliberately independent of internal moderation cases and notes.
func authorDecisionView(d dbgen.SocialAuthorModerationDecision) map[string]any {
	return map[string]any{"id": d.ID, "target": map[string]any{"type": d.TargetType, "id": d.TargetID, "postId": d.PostID, "revision": d.TargetRevision}, "action": d.Action, "ruleVersion": d.RuleVersion, "reason": d.Reason, "decidedAt": timestamp(d.DecidedAt)}
}
func (a *App) authorModerationDecision(w http.ResponseWriter, r *http.Request, actor *Actor) (any, int, error) {
	if err := require(actor); err != nil {
		return nil, 0, err
	}
	id, err := id(r, "id")
	if err != nil {
		return nil, 0, err
	}
	d, err := dbgen.New(a.store(r.Context())).AuthorModerationDecision(r.Context(), id)
	if err != nil {
		return nil, 0, err
	}
	return authorDecisionView(d), 200, nil
}
func (a *App) authorModerationDecisions(w http.ResponseWriter, r *http.Request, actor *Actor) (any, int, error) {
	if err := require(actor); err != nil {
		return nil, 0, err
	}
	c, err := a.profilePageCursor(r.URL.Query().Get("cursor"), "moderation-decisions", actor.ProfileID, actor.ProfileID)
	if err != nil {
		return nil, 0, err
	}
	rows, err := dbgen.New(a.store(r.Context())).AuthorModerationDecisionPage(r.Context(), dbgen.AuthorModerationDecisionPageParams{HasCursor: !c.Before.IsZero(), BeforeTime: pgtype.Timestamptz{Time: c.Before, Valid: true}, BeforeID: c.ID})
	if err != nil {
		return nil, 0, err
	}
	items := []any{}
	for _, d := range rows[:min(20, len(rows))] {
		items = append(items, authorDecisionView(d))
	}
	var next any
	if len(rows) > 20 {
		c.Before, c.ID = rows[19].DecidedAt.Time, rows[19].ID
		next = a.encodeProfileCursor(c)
	}
	return map[string]any{"items": items, "nextCursor": next, "expiresAt": time.Unix(c.Expires, 0).UTC().Format(time.RFC3339)}, 200, nil
}

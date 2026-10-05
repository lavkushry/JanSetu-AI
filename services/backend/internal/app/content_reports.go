package app

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/lavkushry/JanSetu-AI/services/backend/internal/store/dbgen"
)

func reportTarget(m dbgen.SocialModerationCase) (string, uuid.UUID) {
	if m.CommentID != nil {
		return "COMMENT", *m.CommentID
	}
	if m.PostID != nil {
		return "POST", *m.PostID
	}
	return "", uuid.Nil
}

// Receipts never cache source text in an idempotency record. Reads hydrate only
// the still-available reported revision and omit private reporter/actor IDs.
func contentReportReceipt(ctx context.Context, q *dbgen.Queries, m dbgen.SocialModerationCase) (map[string]any, error) {
	kind, target := reportTarget(m)
	var decision any
	d, err := q.ContentReportDecision(ctx, m.ID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	if err == nil {
		action := "DISMISS"
		if d.Action == "REMOVE" {
			action = "REMOVE"
		}
		decision = map[string]any{"action": action, "reason": d.Reason, "decidedAt": timestamp(d.DecidedAt)}
	}
	return map[string]any{"id": m.ID, "targetType": kind, "targetId": target, "targetRevision": m.TargetVersion, "reasonCode": m.ReasonCode, "details": m.Grounds, "state": m.State, "version": m.Version, "createdAt": timestamp(m.CreatedAt), "decision": decision}, nil
}
func contentReportView(ctx context.Context, q *dbgen.Queries, m dbgen.SocialModerationCase, viewer uuid.UUID, review bool) (map[string]any, error) {
	data, err := contentReportReceipt(ctx, q, m)
	if err != nil {
		return nil, err
	}
	data["target"], data["targetState"] = nil, "UNAVAILABLE"
	kind, target := reportTarget(m)
	t, err := q.ContentReportTarget(ctx, dbgen.ContentReportTargetParams{TargetType: kind, TargetID: target, ViewerID: viewer, ReviewAccess: review})
	if errors.Is(err, pgx.ErrNoRows) {
		return data, nil
	}
	if err != nil {
		return nil, err
	}
	if t.Revision != m.TargetVersion {
		data["targetState"] = "CHANGED"
		return data, nil
	}
	data["targetState"] = "AVAILABLE"
	data["target"] = map[string]any{"postId": t.PostID, "title": nullableText(t.Title), "body": t.Body, "authorName": t.DisplayName}
	return data, nil
}
func nullableText(v pgtype.Text) any {
	if !v.Valid {
		return nil
	}
	return v.String
}

func (a *App) createContentReport(w http.ResponseWriter, r *http.Request, actor *Actor) (any, int, error) {
	if err := require(actor); err != nil {
		return nil, 0, err
	}
	var b struct {
		TargetType     string    `json:"targetType"`
		TargetID       uuid.UUID `json:"targetId"`
		TargetRevision int64     `json:"targetRevision"`
		ReasonCode     string    `json:"reasonCode"`
		Details        string    `json:"details"`
	}
	if err := decodeRequired(r, &b, "targetType", "targetId", "targetRevision", "reasonCode"); err != nil {
		return nil, 0, err
	}
	b.Details = strings.TrimSpace(b.Details)
	if (b.TargetType != "POST" && b.TargetType != "COMMENT") || b.TargetID == uuid.Nil || b.TargetRevision < 1 || !textValid(b.Details, 0, 1000) {
		return nil, 0, invalid("Choose published content and provide up to 1,000 characters of detail")
	}
	switch b.ReasonCode {
	case "SPAM", "HARASSMENT", "HATE", "THREATS", "PRIVACY", "MISINFORMATION", "OTHER":
	default:
		return nil, 0, invalid("Choose a report reason")
	}
	if b.ReasonCode == "OTHER" && !textValid(b.Details, 5, 1000) {
		return nil, 0, invalid("Describe the concern in at least five characters")
	}
	result, err := a.createCommand(r, actor, "CreateContentReport", b, func(q *dbgen.Queries) (uuid.UUID, any, error) {
		target, err := q.ContentReportTarget(r.Context(), dbgen.ContentReportTargetParams{TargetType: b.TargetType, TargetID: b.TargetID, ViewerID: actor.ProfileID})
		if err != nil {
			return uuid.Nil, nil, err
		}
		if target.AuthorID != nil && *target.AuthorID == actor.ProfileID {
			return uuid.Nil, nil, invalid("You can edit or delete your own content")
		}
		if target.Revision != b.TargetRevision {
			return uuid.Nil, nil, failure(409, "REPORT_TARGET_CHANGED", "This content changed. Refresh it before reporting")
		}
		var post, comment *uuid.UUID
		if b.TargetType == "POST" {
			post = &b.TargetID
		} else {
			comment = &b.TargetID
		}
		prior, err := q.ExistingContentReport(r.Context(), dbgen.ExistingContentReportParams{ReporterRef: &actor.PrincipalID, PostID: post, CommentID: comment, TargetVersion: target.Revision})
		if err == nil {
			if prior.ReasonCode != b.ReasonCode || prior.Grounds != b.Details {
				return uuid.Nil, nil, failure(409, "REPORT_ALREADY_EXISTS", "You already reported this revision. View your content reports in Account")
			}
			receipt, err := contentReportReceipt(r.Context(), q, prior)
			return prior.ID, receipt, err
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, nil, err
		}
		count, err := q.ContentReportCount(r.Context(), &actor.PrincipalID)
		if err != nil {
			return uuid.Nil, nil, err
		}
		if count >= 10 {
			return uuid.Nil, nil, failure(429, "REPORT_LIMIT_REACHED", "You can submit ten new content reports per hour. Try again later")
		}
		m, err := q.InsertContentReport(r.Context(), dbgen.InsertContentReportParams{ID: uuid.New(), PostID: post, CommentID: comment, TargetVersion: target.Revision, ReporterRef: &actor.PrincipalID, ReasonCode: b.ReasonCode, Grounds: b.Details})
		if err != nil {
			return uuid.Nil, nil, err
		}
		receipt, err := contentReportReceipt(r.Context(), q, m)
		return m.ID, receipt, err
	})
	return result, 201, err
}

func (a *App) ownedContentReport(w http.ResponseWriter, r *http.Request, actor *Actor) (any, int, error) {
	if err := require(actor); err != nil {
		return nil, 0, err
	}
	id, err := id(r, "id")
	if err != nil {
		return nil, 0, err
	}
	q := dbgen.New(a.store(r.Context()))
	m, err := q.OwnedContentReport(r.Context(), dbgen.OwnedContentReportParams{ID: id, ReporterRef: &actor.PrincipalID})
	if err != nil {
		return nil, 0, err
	}
	data, err := contentReportView(r.Context(), q, m, actor.ProfileID, false)
	return data, 200, err
}
func (a *App) ownContentReports(w http.ResponseWriter, r *http.Request, actor *Actor) (any, int, error) {
	return a.contentReportPage(r, actor, false)
}
func (a *App) contentReportQueue(w http.ResponseWriter, r *http.Request, actor *Actor) (any, int, error) {
	return a.contentReportPage(r, actor, true)
}
func (a *App) contentReportPage(r *http.Request, actor *Actor, review bool) (any, int, error) {
	if err := require(actor); err != nil {
		return nil, 0, err
	}
	kind := "content-reports"
	if review {
		if !actor.Has("PLATFORM_MODERATOR") {
			return nil, 0, forbidden()
		}
		kind = "content-report-review"
	}
	c, err := a.profilePageCursor(r.URL.Query().Get("cursor"), kind, actor.ProfileID, actor.ProfileID)
	if err != nil {
		return nil, 0, err
	}
	q := dbgen.New(a.store(r.Context()))
	var rows []dbgen.SocialModerationCase
	if review {
		rows, err = q.ContentReportQueue(r.Context(), dbgen.ContentReportQueueParams{ViewerID: actor.ProfileID, ReviewerPrincipalID: actor.PrincipalID, HasCursor: !c.Before.IsZero(), AfterTime: pgtype.Timestamptz{Time: c.Before, Valid: true}, AfterID: c.ID})
	} else {
		rows, err = q.OwnContentReportPage(r.Context(), dbgen.OwnContentReportPageParams{ReporterRef: &actor.PrincipalID, HasCursor: !c.Before.IsZero(), BeforeTime: pgtype.Timestamptz{Time: c.Before, Valid: true}, BeforeID: c.ID})
	}
	if err != nil {
		return nil, 0, err
	}
	items := []any{}
	for _, row := range rows[:min(20, len(rows))] {
		data, err := contentReportView(r.Context(), q, row, actor.ProfileID, review)
		if err != nil {
			return nil, 0, err
		}
		items = append(items, data)
	}
	var next any
	if len(rows) > 20 {
		c.Before, c.ID = rows[19].CreatedAt.Time, rows[19].ID
		next = a.encodeProfileCursor(c)
	}
	return map[string]any{"items": items, "nextCursor": next, "expiresAt": time.Unix(c.Expires, 0).UTC().Format(time.RFC3339)}, 200, nil
}

func (a *App) contentReportDecision(w http.ResponseWriter, r *http.Request, actor *Actor) (any, int, error) {
	if !actor.Has("PLATFORM_MODERATOR") {
		return nil, 0, forbidden()
	}
	id, err := id(r, "id")
	if err != nil {
		return nil, 0, err
	}
	version, err := expected(r)
	if err != nil {
		return nil, 0, err
	}
	var b struct {
		Action         string `json:"action"`
		Reason         string `json:"reason"`
		TargetRevision int64  `json:"targetRevision"`
	}
	if err := decodeRequired(r, &b, "action", "reason", "targetRevision"); err != nil {
		return nil, 0, err
	}
	b.Reason = strings.TrimSpace(b.Reason)
	if (b.Action != "DISMISS" && b.Action != "REMOVE") || !textValid(b.Reason, 5, 1000) {
		return nil, 0, invalid("Choose a decision and provide a reason")
	}
	var receipt any
	err = a.transaction(r.Context(), actor, func(q *dbgen.Queries) error {
		if !actor.Has("PLATFORM_MODERATOR") {
			return forbidden()
		}
		m, err := q.LockModeration(r.Context(), id)
		if err != nil {
			return err
		}
		if m.ReporterRef == nil {
			return unavailable()
		}
		if *m.ReporterRef == actor.PrincipalID {
			return forbidden()
		}
		if m.Version != version || m.State != "OPEN" || b.TargetRevision != m.TargetVersion {
			return conflict()
		}
		kind, targetID := reportTarget(m)
		var postID uuid.UUID
		var aggregateVersion int64
		if kind == "POST" {
			p, err := q.LockPost(r.Context(), targetID)
			if err != nil {
				return err
			}
			if p.AuthorID != nil && *p.AuthorID == actor.ProfileID {
				return forbidden()
			}
			postID, aggregateVersion = p.ID, p.Version+1
		} else if kind == "COMMENT" {
			c, err := q.LockComment(r.Context(), targetID)
			if err != nil {
				return err
			}
			if c.AuthorID == actor.ProfileID {
				return forbidden()
			}
			p, err := q.LockPost(r.Context(), c.PostID)
			if err != nil {
				return err
			}
			postID, aggregateVersion = p.ID, p.Version
		} else {
			return unavailable()
		}
		action := "ALLOW"
		if b.Action == "REMOVE" {
			target, err := q.ContentReportTarget(r.Context(), dbgen.ContentReportTargetParams{TargetType: kind, TargetID: targetID, ViewerID: actor.ProfileID, ReviewAccess: true})
			if errors.Is(err, pgx.ErrNoRows) || (err == nil && target.Revision != m.TargetVersion) {
				return failure(409, "OBSOLETE_REPORT", "Reported content changed or is unavailable. Dismiss this report and review current content separately")
			}
			if err != nil {
				return err
			}
			if kind == "POST" {
				err = q.RemoveReportedPost(r.Context(), targetID)
			} else {
				err = q.RemoveReportedComment(r.Context(), targetID)
			}
			if err != nil {
				return err
			}
			if err = addEvent(r.Context(), q, "POST", postID, aggregateVersion, "ContentRevoked", map[string]any{}); err != nil {
				return err
			}
			action = "REMOVE"
		}
		if err := q.InsertContentReportDecision(r.Context(), dbgen.InsertContentReportDecisionParams{ID: uuid.New(), ModerationCaseID: m.ID, Action: action, ActorRef: actor.PrincipalID, Reason: b.Reason}); err != nil {
			return err
		}
		if err := q.FinishModeration(r.Context(), m.ID); err != nil {
			return err
		}
		m.State, m.Version = "DECIDED", m.Version+1
		receipt, err = contentReportReceipt(r.Context(), q, m)
		return err
	})
	return receipt, 200, err
}

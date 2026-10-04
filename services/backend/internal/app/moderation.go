package app

import (
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/lavkushry/JanSetu-AI/services/backend/internal/store/dbgen"
	"net/http"
)

func (a *App) moderationQueue(w http.ResponseWriter, r *http.Request, actor *Actor) (any, int, error) {
	if !actor.Has("PLATFORM_MODERATOR") {
		return nil, 0, forbidden()
	}
	q := dbgen.New(a.store(r.Context()))
	rows, e := q.ModerationQueue(r.Context())
	if e != nil {
		return nil, 0, e
	}
	items := []any{}
	for _, m := range rows {
		entry := map[string]any{"id": m.ID, "version": m.Version, "targetRevision": m.TargetVersion, "postId": m.PostID, "commentId": m.CommentID, "createdAt": timestamp(m.CreatedAt)}
		if m.PostID != nil {
			p, e := postData(r.Context(), q, *m.PostID, actor, true)
			if errors.Is(e, pgx.ErrNoRows) {
				continue
			}
			if e != nil {
				return nil, 0, e
			}
			entry["post"] = p
		} else if m.CommentID != nil {
			body, e := q.CandidateComment(r.Context(), dbgen.CandidateCommentParams{CommentID: *m.CommentID, Version: m.TargetVersion})
			if e != nil {
				return nil, 0, e
			}
			entry["body"] = body
		}
		items = append(items, entry)
	}
	return map[string]any{"items": items}, 200, nil
}
func (a *App) moderationDecision(w http.ResponseWriter, r *http.Request, actor *Actor) (any, int, error) {
	mid, e := id(r, "id")
	if e != nil {
		return nil, 0, e
	}
	version, e := expected(r)
	if e != nil {
		return nil, 0, e
	}
	var b struct {
		Action         string `json:"action"`
		Reason         string `json:"reason"`
		TargetRevision int64  `json:"targetRevision"`
	}
	if e = decode(r, &b); e != nil {
		return nil, 0, e
	}
	if (b.Action != "ALLOW" && b.Action != "RESTRICT") || !textValid(b.Reason, 5, 1000) {
		return nil, 0, invalid("Choose a decision and provide a reason")
	}
	e = a.transaction(r.Context(), actor, func(q *dbgen.Queries) error {
		if !actor.Has("PLATFORM_MODERATOR") {
			return forbidden()
		}
		m, e := q.LockModeration(r.Context(), mid)
		if e != nil {
			return e
		}
		if m.Version != version || m.State == "DECIDED" || m.TargetVersion != b.TargetRevision {
			return conflict()
		}
		var aggregate uuid.UUID
		var aggregateVersion int64
		if m.PostID != nil {
			p, e := q.LockPost(r.Context(), *m.PostID)
			if e != nil {
				return e
			}
			if int64(p.CurrentRevision) != m.TargetVersion || p.State == "DELETED" || p.State == "HIDDEN" {
				return failure(409, "OBSOLETE_REVIEW", "Review the latest available revision")
			}
			aggregate = p.ID
			aggregateVersion = p.Version + 1
			if b.Action == "ALLOW" {
				if e = q.ApprovePostRevision(r.Context(), dbgen.ApprovePostRevisionParams{PostID: p.ID, Revision: p.CurrentRevision}); e != nil {
					return e
				}
				if e = q.PublishPost(r.Context(), p.ID); e != nil {
					return e
				}
			} else {
				if e = q.RejectPostRevision(r.Context(), dbgen.RejectPostRevisionParams{PostID: p.ID, Revision: p.CurrentRevision}); e != nil {
					return e
				}
				if e = q.HideUnpublishedPost(r.Context(), p.ID); e != nil {
					return e
				}
			}
		} else if m.CommentID != nil {
			c, e := q.LockComment(r.Context(), *m.CommentID)
			if e != nil {
				return e
			}
			if c.CurrentRevision != m.TargetVersion || c.State == "DELETED" || c.State == "HIDDEN" {
				return failure(409, "OBSOLETE_REVIEW", "Review the latest available revision")
			}
			parent, e := q.LockPost(r.Context(), c.PostID)
			if e != nil {
				return e
			}
			if parent.State != "PUBLISHED" {
				return forbidden()
			}
			aggregate = c.PostID
			aggregateVersion = parent.Version
			if b.Action == "ALLOW" {
				body, e := q.CandidateComment(r.Context(), dbgen.CandidateCommentParams{CommentID: c.ID, Version: c.CurrentRevision})
				if e != nil {
					return e
				}
				if e = q.ApproveComment(r.Context(), dbgen.ApproveCommentParams{CommentID: c.ID, Version: c.CurrentRevision}); e != nil {
					return e
				}
				if e = q.PublishComment(r.Context(), dbgen.PublishCommentParams{ID: c.ID, Body: body}); e != nil {
					return e
				}
				if !c.PublishedVersion.Valid {
					if e = addEvent(r.Context(), q, "POST", c.PostID, parent.Version, "CommentPublished", map[string]any{"commentId": c.ID, "revision": c.CurrentRevision}); e != nil {
						return e
					}
				}
			} else {
				if e = q.RejectCommentRevision(r.Context(), dbgen.RejectCommentRevisionParams{CommentID: c.ID, Version: c.CurrentRevision}); e != nil {
					return e
				}
				if e = q.HideUnpublishedComment(r.Context(), c.ID); e != nil {
					return e
				}
			}
		} else {
			return invalid("Unsupported review target")
		}
		if e = q.InsertModerationDecision(r.Context(), dbgen.InsertModerationDecisionParams{ID: uuid.New(), ModerationCaseID: mid, Action: b.Action, ActorRef: actor.PrincipalID, Reason: b.Reason}); e != nil {
			return e
		}
		if e = q.FinishModeration(r.Context(), mid); e != nil {
			return e
		}
		return addEvent(r.Context(), q, "POST", aggregate, aggregateVersion, "PublicationReviewed", map[string]any{"decision": b.Action})
	})
	return map[string]any{"decision": b.Action}, 200, e
}

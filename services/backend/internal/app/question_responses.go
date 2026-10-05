package app

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/lavkushry/JanSetu-AI/services/backend/internal/store/dbgen"
)

func (a *App) selectResponse(w http.ResponseWriter, r *http.Request, actor *Actor) (any, int, error) {
	if err := require(actor); err != nil {
		return nil, 0, err
	}
	pid, err := id(r, "id")
	if err != nil {
		return nil, 0, err
	}
	version, err := expected(r)
	if err != nil {
		return nil, 0, err
	}
	var input struct {
		CommentID       json.RawMessage `json:"commentId"`
		CommentRevision *int64          `json:"commentRevision"`
	}
	if err = decode(r, &input); err != nil {
		return nil, 0, err
	}
	if input.CommentID == nil {
		return nil, 0, invalid("Provide commentId, or null to clear the choice")
	}
	var commentID *uuid.UUID
	if err = json.Unmarshal(input.CommentID, &commentID); err != nil {
		return nil, 0, invalid("Choose a valid comment ID")
	}
	if commentID != nil && (input.CommentRevision == nil || *input.CommentRevision < 1) {
		return nil, 0, invalid("Provide the published reply revision you are choosing")
	}
	if commentID == nil && input.CommentRevision != nil {
		return nil, 0, invalid("Clear the choice without a reply revision")
	}
	var result any
	err = a.transaction(r.Context(), actor, func(q *dbgen.Queries) error {
		// The post DTO and candidate queries use current visibility, never review access.
		if _, err := postData(r.Context(), q, pid, actor, false); err != nil {
			return err
		}
		p, err := q.LockPost(r.Context(), pid)
		if err != nil {
			return err
		}
		if p.Kind != "QUESTION" || p.State != "PUBLISHED" {
			return invalid("Choose a published question")
		}
		allowed := p.AuthorID != nil && *p.AuthorID == actor.ProfileID
		if !allowed && p.CommunityID != nil {
			m, e := q.Membership(r.Context(), dbgen.MembershipParams{CommunityID: *p.CommunityID, ProfileID: actor.ProfileID})
			if e != nil && !errors.Is(e, pgx.ErrNoRows) {
				return e
			}
			allowed = m.State == "ACTIVE" && (m.Role == "MODERATOR" || m.Role == "OWNER")
		}
		if !allowed {
			return forbidden()
		}
		if p.Version != version {
			return conflict()
		}
		previous, err := q.GetSelectedResponse(r.Context(), pid)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		present := err == nil
		changed := present
		if commentID == nil {
			if err = q.ClearSelectedResponse(r.Context(), pid); err != nil {
				return err
			}
		} else {
			candidate, e := q.QuestionResponseCandidate(r.Context(), dbgen.QuestionResponseCandidateParams{PostID: pid, CommentID: *commentID, ViewerID: actor.ProfileID})
			if errors.Is(e, pgx.ErrNoRows) {
				return invalid("Choose an available published reply in this question")
			}
			if e != nil {
				return e
			}
			if candidate.CommentRevision.Int64 != *input.CommentRevision {
				return conflict()
			}
			changed = !present || previous.CommentID != candidate.CommentID || previous.PostRevision != candidate.PostRevision.Int32 || previous.CommentRevision != candidate.CommentRevision.Int64
			if changed {
				if err = q.SetSelectedResponse(r.Context(), dbgen.SetSelectedResponseParams{PostID: pid, CommentID: candidate.CommentID, SelectedBy: actor.ProfileID, PostRevision: candidate.PostRevision.Int32, CommentRevision: candidate.CommentRevision.Int64}); err != nil {
					return err
				}
			}
		}
		if changed {
			if err = q.TouchQuestionResponse(r.Context(), pid); err != nil {
				return err
			}
			p.Version++
			// Audit/projection metadata contains no reply body or private edit text.
			if err = addEvent(r.Context(), q, "POST", pid, p.Version, "HelpfulResponseChanged", map[string]any{"commentId": commentID}); err != nil {
				return err
			}
		}
		version = p.Version
		result = map[string]any{"commentId": commentID, "version": version}
		return nil
	})
	if err == nil {
		w.Header().Set("ETag", `"`+strconv.FormatInt(version, 10)+`"`)
	}
	return result, 200, err
}

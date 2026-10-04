package app

import (
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/lavkushry/JanSetu-AI/services/backend/internal/store/dbgen"
)

func (a *App) vote(w http.ResponseWriter, r *http.Request, actor *Actor) (any, int, error) {
	pid, e := id(r, "id")
	if e != nil {
		return nil, 0, e
	}
	var b struct {
		Value int16 `json:"value"`
	}
	if e = decodeRequired(r, &b, "value"); e != nil {
		return nil, 0, e
	}
	if b.Value < -1 || b.Value > 1 {
		return nil, 0, invalid("Vote must be -1, 0, or 1")
	}
	e = a.transaction(r.Context(), actor, func(q *dbgen.Queries) error {
		if b.Value != 0 {
			if _, e := postData(r.Context(), q, pid, actor, false); e != nil {
				return e
			}
		}
		p, e := q.LockPost(r.Context(), pid)
		if b.Value == 0 && errors.Is(e, pgx.ErrNoRows) {
			return nil
		}
		if e != nil {
			return e
		}
		if b.Value != 0 && (p.State != "PUBLISHED" || p.AuthorID != nil && *p.AuthorID == actor.ProfileID) {
			return forbidden()
		}
		if b.Value == 0 {
			e = q.DeleteVote(r.Context(), dbgen.DeleteVoteParams{ProfileID: actor.ProfileID, PostID: pid})
		} else {
			e = q.SetVote(r.Context(), dbgen.SetVoteParams{ProfileID: actor.ProfileID, PostID: pid, Value: b.Value})
		}
		if e != nil {
			return e
		}
		return addEvent(r.Context(), q, "POST", pid, p.Version, "PostVoteChanged", map[string]any{})
	})
	return map[string]any{"value": b.Value}, 200, e
}
func (a *App) bookmark(w http.ResponseWriter, r *http.Request, actor *Actor) (any, int, error) {
	return a.postFlag(r, actor, "bookmark")
}
func (a *App) repost(w http.ResponseWriter, r *http.Request, actor *Actor) (any, int, error) {
	return a.postFlag(r, actor, "repost")
}
func (a *App) postFlag(r *http.Request, actor *Actor, kind string) (any, int, error) {
	pid, e := id(r, "id")
	if e != nil {
		return nil, 0, e
	}
	var b struct {
		Enabled bool `json:"enabled"`
	}
	if e = decodeRequired(r, &b, "enabled"); e != nil {
		return nil, 0, e
	}
	e = a.transaction(r.Context(), actor, func(q *dbgen.Queries) error {
		if b.Enabled {
			if _, e := postData(r.Context(), q, pid, actor, false); e != nil {
				return e
			}
		}
		p, e := q.LockPost(r.Context(), pid)
		if !b.Enabled && errors.Is(e, pgx.ErrNoRows) {
			return nil
		}
		if e != nil {
			return e
		}
		if b.Enabled && p.State != "PUBLISHED" {
			return forbidden()
		}
		if kind == "bookmark" {
			if b.Enabled {
				return q.SetBookmark(r.Context(), dbgen.SetBookmarkParams{ProfileID: actor.ProfileID, PostID: pid})
			}
			return q.DeleteBookmark(r.Context(), dbgen.DeleteBookmarkParams{ProfileID: actor.ProfileID, PostID: pid})
		}
		if b.Enabled {
			e = q.SetRepost(r.Context(), dbgen.SetRepostParams{ProfileID: actor.ProfileID, PostID: pid})
		} else {
			e = q.DeleteRepost(r.Context(), dbgen.DeleteRepostParams{ProfileID: actor.ProfileID, PostID: pid})
		}
		if e != nil {
			return e
		}
		return addEvent(r.Context(), q, "POST", pid, p.Version, "PostRepostChanged", map[string]any{})
	})
	return map[string]any{"enabled": b.Enabled}, 200, e
}
func (a *App) block(w http.ResponseWriter, r *http.Request, actor *Actor) (any, int, error) {
	return a.profileFlag(r, actor, true)
}
func (a *App) follow(w http.ResponseWriter, r *http.Request, actor *Actor) (any, int, error) {
	return a.profileFlag(r, actor, false)
}
func (a *App) profileFlag(r *http.Request, actor *Actor, block bool) (any, int, error) {
	pid, e := id(r, "id")
	if e != nil {
		return nil, 0, e
	}
	var b struct {
		Enabled bool `json:"enabled"`
	}
	if e = decodeRequired(r, &b, "enabled"); e != nil {
		return nil, 0, e
	}
	e = a.transaction(r.Context(), actor, func(q *dbgen.Queries) error {
		if pid == actor.ProfileID {
			return invalid("Choose another person")
		}
		// Removing an owned relationship remains possible after the other account
		// becomes inactive. This desired-state command is also an idempotent no-op
		// for an absent relationship and reveals no target account state.
		if !b.Enabled {
			if block {
				return q.DeleteBlock(r.Context(), dbgen.DeleteBlockParams{BlockerID: actor.ProfileID, BlockedID: pid})
			}
			return q.DeleteProfileFollow(r.Context(), dbgen.DeleteProfileFollowParams{FollowerID: actor.ProfileID, FollowedID: pid})
		}
		profiles, e := q.LockProfiles(r.Context(), []uuid.UUID{pid})
		if e != nil {
			return e
		}
		if len(profiles) != 1 || profiles[0].State != "ACTIVE" {
			return unavailable()
		}
		if block {
			if e = q.SetBlock(r.Context(), dbgen.SetBlockParams{BlockerID: actor.ProfileID, BlockedID: pid}); e != nil {
				return e
			}
			return q.RemoveConflictingFollows(r.Context(), dbgen.RemoveConflictingFollowsParams{FollowerID: actor.ProfileID, FollowedID: pid})
		}
		blocked, e := q.HasBlock(r.Context(), dbgen.HasBlockParams{BlockerID: actor.ProfileID, BlockedID: pid})
		if e != nil {
			return e
		}
		if blocked {
			return forbidden()
		}
		return q.SetProfileFollow(r.Context(), dbgen.SetProfileFollowParams{FollowerID: actor.ProfileID, FollowedID: pid})
	})
	return map[string]any{"enabled": b.Enabled}, 200, e
}

package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/lavkushry/JanSetu-AI/services/backend/internal/store/dbgen"
)

type PostInput struct {
	Kind            string      `json:"kind"`
	CommunityID     *uuid.UUID  `json:"communityId"`
	Title           *string     `json:"title"`
	Body            string      `json:"body"`
	LanguageTag     string      `json:"languageTag"`
	MediaIDs        []uuid.UUID `json:"mediaIds"`
	SubmitForReview bool        `json:"submitForReview"`
}

func (b *PostInput) validate() error {
	b.Body = strings.TrimSpace(b.Body)
	if b.LanguageTag == "" {
		b.LanguageTag = "en-IN"
	}
	if len(b.LanguageTag) > 40 {
		return invalid("Invalid language")
	}
	if b.Kind != "SHORT" && b.Kind != "DISCUSSION" && b.Kind != "QUESTION" {
		return failure(422, "FEATURE_UNAVAILABLE", "This post type is not enabled")
	}
	max := 8000
	if b.Kind == "SHORT" {
		max = 1000
	}
	if !textValid(b.Body, 1, max) {
		return invalid("Check the length of your post")
	}
	if b.Kind != "SHORT" && (b.CommunityID == nil || b.Title == nil || !textValid(*b.Title, 1, 180)) {
		return invalid("Choose a community and add a title")
	}
	if b.Title != nil && !textValid(*b.Title, 0, 180) {
		return invalid("Title is too long")
	}
	if len(b.MediaIDs) > 0 {
		return failure(422, "CAPABILITY_UNAVAILABLE", "Media upload is not enabled in this milestone")
	}
	if !b.SubmitForReview {
		return invalid("Local drafts are saved on your device; submit this revision for review")
	}
	return nil
}
func postData(ctx context.Context, q *dbgen.Queries, id uuid.UUID, actor *Actor, review bool) (json.RawMessage, error) {
	b, e := q.GetPost(ctx, dbgen.GetPostParams{PostID: id, ViewerID: actorID(actor), ReviewAccess: review})
	return json.RawMessage(b), e
}
func moderation(ctx context.Context, q *dbgen.Queries, post, comment *uuid.UUID, revision int64) error {
	return q.InsertModeration(ctx, dbgen.InsertModerationParams{ID: uuid.New(), PostID: post, CommentID: comment, TargetVersion: revision})
}
func (a *App) getPost(w http.ResponseWriter, r *http.Request, actor *Actor) (any, int, error) {
	pid, e := id(r, "id")
	if e != nil {
		return nil, 0, e
	}
	data, e := postData(r.Context(), dbgen.New(a.DB), pid, actor, false)
	if e == nil {
		var v struct {
			Version int64 `json:"version"`
		}
		_ = json.Unmarshal(data, &v)
		w.Header().Set("ETag", `"`+json.Number(string(jsonBytes(v.Version))).String()+`"`)
	}
	return data, 200, e
}
func (a *App) createPost(w http.ResponseWriter, r *http.Request, actor *Actor) (any, int, error) {
	if e := require(actor); e != nil {
		return nil, 0, e
	}
	var b PostInput
	if e := decode(r, &b); e != nil {
		return nil, 0, e
	}
	if e := b.validate(); e != nil {
		return nil, 0, e
	}
	result, err := a.createCommand(r, actor, "CreatePost", b, func(q *dbgen.Queries) (uuid.UUID, any, error) {
		if e := a.canPost(r.Context(), q, actor, b.CommunityID); e != nil {
			return uuid.Nil, nil, e
		}
		pid := uuid.New()
		e := q.InsertPost(r.Context(), dbgen.InsertPostParams{ID: pid, AuthorID: &actor.ProfileID, CommunityID: b.CommunityID, Kind: b.Kind, State: "PENDING"})
		if e != nil {
			return uuid.Nil, nil, e
		}
		e = q.InsertPostRevision(r.Context(), dbgen.InsertPostRevisionParams{PostID: pid, Revision: 1, Title: pgtype.Text{String: value(b.Title), Valid: b.Title != nil}, Body: b.Body, LanguageTag: b.LanguageTag, EditorID: &actor.ProfileID})
		if e != nil {
			return uuid.Nil, nil, e
		}
		if e = moderation(r.Context(), q, &pid, nil, 1); e != nil {
			return uuid.Nil, nil, e
		}
		if e = addEvent(r.Context(), q, "POST", pid, 1, "PostReviewRequested", map[string]any{"revision": 1}); e != nil {
			return uuid.Nil, nil, e
		}
		data, e := postData(r.Context(), q, pid, actor, false)
		return pid, data, e
	})
	return result, 201, err
}
func value(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
func (a *App) editPost(w http.ResponseWriter, r *http.Request, actor *Actor) (any, int, error) {
	pid, e := id(r, "id")
	if e != nil {
		return nil, 0, e
	}
	version, e := expected(r)
	if e != nil {
		return nil, 0, e
	}
	var b struct {
		Title           *string     `json:"title"`
		Body            string      `json:"body"`
		LanguageTag     string      `json:"languageTag"`
		MediaIDs        []uuid.UUID `json:"mediaIds"`
		SubmitForReview bool        `json:"submitForReview"`
	}
	if e = decode(r, &b); e != nil {
		return nil, 0, e
	}
	var data json.RawMessage
	e = a.transaction(r.Context(), actor, func(q *dbgen.Queries) error {
		p, e := q.LockPost(r.Context(), pid)
		if e != nil {
			return unavailable()
		}
		if p.AuthorID == nil || *p.AuthorID != actor.ProfileID {
			return forbidden()
		}
		if p.Version != version {
			return conflict()
		}
		input := PostInput{Kind: p.Kind, CommunityID: p.CommunityID, Title: b.Title, Body: b.Body, LanguageTag: b.LanguageTag, MediaIDs: b.MediaIDs, SubmitForReview: b.SubmitForReview}
		if e = input.validate(); e != nil {
			return e
		}
		if e = a.canPost(r.Context(), q, actor, p.CommunityID); e != nil {
			return e
		}
		updated, e := q.EditPost(r.Context(), dbgen.EditPostParams{ID: pid, Version: version})
		if errors.Is(e, pgx.ErrNoRows) {
			return conflict()
		}
		if e != nil {
			return e
		}
		if e = q.InsertPostRevision(r.Context(), dbgen.InsertPostRevisionParams{PostID: pid, Revision: updated.CurrentRevision, Title: pgtype.Text{String: value(input.Title), Valid: input.Title != nil}, Body: input.Body, LanguageTag: input.LanguageTag, EditorID: &actor.ProfileID}); e != nil {
			return e
		}
		if e = moderation(r.Context(), q, &pid, nil, int64(updated.CurrentRevision)); e != nil {
			return e
		}
		if e = addEvent(r.Context(), q, "POST", pid, updated.Version, "PostReviewRequested", map[string]any{"revision": updated.CurrentRevision}); e != nil {
			return e
		}
		data, e = postData(r.Context(), q, pid, actor, false)
		return e
	})
	return data, 200, e
}
func (a *App) deletePost(w http.ResponseWriter, r *http.Request, actor *Actor) (any, int, error) {
	pid, e := id(r, "id")
	if e != nil {
		return nil, 0, e
	}
	version, e := expected(r)
	if e != nil {
		return nil, 0, e
	}
	e = a.transaction(r.Context(), actor, func(q *dbgen.Queries) error {
		p, e := q.LockPost(r.Context(), pid)
		if e != nil {
			return unavailable()
		}
		if p.AuthorID == nil || *p.AuthorID != actor.ProfileID {
			return forbidden()
		}
		if p.Version != version {
			return conflict()
		}
		if e = q.DeletePost(r.Context(), pid); e != nil {
			return e
		}
		return addEvent(r.Context(), q, "POST", pid, p.Version+1, "ContentRevoked", map[string]any{})
	})
	return nil, 204, e
}
func (a *App) comments(w http.ResponseWriter, r *http.Request, actor *Actor) (any, int, error) {
	pid, e := id(r, "id")
	if e != nil {
		return nil, 0, e
	}
	q := dbgen.New(a.DB)
	if _, e = postData(r.Context(), q, pid, actor, false); e != nil {
		return nil, 0, e
	}
	rows, e := q.CommentPage(r.Context(), dbgen.CommentPageParams{PostID: pid, ViewerID: actorID(actor)})
	items := []json.RawMessage{}
	for _, b := range rows {
		items = append(items, json.RawMessage(b))
	}
	return map[string]any{"items": items, "nextCursor": nil}, 200, e
}

type CommentInput struct {
	Body        string     `json:"body"`
	LanguageTag string     `json:"languageTag"`
	ParentID    *uuid.UUID `json:"parentId"`
}

func (b *CommentInput) validate() error {
	b.Body = strings.TrimSpace(b.Body)
	if b.LanguageTag == "" {
		b.LanguageTag = "en-IN"
	}
	if !textValid(b.Body, 1, 4000) || len(b.LanguageTag) > 40 {
		return invalid("Write a comment of up to 4,000 characters")
	}
	return nil
}
func (a *App) createComment(w http.ResponseWriter, r *http.Request, actor *Actor) (any, int, error) {
	if e := require(actor); e != nil {
		return nil, 0, e
	}
	pid, e := id(r, "id")
	if e != nil {
		return nil, 0, e
	}
	var b CommentInput
	if e = decode(r, &b); e != nil {
		return nil, 0, e
	}
	if e = b.validate(); e != nil {
		return nil, 0, e
	}
	data, e := a.createCommand(r, actor, "CreateComment:"+pid.String(), b, func(q *dbgen.Queries) (uuid.UUID, any, error) {
		if _, e := postData(r.Context(), q, pid, actor, false); e != nil {
			return uuid.Nil, nil, e
		}
		p, e := q.LockPost(r.Context(), pid)
		if e != nil {
			return uuid.Nil, nil, e
		}
		if p.State != "PUBLISHED" {
			return uuid.Nil, nil, forbidden()
		}
		if e = a.canPost(r.Context(), q, actor, p.CommunityID); e != nil {
			return uuid.Nil, nil, e
		}
		var depth int16
		if b.ParentID != nil {
			parent, e := q.LockComment(r.Context(), *b.ParentID)
			if e != nil || parent.PostID != pid || parent.State != "PUBLISHED" {
				return uuid.Nil, nil, invalid("Choose an available comment in this thread")
			}
			depth = parent.Depth + 1
			if depth > 20 {
				return uuid.Nil, nil, invalid("Maximum reply depth reached")
			}
		}
		cid := uuid.New()
		if e = q.InsertComment(r.Context(), dbgen.InsertCommentParams{ID: cid, PostID: pid, ParentID: b.ParentID, AuthorID: actor.ProfileID, Body: b.Body, Depth: depth}); e != nil {
			return uuid.Nil, nil, e
		}
		if e = q.InsertCommentRevision(r.Context(), dbgen.InsertCommentRevisionParams{CommentID: cid, Version: 1, Body: b.Body, LanguageTag: b.LanguageTag}); e != nil {
			return uuid.Nil, nil, e
		}
		if e = moderation(r.Context(), q, nil, &cid, 1); e != nil {
			return uuid.Nil, nil, e
		}
		if e = addEvent(r.Context(), q, "POST", pid, p.Version, "CommentReviewRequested", map[string]any{"commentId": cid}); e != nil {
			return uuid.Nil, nil, e
		}
		return cid, map[string]any{"id": cid, "postId": pid, "state": "PENDING", "version": 1, "currentRevision": 1}, nil
	})
	return data, 201, e
}
func (a *App) editComment(w http.ResponseWriter, r *http.Request, actor *Actor) (any, int, error) {
	cid, e := id(r, "id")
	if e != nil {
		return nil, 0, e
	}
	version, e := expected(r)
	if e != nil {
		return nil, 0, e
	}
	var b struct {
		Body        string `json:"body"`
		LanguageTag string `json:"languageTag"`
	}
	if e = decode(r, &b); e != nil {
		return nil, 0, e
	}
	input := CommentInput{Body: b.Body, LanguageTag: b.LanguageTag}
	if e = input.validate(); e != nil {
		return nil, 0, e
	}
	var result any
	e = a.transaction(r.Context(), actor, func(q *dbgen.Queries) error {
		c, e := q.LockComment(r.Context(), cid)
		if e != nil {
			return unavailable()
		}
		if c.AuthorID != actor.ProfileID {
			return forbidden()
		}
		if c.Version != version {
			return conflict()
		}
		if _, e = postData(r.Context(), q, c.PostID, actor, false); e != nil {
			return e
		}
		v, e := q.EditComment(r.Context(), dbgen.EditCommentParams{ID: cid, Version: version})
		if e != nil {
			return conflict()
		}
		if e = q.InsertCommentRevision(r.Context(), dbgen.InsertCommentRevisionParams{CommentID: cid, Version: v.CurrentRevision, Body: input.Body, LanguageTag: input.LanguageTag}); e != nil {
			return e
		}
		if e = moderation(r.Context(), q, nil, &cid, v.CurrentRevision); e != nil {
			return e
		}
		result = map[string]any{"id": cid, "version": v.Version, "currentRevision": v.CurrentRevision, "reviewState": "PENDING"}
		return addEvent(r.Context(), q, "POST", c.PostID, v.Version, "CommentReviewRequested", map[string]any{"commentId": cid})
	})
	return result, 200, e
}
func (a *App) deleteComment(w http.ResponseWriter, r *http.Request, actor *Actor) (any, int, error) {
	cid, e := id(r, "id")
	if e != nil {
		return nil, 0, e
	}
	version, e := expected(r)
	if e != nil {
		return nil, 0, e
	}
	e = a.transaction(r.Context(), actor, func(q *dbgen.Queries) error {
		c, e := q.LockComment(r.Context(), cid)
		if e != nil {
			return unavailable()
		}
		if c.AuthorID != actor.ProfileID {
			return forbidden()
		}
		if c.Version != version {
			return conflict()
		}
		if e = q.DeleteComment(r.Context(), cid); e != nil {
			return e
		}
		return addEvent(r.Context(), q, "POST", c.PostID, c.Version+1, "CommentRevoked", map[string]any{})
	})
	return nil, 204, e
}

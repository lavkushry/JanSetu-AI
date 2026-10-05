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

// Appeal receipts never include identities, source text or internal review notes.
func appealReceipt(ctx context.Context, q *dbgen.Queries, v dbgen.SocialAppeal) (map[string]any, error) {
	var outcome any
	d, err := q.AppealDecision(ctx, v.ID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	if err == nil {
		outcome = map[string]any{"result": d.Result, "reason": d.AuthorReason, "restorationState": d.RestorationState, "restorationReason": d.RestorationReason, "decidedAt": timestamp(d.DecidedAt)}
	}
	return map[string]any{"id": v.ID, "decisionId": v.DecisionID, "grounds": v.Grounds, "state": v.State, "version": v.Version, "createdAt": timestamp(v.CreatedAt), "outcome": outcome}, nil
}
func (a *App) createAppeal(w http.ResponseWriter, r *http.Request, actor *Actor) (any, int, error) {
	if err := require(actor); err != nil {
		return nil, 0, err
	}
	did, err := id(r, "id")
	if err != nil {
		return nil, 0, err
	}
	var b struct {
		Grounds string `json:"grounds"`
	}
	if err = decode(r, &b); err != nil {
		return nil, 0, err
	}
	b.Grounds = strings.TrimSpace(b.Grounds)
	if !textValid(b.Grounds, 5, 1000) {
		return nil, 0, invalid("Explain your appeal in 5–1,000 characters")
	}
	result, err := a.createCommand(r, actor, "appeal:"+did.String(), b, func(q *dbgen.Queries) (uuid.UUID, any, error) {
		original, e := q.AuthorModerationDecision(r.Context(), did)
		if e != nil {
			return uuid.Nil, nil, e
		}
		if original.Action != "RESTRICT" && original.Action != "REMOVE" {
			return uuid.Nil, nil, invalid("Only restrictions and removals can be appealed")
		}
		v, e := q.OwnedAppealForDecision(r.Context(), dbgen.OwnedAppealForDecisionParams{DecisionID: did, AppellantRef: actor.PrincipalID})
		if e == nil {
			if v.Grounds != b.Grounds {
				return uuid.Nil, nil, failure(409, "APPEAL_ALREADY_EXISTS", "This decision already has an appeal")
			}
			receipt, e := appealReceipt(r.Context(), q, v)
			return v.ID, receipt, e
		}
		if !errors.Is(e, pgx.ErrNoRows) {
			return uuid.Nil, nil, e
		}
		n, e := q.AppealCount(r.Context(), actor.PrincipalID)
		if e != nil {
			return uuid.Nil, nil, e
		}
		if n >= 10 {
			return uuid.Nil, nil, failure(429, "APPEAL_QUOTA", "Ten new appeals per hour are available in this pilot")
		}
		v, e = q.InsertAppeal(r.Context(), dbgen.InsertAppealParams{ID: uuid.New(), DecisionID: did, AppellantRef: actor.PrincipalID, Grounds: b.Grounds})
		if e != nil {
			return uuid.Nil, nil, e
		}
		receipt, e := appealReceipt(r.Context(), q, v)
		return v.ID, receipt, e
	})
	return result, 201, err
}
func (a *App) ownAppeal(w http.ResponseWriter, r *http.Request, actor *Actor) (any, int, error) {
	if err := require(actor); err != nil {
		return nil, 0, err
	}
	aid, err := id(r, "id")
	if err != nil {
		return nil, 0, err
	}
	q := dbgen.New(a.store(r.Context()))
	v, err := q.OwnedAppeal(r.Context(), dbgen.OwnedAppealParams{ID: aid, AppellantRef: actor.PrincipalID})
	if err != nil {
		return nil, 0, err
	}
	result, err := appealReceipt(r.Context(), q, v)
	return result, 200, err
}
func (a *App) appealPage(w http.ResponseWriter, r *http.Request, actor *Actor, staff bool) (any, int, error) {
	if err := require(actor); err != nil {
		return nil, 0, err
	}
	kind := "appeals"
	if staff {
		if !actor.Has("PLATFORM_MODERATOR") {
			return nil, 0, forbidden()
		}
		kind = "appeal-review"
	}
	c, err := a.profilePageCursor(r.URL.Query().Get("cursor"), kind, actor.ProfileID, actor.ProfileID)
	if err != nil {
		return nil, 0, err
	}
	q := dbgen.New(a.store(r.Context()))
	var rows []dbgen.SocialAppeal
	if staff {
		rows, err = q.AppealQueue(r.Context(), dbgen.AppealQueueParams{HasCursor: !c.Before.IsZero(), AfterTime: pgtype.Timestamptz{Time: c.Before, Valid: true}, AfterID: c.ID})
	} else {
		rows, err = q.OwnAppealPage(r.Context(), dbgen.OwnAppealPageParams{AppellantRef: actor.PrincipalID, HasCursor: !c.Before.IsZero(), BeforeTime: pgtype.Timestamptz{Time: c.Before, Valid: true}, BeforeID: c.ID})
	}
	if err != nil {
		return nil, 0, err
	}
	items := []any{}
	for _, v := range rows[:min(20, len(rows))] {
		var result map[string]any
		if staff {
			result, err = a.appealReviewView(r.Context(), q, v, actor)
		} else {
			result, err = appealReceipt(r.Context(), q, v)
		}
		if err != nil {
			return nil, 0, err
		}
		items = append(items, result)
	}
	var next any
	if len(rows) > 20 {
		c.Before, c.ID = rows[19].CreatedAt.Time, rows[19].ID
		next = a.encodeProfileCursor(c)
	}
	return map[string]any{"items": items, "nextCursor": next, "expiresAt": time.Unix(c.Expires, 0).UTC().Format(time.RFC3339)}, 200, nil
}
func (a *App) ownAppeals(w http.ResponseWriter, r *http.Request, actor *Actor) (any, int, error) {
	return a.appealPage(w, r, actor, false)
}
func (a *App) appealQueue(w http.ResponseWriter, r *http.Request, actor *Actor) (any, int, error) {
	return a.appealPage(w, r, actor, true)
}

// A restoration plan rechecks the exact revision and current publication policy.
// A reversed historical decision can be recorded even when restoration is blocked.
type appealPlan struct {
	Source  dbgen.AppealSourceStatusRow
	Preview *dbgen.AppealReviewPreviewRow
	Reason  string
}

func (a *App) planAppeal(ctx context.Context, q *dbgen.Queries, d dbgen.AppealOriginalRow) (appealPlan, error) {
	p := appealPlan{Reason: "TARGET_UNAVAILABLE"}
	postID, commentID := uuid.Nil, uuid.Nil
	if d.PostID != nil {
		postID = *d.PostID
	}
	if d.CommentID != nil {
		commentID = *d.CommentID
	}
	s, err := q.AppealSourceStatus(ctx, dbgen.AppealSourceStatusParams{TargetRevision: d.TargetVersion, PostID: postID, CommentID: commentID})
	if errors.Is(err, pgx.ErrNoRows) {
		return p, nil
	}
	if err != nil {
		return p, err
	}
	p.Source = s
	if s.State == "DELETED" || s.AuthorID == nil {
		return p, nil
	}
	preview, err := q.AppealReviewPreview(ctx, dbgen.AppealReviewPreviewParams{DecisionID: d.ID, TargetRevision: d.TargetVersion, PostID: postID, CommentID: commentID})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return p, err
	}
	if err == nil {
		p.Preview = &preview
	}
	if s.CurrentRevision != d.TargetVersion {
		p.Reason = "TARGET_CHANGED"
		return p, nil
	}
	if d.Action == "RESTRICT" {
		if s.ReviewState != "REJECTED" || (s.State != "PUBLISHED" && !(s.State == "HIDDEN" && s.PublishedRevision == 0)) {
			return p, nil
		}
	} else if d.Action == "REMOVE" {
		if s.State != "HIDDEN" || s.PublishedRevision != d.TargetVersion || s.ReviewState != "APPROVED" {
			return p, nil
		}
	} else {
		return p, invalid("Unsupported appeal decision")
	}
	if p.Preview == nil {
		return p, nil
	}
	// Read policy explicitly so unexpected database failures are not recorded as policy outcomes.
	if s.CommunityID != nil {
		community, e := q.LockCommunity(ctx, *s.CommunityID)
		if e != nil {
			return p, e
		}
		if community.State != "ACTIVE" || community.Visibility == "PRIVATE" {
			return p, nil
		}
		membership, e := q.Membership(ctx, dbgen.MembershipParams{CommunityID: *s.CommunityID, ProfileID: *s.AuthorID})
		if e != nil && !errors.Is(e, pgx.ErrNoRows) {
			return p, e
		}
		if e != nil || membership.State != "ACTIVE" || (community.Visibility == "RESTRICTED" && membership.Role == "MEMBER") {
			p.Reason = "POSTING_NOT_ALLOWED"
			return p, nil
		}
	}
	if d.CommentID != nil && s.PublishedRevision == 0 && s.ParentID != nil {
		allowed, e := q.CommentReplyable(ctx, dbgen.CommentReplyableParams{CommentID: *s.ParentID, PostID: s.PostID, ViewerID: *s.AuthorID})
		if errors.Is(e, pgx.ErrNoRows) || (e == nil && !allowed) {
			p.Reason = "PARENT_UNAVAILABLE"
			return p, nil
		}
		if e != nil {
			return p, e
		}
	}
	if d.CommentID != nil {
		allowed, e := q.ThreadReplyable(ctx, dbgen.ThreadReplyableParams{PostID: s.PostID, ViewerID: *s.AuthorID})
		if e != nil {
			return p, e
		}
		if !allowed {
			p.Reason = "TARGET_UNAVAILABLE"
			return p, nil
		}
	}
	p.Reason = "NONE"
	return p, nil
}
func (a *App) appealReviewView(ctx context.Context, q *dbgen.Queries, v dbgen.SocialAppeal, actor *Actor) (map[string]any, error) {
	result, err := appealReceipt(ctx, q, v)
	if err != nil {
		return nil, err
	}
	d, err := q.AppealOriginal(ctx, v.DecisionID)
	if err != nil {
		return nil, err
	}
	p, err := a.planAppeal(ctx, q, d)
	if err != nil {
		return nil, err
	}
	kind, target := "POST", d.PostID
	if d.CommentID != nil {
		kind, target = "COMMENT", d.CommentID
	}
	result["original"] = map[string]any{"action": d.Action, "ruleVersion": d.RuleVersion, "internalReason": d.InternalReason, "target": map[string]any{"type": kind, "id": target, "postId": p.Source.PostID, "revision": d.TargetVersion}}
	result["targetVersion"] = p.Source.Version
	result["restorationReason"] = p.Reason
	result["assignedToMe"] = v.ReviewerRef != nil && *v.ReviewerRef == actor.PrincipalID
	var preview any
	if p.Preview != nil {
		preview = map[string]any{"title": p.Preview.Title.String, "body": p.Preview.Body}
	}
	result["preview"] = preview
	return result, nil
}
func (a *App) appealReview(w http.ResponseWriter, r *http.Request, actor *Actor) (any, int, error) {
	if !actor.Has("PLATFORM_MODERATOR") {
		return nil, 0, forbidden()
	}
	aid, err := id(r, "id")
	if err != nil {
		return nil, 0, err
	}
	q := dbgen.New(a.store(r.Context()))
	v, err := q.ReviewerAppeal(r.Context(), aid)
	if err != nil {
		return nil, 0, err
	}
	result, err := a.appealReviewView(r.Context(), q, v, actor)
	return result, 200, err
}
func (a *App) claimAppeal(w http.ResponseWriter, r *http.Request, actor *Actor) (any, int, error) {
	aid, err := id(r, "id")
	if err != nil {
		return nil, 0, err
	}
	version, err := expected(r)
	if err != nil {
		return nil, 0, err
	}
	var result map[string]any
	err = a.transaction(r.Context(), actor, func(q *dbgen.Queries) error {
		if !actor.Has("PLATFORM_MODERATOR") {
			return forbidden()
		}
		v, e := q.LockAppeal(r.Context(), aid)
		if e != nil {
			return e
		}
		// Owners can SELECT their own record, but cannot act as its reviewer.
		if v.AppellantRef == actor.PrincipalID {
			return forbidden()
		}
		if v.Version != version {
			return conflict()
		}
		if v.State == "OPEN" {
			if e = q.ClaimAppeal(r.Context(), dbgen.ClaimAppealParams{ID: aid, ReviewerRef: &actor.PrincipalID}); e != nil {
				return e
			}
			v.State = "REVIEWING"
			v.Version++
			v.ReviewerRef = &actor.PrincipalID
		} else if v.State != "REVIEWING" || v.ReviewerRef == nil || *v.ReviewerRef != actor.PrincipalID {
			return conflict()
		}
		result, e = appealReceipt(r.Context(), q, v)
		return e
	})
	return result, 200, err
}
func (a *App) decideAppeal(w http.ResponseWriter, r *http.Request, actor *Actor) (any, int, error) {
	aid, err := id(r, "id")
	if err != nil {
		return nil, 0, err
	}
	version, err := expected(r)
	if err != nil {
		return nil, 0, err
	}
	var b struct {
		Result         string `json:"result"`
		AuthorReason   string `json:"authorReason"`
		TargetRevision int64  `json:"targetRevision"`
		TargetVersion  *int64 `json:"targetVersion"`
	}
	if err = decode(r, &b); err != nil {
		return nil, 0, err
	}
	b.AuthorReason = strings.TrimSpace(b.AuthorReason)
	if (b.Result != "UPHELD" && b.Result != "REVERSED") || !textValid(b.AuthorReason, 5, 1000) || b.TargetRevision < 1 || (b.TargetVersion == nil || *b.TargetVersion < 0) {
		return nil, 0, invalid("Choose an outcome, the reviewed revision and a reason shared with the author")
	}
	var result map[string]any
	err = a.transaction(r.Context(), actor, func(q *dbgen.Queries) error {
		if !actor.Has("PLATFORM_MODERATOR") {
			return forbidden()
		}
		v, e := q.LockAppeal(r.Context(), aid)
		if e != nil {
			return e
		}
		if v.AppellantRef == actor.PrincipalID {
			return forbidden()
		}
		if v.Version != version || v.State != "REVIEWING" || v.ReviewerRef == nil || *v.ReviewerRef != actor.PrincipalID {
			return conflict()
		}
		d, e := q.AppealOriginal(r.Context(), v.DecisionID)
		if e != nil {
			return e
		}
		if d.ActorRef == actor.PrincipalID || (d.ReporterRef != nil && *d.ReporterRef == actor.PrincipalID) {
			return forbidden()
		}
		if d.TargetVersion != b.TargetRevision {
			return conflict()
		}
		var parentVersion int64
		if d.PostID != nil {
			_, e = q.LockPost(r.Context(), *d.PostID)
		} else if d.CommentID != nil {
			var c dbgen.SocialComment
			c, e = q.LockComment(r.Context(), *d.CommentID)
			if e == nil {
				var parent dbgen.SocialPost
				parent, e = q.LockPost(r.Context(), c.PostID)
				parentVersion = parent.Version
			}
		} else {
			return invalid("Unsupported target")
		}
		if e != nil {
			return e
		}
		plan, e := a.planAppeal(r.Context(), q, d)
		if e != nil {
			return e
		}
		if plan.Source.Version != *b.TargetVersion {
			return failure(409, "APPEAL_TARGET_CHANGED", "The source changed. Refresh and review it again")
		}
		state, reason := "UNCHANGED", "NONE"
		if b.Result == "REVERSED" {
			state, reason = "NOT_RESTORED", plan.Reason
			if reason == "NONE" {
				if d.PostID != nil {
					if e = q.ApprovePostRevision(r.Context(), dbgen.ApprovePostRevisionParams{PostID: *d.PostID, Revision: int32(d.TargetVersion)}); e != nil {
						return e
					}
					if e = q.PublishPost(r.Context(), *d.PostID); e != nil {
						return e
					}
					parentVersion = plan.Source.Version + 1
				} else {
					if e = q.ApproveComment(r.Context(), dbgen.ApproveCommentParams{CommentID: *d.CommentID, Version: d.TargetVersion}); e != nil {
						return e
					}
					if e = q.PublishComment(r.Context(), dbgen.PublishCommentParams{ID: *d.CommentID, Body: plan.Preview.Body}); e != nil {
						return e
					}
					if plan.Source.PublishedRevision == 0 {
						if e = addEvent(r.Context(), q, "POST", plan.Source.PostID, parentVersion, "CommentPublished", map[string]any{"commentId": *d.CommentID, "revision": d.TargetVersion}); e != nil {
							return e
						}
					}
				}
				if e = addEvent(r.Context(), q, "POST", plan.Source.PostID, parentVersion, "PublicationReviewed", map[string]any{"decision": "RESTORE"}); e != nil {
					return e
				}
				state = "RESTORED"
			}
		}
		if e = q.InsertAppealDecision(r.Context(), dbgen.InsertAppealDecisionParams{ID: uuid.New(), AppealID: aid, Result: b.Result, AuthorReason: b.AuthorReason, RestorationState: state, RestorationReason: reason, ReviewerRef: actor.PrincipalID}); e != nil {
			return e
		}
		if e = q.FinishAppeal(r.Context(), dbgen.FinishAppealParams{ID: aid, State: b.Result}); e != nil {
			return e
		}
		v.State = b.Result
		v.Version++
		result, e = appealReceipt(r.Context(), q, v)
		return e
	})
	return result, 200, err
}

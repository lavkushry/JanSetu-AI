package app

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/lavkushry/JanSetu-AI/services/backend/internal/store/dbgen"
)

var (
	errUnsupportedEventType    = errors.New("unsupported projection event type")
	errUnsupportedEventVersion = errors.New("unsupported projection payload version")
	errInvalidProjectionEvent  = errors.New("invalid projection event")
)

// ProjectionErrorCode returns a fixed diagnostic without exposing event payloads
// or database errors in retained delivery metadata and worker logs.
func ProjectionErrorCode(err error) string {
	switch {
	case errors.Is(err, errUnsupportedEventType):
		return "UNSUPPORTED_EVENT_TYPE"
	case errors.Is(err, errUnsupportedEventVersion):
		return "UNSUPPORTED_PAYLOAD_VERSION"
	case errors.Is(err, errInvalidProjectionEvent):
		return "INVALID_EVENT"
	default:
		return "PROJECTION_FAILED"
	}
}

func validateProjectionEvent(event dbgen.InfraOutbox) error {
	var aggregate string
	switch event.EventType {
	case "PostReviewRequested", "PostVoteChanged", "PostRepostChanged", "HelpfulResponseChanged",
		"PublicationReviewed", "ContentRevoked", "CommentReviewRequested", "CommentRevoked", "CommentPublished":
		aggregate = "POST"
	case "ReportReceived":
		aggregate = "REPORT"
	case "CaseCreated", "ObligationChanged", "VerificationRecorded", "SafeReceiptPublished", "SafeReceiptWithdrawn":
		aggregate = "CASE"
	case "ModerationDecisionRecorded", "PublicationApprovalRecorded":
		aggregate = "MODERATION_DECISION"
	case "AppealOutcomeRecorded":
		aggregate = "APPEAL"
	case "ContentReportOutcomeRecorded":
		aggregate = "CONTENT_REPORT"
	default:
		return errUnsupportedEventType
	}
	if event.PayloadVersion != 1 && !(event.EventType == "SafeReceiptPublished" && event.PayloadVersion == 2) {
		return errUnsupportedEventVersion
	}
	if event.AggregateType != aggregate || event.AggregateID == uuid.Nil || event.AggregateVersion < 1 {
		return errInvalidProjectionEvent
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(event.Payload, &payload); err != nil || payload == nil {
		return errInvalidProjectionEvent
	}
	return nil
}

// These envelopes rebuild only their post's counts. Their complete write set is
// post -> post_stats, plus the leased outbox/dedup rows; no profile/receipt locks
// or notification inserts follow the post lock. Keep this allowlist explicit.
func postStatsOnly(event dbgen.InfraOutbox) bool {
	return event.AggregateType == "POST" && event.AggregateID != uuid.Nil && event.AggregateVersion > 0 && event.PayloadVersion == 1 &&
		(event.EventType == "PostVoteChanged" || event.EventType == "PostRepostChanged")
}

// ProjectOnce claims and fences one durable event. Projection, deduplication,
// and acknowledgement commit together; duplicate delivery is safe.
func (a *App) ProjectOnce(ctx context.Context, owner string) (bool, error) {
	q := dbgen.New(a.Worker)
	token := uuid.New()
	leaseOwner := pgtype.Text{String: owner, Valid: true}
	event, e := q.ClaimEvent(ctx, dbgen.ClaimEventParams{LeaseOwner: leaseOwner, LeaseToken: &token})
	if errors.Is(e, pgx.ErrNoRows) {
		return false, nil
	}
	if e != nil {
		return false, e
	}
	e = pgx.BeginTxFunc(ctx, a.Worker, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := dbgen.New(tx)
		// Notification foreign keys lock recipient profiles after the post. Join
		// command serialization before any row locks to avoid the reverse order
		// of commands, which lock their profile before locking the post.
		statsOnly := postStatsOnly(event)
		if !statsOnly {
			if err := q.LockIdempotency(ctx, pilotMutationLock); err != nil {
				return err
			}
		}
		claimed, e := q.LockClaim(ctx, dbgen.LockClaimParams{ID: event.ID, LeaseToken: &token, LeaseOwner: leaseOwner})
		if errors.Is(e, pgx.ErrNoRows) {
			return nil
		}
		if e != nil {
			return e
		}
		// The leased row must still belong to the reviewed write set. Never enter
		// a notification path without acquiring its ordering lock first.
		if statsOnly && !postStatsOnly(claimed) {
			return errInvalidProjectionEvent
		}
		_, e = q.EventProcessed(ctx, event.ID)
		if e != nil && !errors.Is(e, pgx.ErrNoRows) {
			return e
		}
		fresh := e == nil
		if fresh {
			// Validate before any projection. An older worker must retain future
			// events for recovery instead of acknowledging an unhandled type.
			if e = validateProjectionEvent(claimed); e != nil {
				return e
			}
		}
		if fresh && claimed.AggregateType == "POST" {
			// Lock the aggregate before taking the count snapshot so two workers
			// cannot publish counts computed on opposite sides of a mutation.
			if _, e = q.LockPost(ctx, claimed.AggregateID); e != nil {
				return e
			}
			if e = q.RebuildPostStats(ctx, claimed.AggregateID); e != nil {
				return e
			}
		}
		if fresh {
			// Other explicitly supported types need only the post-stat rebuild
			// above, or intentionally have no public projection. Private
			// operational events do not publish service progress.
			switch claimed.EventType {
			case "CommentPublished":
				var payload struct {
					CommentID uuid.UUID `json:"commentId"`
					Revision  int64     `json:"revision"`
				}
				if e = json.Unmarshal(claimed.Payload, &payload); e != nil {
					return errInvalidProjectionEvent
				}
				if payload.CommentID == uuid.Nil || payload.Revision < 1 {
					return errInvalidProjectionEvent
				}
				if e = q.DeliverReplyActivity(ctx, dbgen.DeliverReplyActivityParams{EventID: claimed.ID, CommentID: payload.CommentID, SourceVersion: pgtype.Int8{Int64: payload.Revision, Valid: true}, EventTime: claimed.CreatedAt}); e != nil {
					return e
				}
			case "ModerationDecisionRecorded", "PublicationApprovalRecorded", "AppealOutcomeRecorded", "ContentReportOutcomeRecorded":
				sourceKind := "MODERATION_DECISION"
				if claimed.EventType == "PublicationApprovalRecorded" {
					sourceKind = "PUBLICATION_APPROVAL"
				}
				if claimed.EventType == "AppealOutcomeRecorded" {
					sourceKind = "APPEAL_OUTCOME"
				}
				if claimed.EventType == "ContentReportOutcomeRecorded" {
					sourceKind = "CONTENT_REPORT_OUTCOME"
				}
				if e = q.DeliverReviewActivity(ctx, dbgen.DeliverReviewActivityParams{EventID: claimed.ID, EventTime: claimed.CreatedAt, SourceKind: sourceKind, SourceID: claimed.AggregateID, SourceVersion: claimed.AggregateVersion}); e != nil {
					return e
				}
			case "SafeReceiptPublished", "SafeReceiptWithdrawn":
				var payload struct {
					ReceiptID          uuid.UUID `json:"receiptId"`
					PublicationVersion int64     `json:"publicationVersion"`
				}
				if e = json.Unmarshal(claimed.Payload, &payload); e != nil {
					return errInvalidProjectionEvent
				}
				if payload.ReceiptID == uuid.Nil {
					return errInvalidProjectionEvent
				}
				version := claimed.AggregateVersion
				if claimed.PayloadVersion == 2 || claimed.EventType == "SafeReceiptWithdrawn" {
					if payload.PublicationVersion < 1 {
						return errInvalidProjectionEvent
					}
					version = payload.PublicationVersion
				}
				// Withdrawal is already authoritative. Processing acknowledges its
				// envelope without publishing or delivering a public notification.
				if claimed.EventType == "SafeReceiptWithdrawn" {
					break
				}
				if e = q.DeliverCaseActivity(ctx, dbgen.DeliverCaseActivityParams{EventID: claimed.ID, ReceiptID: payload.ReceiptID, SourceVersion: pgtype.Int8{Int64: version, Valid: true}, EventTime: claimed.CreatedAt}); e != nil {
					return e
				}
			}
		}
		return q.CompleteEvent(ctx, dbgen.CompleteEventParams{ID: event.ID, LeaseToken: &token, LeaseOwner: leaseOwner})
	})
	if e != nil {
		if retryErr := q.RetryEvent(ctx, dbgen.RetryEventParams{ID: event.ID, LeaseToken: &token, ErrorCode: ProjectionErrorCode(e)}); retryErr != nil {
			return true, errors.Join(e, retryErr)
		}
	}
	return true, e
}
func (a *App) RunWorker(ctx context.Context) error {
	owner := uuid.NewString()
	for {
		worked, e := a.ProjectOnce(ctx, owner)
		if e != nil && ctx.Err() == nil {
			return e
		}
		if ctx.Err() != nil {
			return nil
		}
		if !worked {
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(500 * time.Millisecond):
			}
		}
	}
}

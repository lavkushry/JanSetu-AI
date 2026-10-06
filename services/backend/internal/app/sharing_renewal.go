package app

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/lavkushry/JanSetu-AI/services/backend/internal/store/dbgen"
)

func sharingUnavailable() error {
	return failure(409, "SHARING_CHANGED", "Sharing permission or public progress changed. Refresh the sharing review")
}

// Renewing permission changes eligibility only. A separate, current publisher
// review must create a new public revision, never restore an old decision.
func (a *App) renewPublicSharing(w http.ResponseWriter, r *http.Request, actor *Actor) (any, int, error) {
	reportID, err := id(r, "id")
	if err != nil {
		return nil, 0, err
	}
	requestID, err := id(r, "requestId")
	if err != nil {
		return nil, 0, err
	}
	version, err := expected(r)
	if err != nil {
		return nil, 0, err
	}
	var b struct {
		ClientRequestID    uuid.UUID `json:"clientRequestId"`
		PublicationVersion int64     `json:"publicationVersion"`
		Confirmed          bool      `json:"confirmed"`
	}
	if err = decode(r, &b); err != nil {
		return nil, 0, err
	}
	if b.ClientRequestID == uuid.Nil || b.PublicationVersion < 1 || !b.Confirmed {
		return nil, 0, invalid("Confirm permission for future reviewed public progress")
	}
	if _, err = a.sharingOwner(r, actor, reportID); err != nil {
		return nil, 0, err
	}
	data, err := a.createCommand(r, actor, "RenewPublicSharing", map[string]any{"reportId": reportID, "requestId": requestID, "version": version, "input": b}, func(q *dbgen.Queries) (uuid.UUID, any, error) {
		target, e := q.LockOwnerWithdrawalRequest(r.Context(), dbgen.LockOwnerWithdrawalRequestParams{ID: requestID, ReportID: reportID})
		if e != nil {
			return uuid.Nil, nil, e
		}
		if target.Version != version {
			return uuid.Nil, nil, conflict()
		}
		if target.State != "APPROVED" {
			return uuid.Nil, nil, sharingUnavailable()
		}
		old, e := q.OwnerSharingRenewalByClient(r.Context(), dbgen.OwnerSharingRenewalByClientParams{ReportID: reportID, ClientRequestID: b.ClientRequestID})
		if e == nil {
			if old.RequestID != requestID || old.PublicationVersion != b.PublicationVersion {
				return uuid.Nil, nil, failure(409, "IDEMPOTENCY_CONFLICT", "This permission ID was used for different content")
			}
			return old.ID, map[string]any{"requestId": requestID}, nil
		}
		if !errors.Is(e, pgx.ErrNoRows) {
			return uuid.Nil, nil, e
		}
		active, e := q.OwnerActiveSharingRenewal(r.Context(), requestID)
		if e == nil {
			if active.PublicationVersion != b.PublicationVersion {
				return uuid.Nil, nil, sharingUnavailable()
			}
			return active.ID, map[string]any{"requestId": requestID}, nil
		}
		if !errors.Is(e, pgx.ErrNoRows) {
			return uuid.Nil, nil, e
		}
		review, e := q.OwnerSharingRenewalReview(r.Context(), dbgen.OwnerSharingRenewalReviewParams{RequestID: requestID, ReportID: reportID})
		if errors.Is(e, pgx.ErrNoRows) {
			return uuid.Nil, nil, sharingUnavailable()
		}
		if e != nil {
			return uuid.Nil, nil, e
		}
		if review.PublicationVersion != b.PublicationVersion {
			return uuid.Nil, nil, publicationConflict()
		}
		if !review.CanRenew {
			return uuid.Nil, nil, sharingUnavailable()
		}
		renewalID := uuid.New()
		if e = q.InsertSharingRenewal(r.Context(), dbgen.InsertSharingRenewalParams{ID: renewalID, RequestID: requestID, ReportID: reportID, ClientRequestID: b.ClientRequestID, PublicationVersion: b.PublicationVersion}); e != nil {
			return uuid.Nil, nil, e
		}
		return renewalID, map[string]any{"requestId": requestID}, nil
	})
	if err != nil {
		return nil, 0, err
	}
	var saved struct{ RequestID uuid.UUID }
	if err = json.Unmarshal(data, &saved); err != nil {
		return nil, 0, err
	}
	result, err := ownerWithdrawalReceipt(r, dbgen.New(a.store(r.Context())), reportID, saved.RequestID)
	return result, 201, err
}

func (a *App) cancelSharingRenewal(w http.ResponseWriter, r *http.Request, actor *Actor) (any, int, error) {
	reportID, err := id(r, "id")
	if err != nil {
		return nil, 0, err
	}
	requestID, err := id(r, "requestId")
	if err != nil {
		return nil, 0, err
	}
	renewalID, err := id(r, "renewalId")
	if err != nil {
		return nil, 0, err
	}
	version, err := expected(r)
	if err != nil {
		return nil, 0, err
	}
	var b struct {
		PublicationVersion int64 `json:"publicationVersion"`
		Confirmed          bool  `json:"confirmed"`
	}
	if err = decode(r, &b); err != nil {
		return nil, 0, err
	}
	if !b.Confirmed || b.PublicationVersion < 1 {
		return nil, 0, invalid("Confirm undoing permission while public progress is withdrawn")
	}
	if _, err = a.sharingOwner(r, actor, reportID); err != nil {
		return nil, 0, err
	}
	var result any
	err = a.transaction(r.Context(), actor, func(q *dbgen.Queries) error {
		// Keep request -> renewal ordering consistent with renewal creation.
		if _, e := q.LockOwnerWithdrawalRequest(r.Context(), dbgen.LockOwnerWithdrawalRequestParams{ID: requestID, ReportID: reportID}); e != nil {
			return e
		}
		n, e := q.LockOwnerSharingRenewal(r.Context(), dbgen.LockOwnerSharingRenewalParams{ID: renewalID, RequestID: requestID, ReportID: reportID})
		if e != nil {
			return e
		}
		if n.Version != version {
			return conflict()
		}
		if n.State != "CANCELLED" {
			review, e := q.OwnerSharingRenewalReview(r.Context(), dbgen.OwnerSharingRenewalReviewParams{RequestID: requestID, ReportID: reportID})
			if errors.Is(e, pgx.ErrNoRows) {
				return sharingUnavailable()
			}
			if e != nil {
				return e
			}
			if review.PublicationVersion != b.PublicationVersion {
				return publicationConflict()
			}
			if !review.CanUndo {
				return sharingUnavailable()
			}
			if e = q.CancelSharingRenewal(r.Context(), renewalID); e != nil {
				return e
			}
		}
		result, e = ownerWithdrawalReceipt(r, q, reportID, requestID)
		return e
	})
	return result, 200, err
}

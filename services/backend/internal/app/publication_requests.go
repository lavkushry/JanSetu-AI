package app

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/lavkushry/JanSetu-AI/services/backend/internal/store/dbgen"
)

func ownerWithdrawalDTO(v dbgen.OwnerWithdrawalRequestsRow) any {
	var outcome any
	if v.Result.Valid {
		outcome = map[string]any{"result": v.Result.String, "reason": v.ResidentReason.String, "decidedAt": timestamp(v.DecidedAt)}
	}
	var sharingReview any
	if v.SharingPublicationVersion.Valid {
		var renewal any
		if v.RenewalID != nil {
			renewal = map[string]any{"id": v.RenewalID, "state": v.RenewalState.String, "version": v.RenewalVersion.Int64, "publicationVersion": v.RenewalPublicationVersion.Int64, "createdAt": timestamp(v.RenewedAt), "cancelledAt": timestamp(v.RenewalCancelledAt)}
		}
		sharingReview = map[string]any{"publicationVersion": v.SharingPublicationVersion.Int64, "canRenew": v.CanRenew, "canUndo": v.CanUndo, "renewal": renewal}
	}
	return map[string]any{"id": v.ID, "publicationVersion": v.PublicationVersion, "reasonCode": v.ReasonCode, "state": v.State, "version": v.Version, "createdAt": timestamp(v.CreatedAt), "outcome": outcome, "sharingReview": sharingReview}
}
func ownerWithdrawalReceipt(r *http.Request, q *dbgen.Queries, reportID, requestID uuid.UUID) (any, error) {
	row, err := q.OwnerWithdrawalReceipt(r.Context(), dbgen.OwnerWithdrawalReceiptParams{ID: requestID, ReportID: reportID})
	if err != nil {
		return nil, err
	}
	return ownerWithdrawalDTO(dbgen.OwnerWithdrawalRequestsRow(row)), nil
}
func (a *App) sharingOwner(r *http.Request, actor *Actor, reportID uuid.UUID) (dbgen.OwnedSharingReportRow, error) {
	if err := require(actor); err != nil {
		return dbgen.OwnedSharingReportRow{}, err
	}
	if _, err := a.ownedAliases(r.Context()); err != nil {
		return dbgen.OwnedSharingReportRow{}, err
	}
	return dbgen.New(a.store(r.Context())).OwnedSharingReport(r.Context(), reportID)
}
func (a *App) myPublicSharing(w http.ResponseWriter, r *http.Request, actor *Actor) (any, int, error) {
	reportID, err := id(r, "id")
	if err != nil {
		return nil, 0, err
	}
	report, err := a.sharingOwner(r, actor, reportID)
	if err != nil {
		return nil, 0, err
	}
	q := dbgen.New(a.store(r.Context()))
	rows, err := q.OwnerWithdrawalRequests(r.Context(), reportID)
	if err != nil {
		return nil, 0, err
	}
	items := []any{}
	for _, v := range rows {
		items = append(items, ownerWithdrawalDTO(v))
	}
	blocked, err := q.OwnerWithdrawalBlocked(r.Context(), reportID)
	if err != nil {
		return nil, 0, err
	}
	var preview any
	if report.ReceiptID != nil {
		v, e := q.Receipt(r.Context(), *report.ReceiptID)
		if e == nil {
			preview = map[string]any{"receiptId": v.ID, "title": v.Title, "summary": v.SafeSummary, "area": v.AreaLabel, "version": v.PublicationVersion}
		}
		if e != nil && !errors.Is(e, pgx.ErrNoRows) {
			return nil, 0, e
		}
	}
	var permissionRequest any
	permission, e := q.OwnerCurrentSharingRequest(r.Context(), reportID)
	if e == nil {
		permissionRequest = ownerWithdrawalDTO(dbgen.OwnerWithdrawalRequestsRow(permission))
	}
	if e != nil && !errors.Is(e, pgx.ErrNoRows) {
		return nil, 0, e
	}
	return map[string]any{"publication": preview, "blocked": blocked || report.PublicationPreference == "PRIVATE", "requests": items, "permissionRequest": permissionRequest}, 200, nil
}

// Stable client IDs survive a lost response or expired command key. Stored
// command responses contain only the request ID; retries hydrate its live outcome.
func (a *App) requestPublicationWithdrawal(w http.ResponseWriter, r *http.Request, actor *Actor) (any, int, error) {
	reportID, err := id(r, "id")
	if err != nil {
		return nil, 0, err
	}
	if _, err = creationKey(r); err != nil {
		return nil, 0, err
	}
	var b struct {
		ClientRequestID    uuid.UUID `json:"clientRequestId"`
		PublicationVersion int64     `json:"publicationVersion"`
		ReasonCode         string    `json:"reasonCode"`
		Confirmed          bool      `json:"confirmed"`
	}
	if err = decode(r, &b); err != nil {
		return nil, 0, err
	}
	if b.ClientRequestID == uuid.Nil || b.PublicationVersion < 1 || !b.Confirmed || (b.ReasonCode != "PRIVACY" && b.ReasonCode != "LOCATION" && b.ReasonCode != "SHARING_PREFERENCE") {
		return nil, 0, invalid("Choose a sharing concern and confirm the reviewed public revision")
	}
	if _, err = a.sharingOwner(r, actor, reportID); err != nil {
		return nil, 0, err
	}
	data, err := a.createCommand(r, actor, "RequestPublicationWithdrawal", map[string]any{"reportId": reportID, "input": b}, func(q *dbgen.Queries) (uuid.UUID, any, error) {
		report, e := q.OwnedSharingReport(r.Context(), reportID)
		if e != nil {
			return uuid.Nil, nil, e
		}
		old, e := q.OwnerWithdrawalByClient(r.Context(), dbgen.OwnerWithdrawalByClientParams{ReportID: reportID, ClientRequestID: b.ClientRequestID})
		if e == nil {
			if old.PublicationVersion != b.PublicationVersion || old.ReasonCode != b.ReasonCode {
				return uuid.Nil, nil, failure(409, "IDEMPOTENCY_CONFLICT", "This request ID was already used for different content")
			}
			return old.ID, map[string]any{"id": old.ID}, nil
		}
		if !errors.Is(e, pgx.ErrNoRows) {
			return uuid.Nil, nil, e
		}
		if report.ReceiptID == nil || report.CaseID == nil {
			return uuid.Nil, nil, failure(409, "PUBLICATION_UNAVAILABLE", "No reviewed public progress is currently available")
		}
		existing, e := q.OwnerWithdrawalBySnapshot(r.Context(), dbgen.OwnerWithdrawalBySnapshotParams{ReportID: reportID, ReceiptID: *report.ReceiptID, PublicationVersion: b.PublicationVersion})
		if e == nil {
			if existing.ReasonCode != b.ReasonCode {
				return uuid.Nil, nil, failure(409, "REQUEST_EXISTS", "A request already records this reviewed public revision")
			}
			return existing.ID, map[string]any{"id": existing.ID}, nil
		}
		if !errors.Is(e, pgx.ErrNoRows) {
			return uuid.Nil, nil, e
		}
		current, e := q.Receipt(r.Context(), *report.ReceiptID)
		if errors.Is(e, pgx.ErrNoRows) {
			return uuid.Nil, nil, failure(409, "PUBLICATION_UNAVAILABLE", "No reviewed public progress is currently available")
		}
		if e != nil {
			return uuid.Nil, nil, e
		}
		if current.PublicationVersion != b.PublicationVersion {
			return uuid.Nil, nil, publicationConflict()
		}
		blocked, e := q.OwnerWithdrawalBlocked(r.Context(), reportID)
		if e != nil {
			return uuid.Nil, nil, e
		}
		if blocked || report.PublicationPreference != "SANITIZED_RECEIPT" {
			return uuid.Nil, nil, failure(409, "SHARING_PAUSED", "An active sharing request or private preference prevents another request")
		}
		requestID := uuid.New()
		if e = q.InsertWithdrawalRequest(r.Context(), dbgen.InsertWithdrawalRequestParams{ID: requestID, ReportID: reportID, CaseID: *report.CaseID, ReceiptID: *report.ReceiptID, ClientRequestID: b.ClientRequestID, PublicationVersion: b.PublicationVersion, ReasonCode: b.ReasonCode}); e != nil {
			return uuid.Nil, nil, e
		}
		return requestID, map[string]any{"id": requestID}, nil
	})
	if err != nil {
		return nil, 0, err
	}
	var saved struct{ ID uuid.UUID }
	if err = json.Unmarshal(data, &saved); err != nil {
		return nil, 0, err
	}
	result, err := ownerWithdrawalReceipt(r, dbgen.New(a.store(r.Context())), reportID, saved.ID)
	return result, 201, err
}
func (a *App) cancelPublicationWithdrawal(w http.ResponseWriter, r *http.Request, actor *Actor) (any, int, error) {
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
		Confirmed bool `json:"confirmed"`
	}
	if err = decode(r, &b); err != nil {
		return nil, 0, err
	}
	if !b.Confirmed {
		return nil, 0, invalid("Confirm cancellation of this request")
	}
	if _, err = a.sharingOwner(r, actor, reportID); err != nil {
		return nil, 0, err
	}
	var result any
	err = a.transaction(r.Context(), actor, func(q *dbgen.Queries) error {
		v, e := q.LockOwnerWithdrawalRequest(r.Context(), dbgen.LockOwnerWithdrawalRequestParams{ID: requestID, ReportID: reportID})
		if e != nil {
			return e
		}
		if v.Version != version {
			return conflict()
		}
		if v.State == "CANCELLED" {
			result, e = ownerWithdrawalReceipt(r, q, reportID, requestID)
			return e
		}
		if v.State != "REQUESTED" {
			return failure(409, "REQUEST_DECIDED", "This request already has a publisher decision")
		}
		if e = q.InsertWithdrawalCancel(r.Context(), requestID); e != nil {
			return e
		}
		if e = q.DecideWithdrawalRequest(r.Context(), dbgen.DecideWithdrawalRequestParams{ID: requestID, State: "CANCELLED"}); e != nil {
			return e
		}
		result, e = ownerWithdrawalReceipt(r, q, reportID, requestID)
		return e
	})
	return result, 200, err
}
func withdrawalReviewDTO(v dbgen.OpsPublicationWithdrawalReview) any {
	var outcome any
	if v.Result.Valid {
		outcome = map[string]any{"result": v.Result.String, "reason": v.ResidentReason.String, "internalReason": v.InternalReason.String, "decidedAt": timestamp(v.DecidedAt)}
	}
	return map[string]any{"id": v.ID, "caseId": v.CaseID, "version": v.Version, "state": v.State, "reasonCode": v.ReasonCode, "createdAt": timestamp(v.CreatedAt), "requestedPublicationVersion": v.PublicationVersion, "caseVersion": v.CaseVersion,
		"publication": map[string]any{"receiptId": v.ReceiptID, "title": v.Title, "summary": v.SafeSummary, "area": v.AreaLabel, "version": v.CurrentPublicationVersion, "state": v.PublicationState}, "outcome": outcome}
}
func (a *App) publicationWithdrawalQueue(w http.ResponseWriter, r *http.Request, actor *Actor) (any, int, error) {
	if err := require(actor); err != nil {
		return nil, 0, err
	}
	if !actor.Has("PUBLISHER") {
		return nil, 0, forbidden()
	}
	state := r.URL.Query().Get("state")
	if state == "" {
		state = "REQUESTED"
	}
	if state != "REQUESTED" && state != "APPROVED" && state != "DECLINED" && state != "CANCELLED" {
		return nil, 0, invalid("Choose a withdrawal request state")
	}
	rows, err := dbgen.New(a.store(r.Context())).WithdrawalQueue(r.Context(), state)
	items := []any{}
	for _, v := range rows {
		items = append(items, withdrawalReviewDTO(v))
	}
	return map[string]any{"items": items}, 200, err
}
func (a *App) publicationWithdrawalDetail(w http.ResponseWriter, r *http.Request, actor *Actor) (any, int, error) {
	if err := require(actor); err != nil {
		return nil, 0, err
	}
	if !actor.Has("PUBLISHER") {
		return nil, 0, forbidden()
	}
	requestID, err := id(r, "id")
	if err != nil {
		return nil, 0, err
	}
	v, err := dbgen.New(a.store(r.Context())).WithdrawalReview(r.Context(), requestID)
	if err != nil {
		return nil, 0, err
	}
	return withdrawalReviewDTO(v), 200, nil
}
func (a *App) reviewPublicationWithdrawal(w http.ResponseWriter, r *http.Request, actor *Actor) (any, int, error) {
	requestID, err := id(r, "id")
	if err != nil {
		return nil, 0, err
	}
	version, err := expected(r)
	if err != nil {
		return nil, 0, err
	}
	var b struct {
		Result             string `json:"result"`
		CaseVersion        int64  `json:"caseVersion"`
		PublicationVersion int64  `json:"publicationVersion"`
		InternalReason     string `json:"internalReason"`
		ResidentReason     string `json:"residentReason"`
		Reviewed           bool   `json:"reviewed"`
	}
	if err = decode(r, &b); err != nil {
		return nil, 0, err
	}
	b.InternalReason = strings.TrimSpace(b.InternalReason)
	b.ResidentReason = strings.TrimSpace(b.ResidentReason)
	if !b.Reviewed || (b.Result != "APPROVED" && b.Result != "DECLINED") || b.CaseVersion < 1 || b.PublicationVersion < 1 || !textValid(b.InternalReason, 5, 1000) || !textValid(b.ResidentReason, 5, 1000) {
		return nil, 0, invalid("Review the current public preview and provide separate private and resident-facing reasons")
	}
	var result any
	err = a.transaction(r.Context(), actor, func(q *dbgen.Queries) error {
		if !actor.Has("PUBLISHER") {
			return forbidden()
		}
		review, e := q.WithdrawalReview(r.Context(), requestID)
		if e != nil {
			return e
		}
		c, e := q.LockCase(r.Context(), review.CaseID)
		if e != nil {
			return e
		}
		v, e := q.LockWithdrawalRequest(r.Context(), requestID)
		if e != nil {
			return e
		}
		if v.Version != version {
			return conflict()
		}
		if v.State != "REQUESTED" {
			return failure(409, "REQUEST_DECIDED", "This request already has a recorded outcome")
		}
		if c.Version != b.CaseVersion {
			return conflict()
		}
		current, e := q.LockPublicationReceipt(r.Context(), v.ReceiptID)
		if e != nil {
			return e
		}
		if current.PublicationVersion != b.PublicationVersion {
			return publicationConflict()
		}
		if e = q.InsertWithdrawalDecision(r.Context(), dbgen.InsertWithdrawalDecisionParams{ID: uuid.New(), RequestID: requestID, Result: b.Result, CaseVersion: c.Version, PublicationVersion: current.PublicationVersion, InternalReason: b.InternalReason, ResidentReason: b.ResidentReason, ReviewerRef: actor.PrincipalID}); e != nil {
			return e
		}
		if b.Result == "APPROVED" {
			if _, e = withdrawReviewedReceipt(r, q, c, current, actor.PrincipalID, b.InternalReason); e != nil {
				return e
			}
		}
		if e = q.DecideWithdrawalRequest(r.Context(), dbgen.DecideWithdrawalRequestParams{ID: requestID, State: b.Result}); e != nil {
			return e
		}
		updated, e := q.WithdrawalReview(r.Context(), requestID)
		if e != nil {
			return e
		}
		result = withdrawalReviewDTO(updated)
		return nil
	})
	return result, 200, err
}

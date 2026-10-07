package app

import (
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/lavkushry/JanSetu-AI/services/backend/internal/store/dbgen"
)

func publicationConflict() error {
	return failure(409, "PUBLICATION_CONFLICT", "Public progress changed. Refresh its review before submitting again")
}
func publicationResult(rid uuid.UUID, caseVersion, publicationVersion int64, state string) any {
	return map[string]any{"receiptId": rid, "version": caseVersion, "publicationVersion": publicationVersion, "state": state}
}

func (a *App) publishReceipt(w http.ResponseWriter, r *http.Request, actor *Actor) (any, int, error) {
	cid, e := id(r, "id")
	if e != nil {
		return nil, 0, e
	}
	version, e := expected(r)
	if e != nil {
		return nil, 0, e
	}
	var b struct {
		Title              string `json:"title"`
		Summary            string `json:"summary"`
		Area               string `json:"area"`
		Reviewed           bool   `json:"reviewed"`
		PublicationVersion *int64 `json:"publicationVersion"`
		Reason             string `json:"reason"`
	}
	if e = decode(r, &b); e != nil {
		return nil, 0, e
	}
	b.Title = strings.TrimSpace(b.Title)
	b.Summary = strings.TrimSpace(b.Summary)
	b.Area = strings.TrimSpace(b.Area)
	b.Reason = strings.TrimSpace(b.Reason)
	if b.PublicationVersion == nil || *b.PublicationVersion < 0 || !textValid(b.Reason, 5, 1000) {
		return nil, 0, invalid("Provide the publication revision and a private review reason")
	}
	if !b.Reviewed || !textValid(b.Title, 5, 180) || !textValid(b.Summary, 10, 1500) || !textValid(b.Area, 3, 80) {
		return nil, 0, invalid("Review the safe public preview and confirm it contains no identifying details")
	}
	var result any
	e = a.transaction(r.Context(), actor, func(q *dbgen.Queries) error {
		if !actor.Has("PUBLISHER") {
			return forbidden()
		}
		c, e := q.LockCase(r.Context(), cid)
		if e != nil {
			return e
		}
		if c.Version != version {
			return conflict()
		}
		allowed, e := q.PublicationAllowed(r.Context(), cid)
		if e != nil {
			return e
		}
		if !allowed {
			return failure(403, "PUBLICATION_PRIVATE", "Private sharing preferences or withdrawal requests currently prevent publication")
		}
		rid := uuid.New()
		publicationVersion := int64(0)
		action := "PUBLISH"
		binding, e := q.PublicationBinding(r.Context(), cid)
		if e == nil {
			rid = binding.ReceiptID
			current, err := q.LockPublicationReceipt(r.Context(), rid)
			if err != nil {
				return err
			}
			// Owner-scoped SQL can hold this receipt independently of API
			// serialization. Recheck permission after waiting for its lock,
			// before either publication or an identical-preview no-op.
			allowed, err := q.PublicationAllowed(r.Context(), cid)
			if err != nil {
				return err
			}
			if !allowed {
				return failure(403, "PUBLICATION_PRIVATE", "Private sharing preferences or withdrawal requests currently prevent publication")
			}
			publicationVersion = current.PublicationVersion
			if publicationVersion != *b.PublicationVersion {
				return publicationConflict()
			}
			if current.PublicationState == "PUBLISHED" {
				action = "CORRECT"
				if current.ProjectionVersion == c.Version && current.Title == b.Title && current.SafeSummary == b.Summary && current.AreaLabel == b.Area {
					result = publicationResult(rid, c.Version, publicationVersion, "PUBLISHED")
					return nil
				}
			}
		} else if !errors.Is(e, pgx.ErrNoRows) {
			return e
		}
		if publicationVersion != *b.PublicationVersion {
			return publicationConflict()
		}
		publicationVersion++
		obligations, e := q.CaseObligations(r.Context(), cid)
		if e != nil {
			return e
		}
		responsibilities := []any{}
		for _, o := range obligations {
			if o.ScopeReplaced {
				continue
			}
			responsibilities = append(responsibilities, map[string]any{"agency": o.AgencyName, "state": o.State, "dueAt": timestamp(o.DueAt)})
		}
		decision := uuid.New()
		if e = q.InsertPublicationDecision(r.Context(), dbgen.InsertPublicationDecisionParams{ID: decision, CaseID: cid, CaseVersion: c.Version, Action: action, SafePayload: jsonBytes(map[string]any{"title": b.Title, "summary": b.Summary, "area": b.Area}), ReviewerRef: actor.PrincipalID, PublicationVersion: pgtype.Int8{Int64: publicationVersion, Valid: true}, InternalReason: pgtype.Text{String: b.Reason, Valid: true}}); e != nil {
			return e
		}
		if e = q.SaveReceipt(r.Context(), dbgen.SaveReceiptParams{ID: rid, Title: b.Title, SafeSummary: b.Summary, AreaLabel: b.Area, PublicState: c.State, UrgencyTier: c.UrgencyTier, FirstReportedAt: c.FirstValidReportAt, Responsibilities: jsonBytes(responsibilities), ProjectionVersion: c.Version, PublicationVersion: publicationVersion}); e != nil {
			return e
		}
		if e = q.SavePublicationBinding(r.Context(), dbgen.SavePublicationBindingParams{CaseID: cid, ReceiptID: rid, ApprovedCaseVersion: c.Version, ReviewerRef: actor.PrincipalID, DecisionRef: decision.String()}); e != nil {
			return e
		}
		if e = q.DeleteReceiptEvents(r.Context(), rid); e != nil {
			return e
		}
		if e = q.InsertReceiptEvent(r.Context(), dbgen.InsertReceiptEventParams{ReceiptID: rid, Sequence: 1, Type: "PLATFORM_RECEIVED", SafeText: "The platform received a service report.", ActorType: "PLATFORM", OccurredAt: c.FirstValidReportAt, ProjectionVersion: c.Version}); e != nil {
			return e
		}
		events, e := q.CaseEvents(r.Context(), cid)
		if e != nil {
			return e
		}
		sequence := int64(2)
		safeTexts := map[string]string{"INTAKE_REVIEWED": "A coordinator assessed the issue and proposed an agency task.", "TASK_PROPOSED": "A coordinator proposed another required restoration task.", "TASK_PREREQUISITES_ADDED": "A coordinator reviewed additional verification prerequisites before task acceptance.", "TASK_SPLIT_REQUESTED": "An agency requested review of a partial acceptance.", "TASK_SPLIT_APPROVED": "A coordinator confirmed separate accepted and remaining required tasks.", "TASK_SPLIT_REJECTED": "A coordinator rejected the proposed scope split.", "ACCEPTED": "The agency accepted responsibility for this task.", "IN_PROGRESS": "The agency reported work in progress.", "COMPLETION_CLAIMED": "The agency claimed completion. Independent verification is pending.", "VERIFIED": "An independent reviewer recorded restoration as verified.", "NOT_RESTORED": "An independent reviewer found the service was not restored.", "INSUFFICIENT": "The reviewer needs more evidence to verify restoration."}
		for _, v := range events {
			safe, ok := safeTexts[v.EventType]
			if !ok {
				continue
			}
			role := v.ActorRole
			if role == "COORDINATOR" {
				role = "PLATFORM"
			}
			if e = q.InsertReceiptEvent(r.Context(), dbgen.InsertReceiptEventParams{ReceiptID: rid, Sequence: sequence, Type: v.EventType, SafeText: safe, ActorType: role, OccurredAt: v.OccurredAt, ProjectionVersion: c.Version}); e != nil {
				return e
			}
			sequence++
		}
		if e = addEventVersion(r.Context(), q, "CASE", cid, c.Version, "SafeReceiptPublished", 2, map[string]any{"receiptId": rid, "publicationVersion": publicationVersion}); e != nil {
			return e
		}
		result = publicationResult(rid, c.Version, publicationVersion, "PUBLISHED")
		return nil
	})
	return result, 200, e
}

// withdrawReceipt revokes public visibility in the reviewed command transaction;
// delayed workers cannot restore the receipt or its earlier Activity.
func (a *App) withdrawReceipt(w http.ResponseWriter, r *http.Request, actor *Actor) (any, int, error) {
	cid, err := id(r, "id")
	if err != nil {
		return nil, 0, err
	}
	version, err := expected(r)
	if err != nil {
		return nil, 0, err
	}
	var b struct {
		PublicationVersion *int64 `json:"publicationVersion"`
		Reason             string `json:"reason"`
		Reviewed           bool   `json:"reviewed"`
	}
	if err = decode(r, &b); err != nil {
		return nil, 0, err
	}
	b.Reason = strings.TrimSpace(b.Reason)
	if !b.Reviewed || b.PublicationVersion == nil || *b.PublicationVersion < 1 || !textValid(b.Reason, 5, 1000) {
		return nil, 0, invalid("Review the withdrawal and provide its current publication revision and private reason")
	}
	var result any
	err = a.transaction(r.Context(), actor, func(q *dbgen.Queries) error {
		if !actor.Has("PUBLISHER") {
			return forbidden()
		}
		c, err := q.LockCase(r.Context(), cid)
		if err != nil {
			return err
		}
		if c.Version != version {
			return conflict()
		}
		binding, err := q.PublicationBinding(r.Context(), cid)
		if err != nil {
			return err
		}
		current, err := q.LockPublicationReceipt(r.Context(), binding.ReceiptID)
		if err != nil {
			return err
		}
		if current.PublicationVersion != *b.PublicationVersion {
			return publicationConflict()
		}
		if current.PublicationState == "WITHDRAWN" {
			result = publicationResult(current.ID, c.Version, current.PublicationVersion, "WITHDRAWN")
			return nil
		}
		next, err := withdrawReviewedReceipt(r, q, c, current, actor.PrincipalID, b.Reason)
		if err != nil {
			return err
		}
		result = publicationResult(current.ID, c.Version, next, "WITHDRAWN")
		return nil
	})
	return result, 200, err
}

// withdrawReviewedReceipt is shared by direct withdrawal and resident-request
// approval so visibility, version, history and safe event commit together.
func withdrawReviewedReceipt(r *http.Request, q *dbgen.Queries, c dbgen.OpsCaseRecord, current dbgen.SocialCaseReceipt, reviewer uuid.UUID, reason string) (int64, error) {
	if current.PublicationState == "WITHDRAWN" {
		return current.PublicationVersion, nil
	}
	next := current.PublicationVersion + 1
	decision := uuid.New()
	if err := q.InsertPublicationDecision(r.Context(), dbgen.InsertPublicationDecisionParams{
		ID: decision, CaseID: c.ID, CaseVersion: c.Version, Action: "WITHDRAW", ReviewerRef: reviewer,
		PublicationVersion: pgtype.Int8{Int64: next, Valid: true}, InternalReason: pgtype.Text{String: reason, Valid: true},
	}); err != nil {
		return 0, err
	}
	if err := q.WithdrawReceipt(r.Context(), current.ID); err != nil {
		return 0, err
	}
	if err := q.SavePublicationBinding(r.Context(), dbgen.SavePublicationBindingParams{CaseID: c.ID, ReceiptID: current.ID, ApprovedCaseVersion: c.Version, ReviewerRef: reviewer, DecisionRef: decision.String()}); err != nil {
		return 0, err
	}
	if err := addEvent(r.Context(), q, "CASE", c.ID, c.Version, "SafeReceiptWithdrawn", map[string]any{"receiptId": current.ID, "publicationVersion": next}); err != nil {
		return 0, err
	}
	return next, nil
}

// publicationReview exposes safe review fields and minimal immutable decisions
// only to a live publisher in the case scope, including withdrawn receipts.
func publicationReview(r *http.Request, q *dbgen.Queries, cid uuid.UUID, actor *Actor) (any, error) {
	if !actor.Has("PUBLISHER") {
		return nil, nil
	}
	current, err := q.PublicationStatus(r.Context(), cid)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	history, err := q.PublicationHistory(r.Context(), cid)
	if err != nil {
		return nil, err
	}
	decisions := []any{}
	for _, d := range history {
		var revision any
		if d.PublicationVersion.Valid {
			revision = d.PublicationVersion.Int64
		}
		var reason any
		if d.InternalReason.Valid {
			reason = d.InternalReason.String
		}
		decisions = append(decisions, map[string]any{"id": d.ID, "caseVersion": d.CaseVersion, "publicationVersion": revision, "action": d.Action, "reason": reason, "decidedAt": timestamp(d.DecidedAt)})
	}
	return map[string]any{"receiptId": current.ReceiptID, "state": current.PublicationState, "version": current.PublicationVersion, "caseVersion": current.CaseVersion, "title": current.Title, "summary": current.SafeSummary, "area": current.AreaLabel, "decisions": decisions}, nil
}

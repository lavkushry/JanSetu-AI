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

func caseEvent(r *http.Request, q *dbgen.Queries, actor *Actor, cid uuid.UUID, kind, role string, payload any) error {
	return q.InsertCaseEvent(r.Context(), dbgen.InsertCaseEventParams{ID: uuid.New(), CaseID: cid, EventType: kind, ActorRef: actor.PrincipalID, ActorRole: role, Payload: jsonBytes(payload)})
}
func agencyIDs(actor *Actor) []uuid.UUID {
	ids := []uuid.UUID{}
	if actor != nil {
		for _, g := range actor.Agencies {
			ids = append(ids, g.AgencyID)
		}
	}
	return ids
}
func staffAll(actor *Actor) bool { return actor.Has("COORDINATOR") || actor.Has("PUBLISHER") }
func (a *App) cases(w http.ResponseWriter, r *http.Request, actor *Actor) (any, int, error) {
	if e := require(actor); e != nil {
		return nil, 0, e
	}
	if !staffAll(actor) && len(actor.Agencies) == 0 {
		return nil, 0, forbidden()
	}
	rows, e := dbgen.New(a.store(r.Context())).Cases(r.Context(), dbgen.CasesParams{AllCases: staffAll(actor), AgencyIds: agencyIDs(actor)})
	items := []any{}
	for _, c := range rows {
		items = append(items, map[string]any{"id": c.ID, "title": c.Title, "state": c.State, "category": c.CategoryCode, "urgencyTier": c.UrgencyTier, "firstReportedAt": timestamp(c.FirstValidReportAt), "version": c.Version})
	}
	return map[string]any{"items": items}, 200, e
}
func (a *App) caseData(r *http.Request, q *dbgen.Queries, cid uuid.UUID, actor *Actor) (any, error) {
	if e := require(actor); e != nil {
		return nil, e
	}
	c, e := q.LockCase(r.Context(), cid)
	if e != nil {
		return nil, e
	}
	obligations, e := q.CaseObligations(r.Context(), cid)
	if e != nil {
		return nil, e
	}
	allowed := staffAll(actor)
	items := []any{}
	for _, o := range obligations {
		if o.AgencyID != nil && actor.Agency(*o.AgencyID, "") {
			allowed = true
		}
		items = append(items, map[string]any{"id": o.ID, "agencyId": o.AgencyID, "agency": o.AgencyName, "state": o.State, "version": o.Version, "dueAt": timestamp(o.DueAt), "workSummary": o.WorkSummary, "acceptedAt": timestamp(o.AcceptedAt), "completedAt": timestamp(o.CompletedAt), "canVerify": o.AgencyID != nil && actor.Agency(*o.AgencyID, "VERIFIER") && (o.CompletionActorRef == nil || *o.CompletionActorRef != actor.PrincipalID)})
	}
	if !allowed {
		return nil, forbidden()
	}
	events, e := q.CaseEvents(r.Context(), cid)
	if e != nil {
		return nil, e
	}
	timeline := []any{}
	for _, v := range events {
		timeline = append(timeline, map[string]any{"sequence": v.Sequence, "type": v.EventType, "role": v.ActorRole, "at": timestamp(v.OccurredAt), "detail": json.RawMessage(v.Payload)})
	}
	var receipt any
	binding, e := q.PublicationBinding(r.Context(), cid)
	if e == nil {
		receipt = binding.ReceiptID
	} else if !errors.Is(e, pgx.ErrNoRows) {
		return nil, e
	}
	return map[string]any{"id": c.ID, "category": c.CategoryCode, "state": c.State, "urgencyTier": c.UrgencyTier, "version": c.Version, "firstReportedAt": timestamp(c.FirstValidReportAt), "obligations": items, "events": timeline, "receiptId": receipt}, nil
}
func (a *App) caseDetail(w http.ResponseWriter, r *http.Request, actor *Actor) (any, int, error) {
	cid, e := id(r, "id")
	if e != nil {
		return nil, 0, e
	}
	data, e := a.caseData(r, dbgen.New(a.store(r.Context())), cid, actor)
	return data, 200, e
}
func (a *App) acceptObligation(w http.ResponseWriter, r *http.Request, actor *Actor) (any, int, error) {
	return a.obligationTransition(r, actor, "ACCEPTED")
}
func (a *App) startWork(w http.ResponseWriter, r *http.Request, actor *Actor) (any, int, error) {
	return a.obligationTransition(r, actor, "IN_PROGRESS")
}
func (a *App) claimCompletion(w http.ResponseWriter, r *http.Request, actor *Actor) (any, int, error) {
	return a.obligationTransition(r, actor, "COMPLETION_CLAIMED")
}
func (a *App) obligationTransition(r *http.Request, actor *Actor, target string) (any, int, error) {
	oid, e := id(r, "id")
	if e != nil {
		return nil, 0, e
	}
	version, e := expected(r)
	if e != nil {
		return nil, 0, e
	}
	var b struct {
		Summary string `json:"summary"`
	}
	if e = decode(r, &b); e != nil {
		return nil, 0, e
	}
	if !textValid(b.Summary, 5, 2000) {
		return nil, 0, invalid("Describe the work or decision")
	}
	var result any
	e = a.transaction(r.Context(), actor, func(q *dbgen.Queries) error {
		o, e := q.LockObligation(r.Context(), oid)
		if e != nil {
			return e
		}
		if o.AgencyID == nil || !(actor.Agency(*o.AgencyID, "AGENCY_AGENT") || actor.Agency(*o.AgencyID, "AGENCY_LEAD")) {
			return forbidden()
		}
		if o.Version != version {
			return conflict()
		}
		c, e := q.LockCase(r.Context(), o.CaseID)
		if e != nil {
			return e
		}
		if c.State == "RESOLVED" || c.State == "WITHDRAWN" {
			return forbidden()
		}
		valid := (target == "ACCEPTED" && o.State == "PROPOSED") || (target == "IN_PROGRESS" && o.State == "ACCEPTED") || (target == "COMPLETION_CLAIMED" && o.State == "IN_PROGRESS")
		if !valid {
			return failure(409, "INVALID_TRANSITION", "Refresh the current task state")
		}
		if e = q.ChangeObligation(r.Context(), dbgen.ChangeObligationParams{ID: oid, State: target, WorkSummary: b.Summary, CompletionActorRef: &actor.PrincipalID}); e != nil {
			return e
		}
		state := "ACTIVE"
		if target == "COMPLETION_CLAIMED" {
			state = "VERIFICATION_PENDING"
		}
		if e = q.ChangeCase(r.Context(), dbgen.ChangeCaseParams{ID: c.ID, State: state}); e != nil {
			return e
		}
		if e = caseEvent(r, q, actor, c.ID, target, "AGENCY", map[string]any{"obligationId": oid, "summary": b.Summary}); e != nil {
			return e
		}
		result = map[string]any{"id": oid, "state": target, "version": version + 1, "caseVersion": c.Version + 1}
		return addEvent(r.Context(), q, "CASE", c.ID, c.Version+1, "ObligationChanged", map[string]any{})
	})
	return result, 200, e
}
func (a *App) verify(w http.ResponseWriter, r *http.Request, actor *Actor) (any, int, error) {
	cid, e := id(r, "id")
	if e != nil {
		return nil, 0, e
	}
	version, e := expected(r)
	if e != nil {
		return nil, 0, e
	}
	var b struct {
		ObligationID uuid.UUID `json:"obligationId"`
		Result       string    `json:"result"`
		Reason       string    `json:"reason"`
	}
	if e = decode(r, &b); e != nil {
		return nil, 0, e
	}
	if b.Result != "VERIFIED" && b.Result != "NOT_RESTORED" && b.Result != "INSUFFICIENT" || !textValid(b.Reason, 10, 2000) {
		return nil, 0, invalid("Record the inspection result and reason")
	}
	var result any
	e = a.transaction(r.Context(), actor, func(q *dbgen.Queries) error {
		c, e := q.LockCase(r.Context(), cid)
		if e != nil {
			return e
		}
		if c.Version != version {
			return conflict()
		}
		o, e := q.LockObligation(r.Context(), b.ObligationID)
		if e != nil {
			return e
		}
		if o.CaseID != cid || o.AgencyID == nil || !actor.Agency(*o.AgencyID, "VERIFIER") || o.CompletionActorRef != nil && *o.CompletionActorRef == actor.PrincipalID {
			return forbidden()
		}
		if o.State != "COMPLETION_CLAIMED" || c.State != "VERIFICATION_PENDING" {
			return failure(409, "INVALID_TRANSITION", "Only completion claims can be verified")
		}
		if e = q.InsertVerification(r.Context(), dbgen.InsertVerificationParams{ID: uuid.New(), CaseID: cid, ObligationID: o.ID, ReviewerRef: actor.PrincipalID, Result: b.Result, Reason: b.Reason}); e != nil {
			return e
		}
		state := o.State
		if b.Result == "VERIFIED" {
			state = "VERIFIED"
		}
		if b.Result == "NOT_RESTORED" {
			state = "IN_PROGRESS"
		}
		if e = q.ChangeObligation(r.Context(), dbgen.ChangeObligationParams{ID: o.ID, State: state, WorkSummary: o.WorkSummary, CompletionActorRef: o.CompletionActorRef}); e != nil {
			return e
		}
		caseState := "VERIFICATION_PENDING"
		if b.Result == "NOT_RESTORED" {
			caseState = "REOPENED"
		}
		resolved, e := q.CaseCanResolve(r.Context(), cid)
		if e != nil {
			return e
		}
		if resolved {
			caseState = "RESOLVED"
		}
		if e = q.ChangeCase(r.Context(), dbgen.ChangeCaseParams{ID: cid, State: caseState}); e != nil {
			return e
		}
		if e = caseEvent(r, q, actor, cid, b.Result, "REVIEWER", map[string]any{"obligationId": o.ID, "reason": b.Reason}); e != nil {
			return e
		}
		result = map[string]any{"id": cid, "state": caseState, "version": version + 1}
		return addEvent(r.Context(), q, "CASE", cid, version+1, "VerificationRecorded", map[string]any{})
	})
	return result, 200, e
}
func (a *App) receiptData(r *http.Request, q *dbgen.Queries, rid uuid.UUID, actor *Actor, detail bool) (any, error) {
	v, e := q.Receipt(r.Context(), rid)
	if e != nil {
		return nil, e
	}
	following, e := q.CaseFollowing(r.Context(), dbgen.CaseFollowingParams{ProfileID: actorID(actor), ReceiptID: rid})
	if e != nil {
		return nil, e
	}
	data := map[string]any{"id": v.ID, "title": v.Title, "summary": v.SafeSummary, "area": v.AreaLabel, "state": v.PublicState, "urgencyTier": v.UrgencyTier, "firstReportedAt": timestamp(v.FirstReportedAt), "updatedAt": timestamp(v.UpdatedAt), "nextUpdateDueAt": timestamp(v.NextUpdateDueAt), "responsibilities": json.RawMessage(v.Responsibilities), "version": v.ProjectionVersion, "following": following}
	if detail {
		events, e := q.ReceiptEvents(r.Context(), rid)
		if e != nil {
			return nil, e
		}
		items := []any{}
		for _, v := range events {
			items = append(items, map[string]any{"sequence": v.Sequence, "type": v.Type, "text": v.SafeText, "actorType": v.ActorType, "at": timestamp(v.OccurredAt)})
		}
		data["events"] = items
	}
	return data, nil
}
func (a *App) getReceipt(w http.ResponseWriter, r *http.Request, actor *Actor) (any, int, error) {
	rid, e := id(r, "id")
	if e != nil {
		return nil, 0, e
	}
	data, e := a.receiptData(r, dbgen.New(a.store(r.Context())), rid, actor, true)
	return data, 200, e
}
func (a *App) caseFollow(w http.ResponseWriter, r *http.Request, actor *Actor) (any, int, error) {
	rid, e := id(r, "id")
	if e != nil {
		return nil, 0, e
	}
	var b struct {
		Following bool `json:"following"`
	}
	if e = decodeRequired(r, &b, "following"); e != nil {
		return nil, 0, e
	}
	e = a.transaction(r.Context(), actor, func(q *dbgen.Queries) error {
		if _, e := q.Receipt(r.Context(), rid); e != nil {
			return e
		}
		if b.Following {
			return q.SetCaseFollow(r.Context(), dbgen.SetCaseFollowParams{ProfileID: actor.ProfileID, ReceiptID: rid})
		}
		return q.DeleteCaseFollow(r.Context(), dbgen.DeleteCaseFollowParams{ProfileID: actor.ProfileID, ReceiptID: rid})
	})
	return map[string]any{"following": b.Following}, 200, e
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
		Title    string `json:"title"`
		Summary  string `json:"summary"`
		Area     string `json:"area"`
		Reviewed bool   `json:"reviewed"`
	}
	if e = decode(r, &b); e != nil {
		return nil, 0, e
	}
	b.Title = strings.TrimSpace(b.Title)
	b.Summary = strings.TrimSpace(b.Summary)
	b.Area = strings.TrimSpace(b.Area)
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
			return failure(403, "PUBLICATION_PRIVATE", "This report requested private progress only")
		}
		rid := uuid.New()
		binding, e := q.PublicationBinding(r.Context(), cid)
		if e == nil {
			rid = binding.ReceiptID
		} else if !errors.Is(e, pgx.ErrNoRows) {
			return e
		}
		obligations, e := q.CaseObligations(r.Context(), cid)
		if e != nil {
			return e
		}
		responsibilities := []any{}
		for _, o := range obligations {
			responsibilities = append(responsibilities, map[string]any{"agency": o.AgencyName, "state": o.State, "dueAt": timestamp(o.DueAt)})
		}
		decision := uuid.New()
		if e = q.InsertPublicationDecision(r.Context(), dbgen.InsertPublicationDecisionParams{ID: decision, CaseID: cid, CaseVersion: c.Version, SafePayload: jsonBytes(b), ReviewerRef: actor.PrincipalID}); e != nil {
			return e
		}
		if e = q.SaveReceipt(r.Context(), dbgen.SaveReceiptParams{ID: rid, Title: b.Title, SafeSummary: b.Summary, AreaLabel: b.Area, PublicState: c.State, UrgencyTier: c.UrgencyTier, FirstReportedAt: c.FirstValidReportAt, Responsibilities: jsonBytes(responsibilities), ProjectionVersion: c.Version}); e != nil {
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
		safeTexts := map[string]string{"INTAKE_REVIEWED": "A coordinator assessed the issue and proposed an agency task.", "ACCEPTED": "The agency accepted responsibility for this task.", "IN_PROGRESS": "The agency reported work in progress.", "COMPLETION_CLAIMED": "The agency claimed completion. Independent verification is pending.", "VERIFIED": "An independent reviewer recorded restoration as verified.", "NOT_RESTORED": "An independent reviewer found the service was not restored.", "INSUFFICIENT": "The reviewer needs more evidence to verify restoration."}
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
		if e = addEvent(r.Context(), q, "CASE", cid, c.Version, "SafeReceiptPublished", map[string]any{"receiptId": rid}); e != nil {
			return e
		}
		result = map[string]any{"receiptId": rid, "version": c.Version}
		return nil
	})
	return result, 200, e
}

package app

import (
	"encoding/json"
	"errors"
	"net/http"

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
	prerequisites, e := q.CaseTaskPrerequisites(r.Context(), cid)
	if e != nil {
		return nil, e
	}
	items := []any{}
	splits, e := q.CaseTaskSplits(r.Context(), cid)
	if e != nil {
		return nil, e
	}
	amendments, e := q.CasePrerequisiteAmendments(r.Context(), cid)
	if e != nil {
		return nil, e
	}
	edges, e := q.CaseTaskReadinessEdges(r.Context(), cid)
	if e != nil {
		return nil, e
	}
	open := c.State != "RESOLVED" && c.State != "WITHDRAWN"
	for _, o := range obligations {
		pendingSplit := false
		for _, s := range splits {
			pendingSplit = pendingSplit || s.TaskID == o.ID && s.State == "PENDING"
		}
		canPartiallyAccept := open && o.State == "PROPOSED" && o.RequiredForRestoration && o.ObligationType == "RESTORATION" && !pendingSplit && len(obligations)+2 <= maxRestorationTasks && canWorkTask(actor, o.AgencyID)
		availablePrerequisites := []uuid.UUID{}
		if open && o.State == "PROPOSED" && o.RequiredForRestoration && o.ObligationType == "RESTORATION" && !pendingSplit && actor.Has("COORDINATOR") {
			for _, candidate := range obligations {
				exists := false
				for _, edge := range edges {
					exists = exists || edge.Source == o.ID && edge.Target == candidate.ID
				}
				if candidate.ID != o.ID && candidate.RequiredForRestoration && candidate.ObligationType == "RESTORATION" && candidate.State != "CANCELLED" && !exists && !prerequisiteCycle(edges, o.ID, candidate.ID) {
					availablePrerequisites = append(availablePrerequisites, candidate.ID)
				}
			}
		}
		prerequisite := prerequisitesFor(prerequisites, o.ID)
		if o.AgencyID != nil && actor.Agency(*o.AgencyID, "") {
			allowed = true
		}
		items = append(items, map[string]any{"id": o.ID, "agencyId": o.AgencyID, "agency": o.AgencyName, "scope": o.ScopeText, "requiredForRestoration": o.RequiredForRestoration, "scopeReplaced": o.ScopeReplaced, "parentTaskId": o.ParentObligationID, "canPartiallyAccept": canPartiallyAccept, "availablePrerequisiteTaskIds": availablePrerequisites, "prerequisiteTaskIds": prerequisite.IDs, "blockedByTaskIds": prerequisite.BlockedIDs, "state": o.State, "version": o.Version, "dueAt": timestamp(o.DueAt), "workSummary": o.WorkSummary, "acceptedAt": timestamp(o.AcceptedAt), "completedAt": timestamp(o.CompletedAt), "canVerify": o.AgencyID != nil && actor.Agency(*o.AgencyID, "VERIFIER") && (o.CompletionActorRef == nil || *o.CompletionActorRef != actor.PrincipalID)})
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
	publication, e := publicationReview(r, q, cid, actor)
	if e != nil {
		return nil, e
	}
	blocked := false
	if actor.Has("PUBLISHER") {
		allowed, err := q.PublicationAllowed(r.Context(), cid)
		if err != nil {
			return nil, err
		}
		blocked = !allowed
	}
	return map[string]any{"canProposeTask": actor.Has("COORDINATOR") && c.State != "RESOLVED" && c.State != "WITHDRAWN" && len(obligations) < maxRestorationTasks, "publicationBlocked": blocked, "canPublish": actor.Has("PUBLISHER"), "publication": publication, "id": c.ID, "category": c.CategoryCode, "state": c.State, "urgencyTier": c.UrgencyTier, "version": c.Version, "firstReportedAt": timestamp(c.FirstValidReportAt), "obligations": items, "taskSplitRequests": caseSplitData(splits, actor, open), "prerequisiteAmendments": prerequisiteAmendmentData(amendments), "events": timeline, "receiptId": receipt}, nil
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
		if target == "ACCEPTED" {
			pending, err := q.TaskHasPendingSplit(r.Context(), oid)
			if err != nil {
				return err
			}
			if pending {
				return failure(409, "TASK_SPLIT_PENDING", "A partial acceptance awaits coordinator review. Refresh the case")
			}
		}
		if target != "ACCEPTED" {
			if e = requirePrerequisiteVerification(r, q, c.ID, oid); e != nil {
				return e
			}
		}
		if e = q.ChangeObligation(r.Context(), dbgen.ChangeObligationParams{ID: oid, State: target, WorkSummary: b.Summary, CompletionActorRef: &actor.PrincipalID}); e != nil {
			return e
		}
		tasks, e := q.CaseObligations(r.Context(), c.ID)
		if e != nil {
			return e
		}
		state := restorationState(tasks, "")
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
		if e = requirePrerequisiteVerification(r, q, cid, o.ID); e != nil {
			return e
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
		fallback := ""
		if b.Result == "NOT_RESTORED" {
			fallback = "REOPENED"
		}
		tasks, e := q.CaseObligations(r.Context(), cid)
		if e != nil {
			return e
		}
		caseState := restorationState(tasks, fallback)
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
	data := map[string]any{"id": v.ID, "title": v.Title, "summary": v.SafeSummary, "area": v.AreaLabel, "state": v.PublicState, "urgencyTier": v.UrgencyTier, "firstReportedAt": timestamp(v.FirstReportedAt), "updatedAt": timestamp(v.UpdatedAt), "nextUpdateDueAt": timestamp(v.NextUpdateDueAt), "responsibilities": json.RawMessage(v.Responsibilities), "version": v.PublicationVersion, "following": following}
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
		// An owner can stop following even when a publication is unavailable.
		// The response does not reveal whether that receipt still exists.
		if !b.Following {
			return q.DeleteCaseFollow(r.Context(), dbgen.DeleteCaseFollowParams{ProfileID: actor.ProfileID, ReceiptID: rid})
		}
		if _, e := q.Receipt(r.Context(), rid); e != nil {
			return e
		}
		return q.SetCaseFollow(r.Context(), dbgen.SetCaseFollowParams{ProfileID: actor.ProfileID, ReceiptID: rid})
	})
	return map[string]any{"following": b.Following}, 200, e
}

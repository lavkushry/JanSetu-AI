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

const localTaskAuthority = "synthetic-local-mandate-v1"

type partialAcceptance struct {
	ClientRequestID   uuid.UUID `json:"clientRequestId"`
	AcceptedScope     string    `json:"acceptedScope"`
	RemainingScope    string    `json:"remainingScope"`
	AuthorityBasisRef string    `json:"authorityBasisRef"`
	Reason            string    `json:"reason"`
}
type splitDecision struct {
	RequestID         uuid.UUID `json:"requestId"`
	Result            string    `json:"result"`
	RemainingAgencyID uuid.UUID `json:"remainingAgencyId,omitempty"`
	Reviewed          bool      `json:"reviewed"`
	Reason            string    `json:"reason"`
}

func canWorkTask(actor *Actor, agency *uuid.UUID) bool {
	return agency != nil && (actor.Agency(*agency, "AGENCY_AGENT") || actor.Agency(*agency, "AGENCY_LEAD"))
}

func (a *App) partiallyAcceptTask(w http.ResponseWriter, r *http.Request, actor *Actor) (any, int, error) {
	oid, err := id(r, "id")
	if err != nil {
		return nil, 0, err
	}
	version, err := expected(r)
	if err != nil {
		return nil, 0, err
	}
	var b partialAcceptance
	if err = decode(r, &b); err != nil {
		return nil, 0, err
	}
	b.AcceptedScope = strings.TrimSpace(b.AcceptedScope)
	b.RemainingScope = strings.TrimSpace(b.RemainingScope)
	b.Reason = strings.TrimSpace(b.Reason)
	if b.ClientRequestID == uuid.Nil || !textValid(b.AcceptedScope, 10, 1000) || !textValid(b.RemainingScope, 10, 1000) || strings.EqualFold(b.AcceptedScope, b.RemainingScope) || !textValid(b.Reason, 10, 2000) || b.AuthorityBasisRef != localTaskAuthority {
		return nil, 0, invalid("Describe distinct accepted and remaining work, with a reason and the local fictional authority basis")
	}
	var result any
	err = a.transaction(r.Context(), actor, func(q *dbgen.Queries) error {
		o, err := q.LockObligation(r.Context(), oid)
		if err != nil {
			return err
		}
		if !canWorkTask(actor, o.AgencyID) {
			return forbidden()
		}
		c, err := q.LockCase(r.Context(), o.CaseID)
		if err != nil {
			return err
		}
		existing, err := q.TaskSplitByClientID(r.Context(), dbgen.TaskSplitByClientIDParams{TaskID: oid, ClientRequestID: b.ClientRequestID})
		if err == nil {
			if existing.ProposerRef != actor.PrincipalID {
				return forbidden()
			}
			if existing.AcceptedScope != b.AcceptedScope || existing.RemainingScope != b.RemainingScope || existing.Reason != b.Reason || existing.AuthorityBasisRef != b.AuthorityBasisRef {
				return failure(409, "TASK_SPLIT_CONFLICT", "This partial acceptance was already saved with different content. Refresh the case")
			}
			result = splitResult(existing, o.Version, c.Version)
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if o.Version != version {
			return conflict()
		}
		if c.State == "RESOLVED" || c.State == "WITHDRAWN" {
			return failure(409, "CASE_CLOSED", "Closed cases cannot receive partial acceptance requests")
		}
		if o.State != "PROPOSED" || !o.RequiredForRestoration || o.ObligationType != "RESTORATION" {
			return failure(409, "INVALID_TRANSITION", "Only proposed required restoration work can be partially accepted")
		}
		pending, err := q.TaskHasPendingSplit(r.Context(), oid)
		if err != nil {
			return err
		}
		if pending {
			return failure(409, "TASK_SPLIT_PENDING", "A partial acceptance already awaits coordinator review")
		}
		if strings.EqualFold(b.AcceptedScope, o.ScopeText) || strings.EqualFold(b.RemainingScope, o.ScopeText) {
			return invalid("Define two parts of the original work; neither part can repeat the entire original scope")
		}
		tasks, err := q.CaseObligations(r.Context(), c.ID)
		if err != nil {
			return err
		}
		if len(tasks)+2 > maxRestorationTasks {
			return failure(409, "TASK_LIMIT", "A split needs room for two tasks within the eight-task case limit")
		}
		sid := uuid.New()
		if err = q.InsertTaskSplitRequest(r.Context(), dbgen.InsertTaskSplitRequestParams{ID: sid, CaseID: c.ID, TaskID: oid, ClientRequestID: b.ClientRequestID, ProposerRef: actor.PrincipalID, TaskVersion: version, AcceptedScope: b.AcceptedScope, RemainingScope: b.RemainingScope, Reason: b.Reason}); err != nil {
			return err
		}
		if err = q.TouchObligation(r.Context(), oid); err != nil {
			return err
		}
		if err = q.ChangeCase(r.Context(), dbgen.ChangeCaseParams{ID: c.ID, State: c.State}); err != nil {
			return err
		}
		if err = caseEvent(r, q, actor, c.ID, "TASK_SPLIT_REQUESTED", "AGENCY", map[string]any{"obligationId": oid, "splitRequestId": sid, "acceptedScope": b.AcceptedScope, "remainingScope": b.RemainingScope, "reason": b.Reason, "authorityBasisRef": localTaskAuthority}); err != nil {
			return err
		}
		saved, err := q.TaskSplitByClientID(r.Context(), dbgen.TaskSplitByClientIDParams{TaskID: oid, ClientRequestID: b.ClientRequestID})
		if err != nil {
			return err
		}
		result = splitResult(saved, version+1, c.Version+1)
		return addEvent(r.Context(), q, "CASE", c.ID, c.Version+1, "ObligationChanged", map[string]any{})
	})
	return result, 201, err
}

func splitResult(s dbgen.OpsTaskSplitRequest, taskVersion, caseVersion int64) any {
	return map[string]any{"id": s.ID, "state": s.State, "taskVersion": taskVersion, "caseVersion": caseVersion, "acceptedTaskId": s.AcceptedTaskID, "remainingTaskId": s.RemainingTaskID}
}

func (a *App) decideTaskSplit(w http.ResponseWriter, r *http.Request, actor *Actor) (any, int, error) {
	cid, err := id(r, "id")
	if err != nil {
		return nil, 0, err
	}
	version, err := expected(r)
	if err != nil {
		return nil, 0, err
	}
	var b splitDecision
	if err = decode(r, &b); err != nil {
		return nil, 0, err
	}
	b.Reason = strings.TrimSpace(b.Reason)
	if b.RequestID == uuid.Nil || !textValid(b.Reason, 10, 2000) || (b.Result != "APPROVE" && b.Result != "REJECT") || b.Result == "APPROVE" && (!b.Reviewed || b.RemainingAgencyID == uuid.Nil) || b.Result == "REJECT" && b.RemainingAgencyID != uuid.Nil {
		return nil, 0, invalid("Record a review reason. Approval requires a complete non-overlapping split review and an agency for all remaining work")
	}
	state := "REJECTED"
	var remainingAgency *uuid.UUID
	if b.Result == "APPROVE" {
		state = "APPROVED"
		remainingAgency = &b.RemainingAgencyID
	}
	var result any
	err = a.transaction(r.Context(), actor, func(q *dbgen.Queries) error {
		if !actor.Has("COORDINATOR") {
			return forbidden()
		}
		c, err := q.LockCase(r.Context(), cid)
		if err != nil {
			return err
		}
		s, err := q.LockTaskSplit(r.Context(), dbgen.LockTaskSplitParams{CaseID: cid, ID: b.RequestID})
		if err != nil {
			return err
		}
		if s.ProposerRef == actor.PrincipalID {
			return forbidden()
		}
		o, err := q.LockObligation(r.Context(), s.TaskID)
		if err != nil {
			return err
		}
		if s.State != "PENDING" {
			if s.ReviewerRef == nil || *s.ReviewerRef != actor.PrincipalID || s.State != state || s.DecisionReason.String != b.Reason || (s.RemainingAgencyID == nil) != (remainingAgency == nil) || remainingAgency != nil && *s.RemainingAgencyID != *remainingAgency {
				return failure(409, "TASK_SPLIT_DECISION_CONFLICT", "This partial acceptance already has a different coordinator decision. Refresh the case")
			}
			result = splitResult(s, o.Version, c.Version)
			return nil
		}
		if c.Version != version {
			return conflict()
		}
		if c.State == "RESOLVED" || c.State == "WITHDRAWN" {
			return failure(409, "CASE_CLOSED", "Closed cases cannot receive task split decisions")
		}
		if o.State != "PROPOSED" || o.Version != s.TaskVersion+1 {
			return failure(409, "TASK_SPLIT_TASK_CHANGED", "The original task changed. Refresh and review its current scope")
		}
		var acceptedID, remainingID *uuid.UUID
		if state == "APPROVED" {
			tasks, err := q.CaseObligations(r.Context(), cid)
			if err != nil {
				return err
			}
			if len(tasks)+2 > maxRestorationTasks {
				return failure(409, "TASK_LIMIT", "A split needs room for two tasks within the eight-task case limit")
			}
			agencies, err := q.AgencyDirectory(r.Context())
			if err != nil {
				return err
			}
			originalActive, remainderActive := false, false
			for _, agency := range agencies {
				originalActive = originalActive || o.AgencyID != nil && agency.ID == *o.AgencyID
				remainderActive = remainderActive || agency.ID == b.RemainingAgencyID
			}
			if !originalActive || !remainderActive {
				return invalid("Both accepted and remaining work need active agencies")
			}
			for _, task := range tasks {
				if !task.ScopeReplaced && task.AgencyID != nil && ((*task.AgencyID == *o.AgencyID && strings.EqualFold(task.ScopeText, s.AcceptedScope)) || (*task.AgencyID == b.RemainingAgencyID && strings.EqualFold(task.ScopeText, s.RemainingScope))) {
					return failure(409, "TASK_SCOPE_EXISTS", "An agency already has a task with one of these scopes. Review the existing work")
				}
			}
			prerequisites, err := q.CaseTaskPrerequisites(r.Context(), cid)
			if err != nil {
				return err
			}
			accepted, remaining := uuid.New(), uuid.New()
			acceptedID = &accepted
			remainingID = &remaining
			for _, child := range []struct {
				id     uuid.UUID
				agency *uuid.UUID
				scope  string
			}{{accepted, o.AgencyID, s.AcceptedScope}, {remaining, remainingAgency, s.RemainingScope}} {
				clientID := uuid.New()
				if err = q.InsertSplitObligation(r.Context(), dbgen.InsertSplitObligationParams{ID: child.id, CaseID: cid, AgencyID: child.agency, ScopeText: child.scope, ClientTaskID: &clientID, ParentObligationID: &o.ID, DueAt: o.DueAt}); err != nil {
					return err
				}
				for _, pid := range prerequisitesFor(prerequisites, o.ID).IDs {
					if err = q.InsertTaskPrerequisite(r.Context(), dbgen.InsertTaskPrerequisiteParams{CaseID: cid, TaskID: child.id, PrerequisiteTaskID: pid}); err != nil {
						return err
					}
				}
			}
		}
		if err = q.DecideTaskSplit(r.Context(), dbgen.DecideTaskSplitParams{ID: s.ID, State: state, ReviewerRef: &actor.PrincipalID, DecisionReason: pgtype.Text{String: b.Reason, Valid: true}, AcceptedTaskID: acceptedID, RemainingTaskID: remainingID, RemainingAgencyID: remainingAgency}); err != nil {
			return err
		}
		if state == "APPROVED" {
			if err = q.ChangeObligation(r.Context(), dbgen.ChangeObligationParams{ID: o.ID, State: "CANCELLED", WorkSummary: o.WorkSummary, CompletionActorRef: o.CompletionActorRef}); err != nil {
				return err
			}
			if err = q.ChangeObligation(r.Context(), dbgen.ChangeObligationParams{ID: *acceptedID, State: "ACCEPTED", WorkSummary: "Agency accepted this defined portion; the coordinator confirmed the complete split."}); err != nil {
				return err
			}
		} else if err = q.TouchObligation(r.Context(), o.ID); err != nil {
			return err
		}
		tasks, err := q.CaseObligations(r.Context(), cid)
		if err != nil {
			return err
		}
		nextState := c.State
		if state == "APPROVED" {
			nextState = restorationState(tasks, "")
		}
		if err = q.ChangeCase(r.Context(), dbgen.ChangeCaseParams{ID: cid, State: nextState}); err != nil {
			return err
		}
		if err = caseEvent(r, q, actor, cid, "TASK_SPLIT_"+state, "COORDINATOR", map[string]any{"obligationId": o.ID, "splitRequestId": s.ID, "acceptedTaskId": acceptedID, "remainingTaskId": remainingID, "reason": b.Reason}); err != nil {
			return err
		}
		saved, err := q.LockTaskSplit(r.Context(), dbgen.LockTaskSplitParams{CaseID: cid, ID: s.ID})
		if err != nil {
			return err
		}
		result = splitResult(saved, o.Version+1, c.Version+1)
		return addEvent(r.Context(), q, "CASE", cid, c.Version+1, "ObligationChanged", map[string]any{})
	})
	return result, 200, err
}

func caseSplitData(splits []dbgen.OpsTaskSplitRequest, actor *Actor, open bool) []any {
	items := []any{}
	for _, s := range splits {
		items = append(items, map[string]any{"id": s.ID, "taskId": s.TaskID, "acceptedScope": s.AcceptedScope, "remainingScope": s.RemainingScope, "reason": s.Reason, "authorityBasisRef": s.AuthorityBasisRef, "state": s.State, "createdAt": timestamp(s.CreatedAt), "acceptedTaskId": s.AcceptedTaskID, "remainingTaskId": s.RemainingTaskID, "remainingAgencyId": s.RemainingAgencyID, "decisionReason": s.DecisionReason.String, "canDecide": open && s.State == "PENDING" && actor.Has("COORDINATOR") && actor.PrincipalID != s.ProposerRef})
	}
	return items
}

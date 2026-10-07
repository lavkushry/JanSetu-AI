package app

import (
	"errors"
	"net/http"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/lavkushry/JanSetu-AI/services/backend/internal/store/dbgen"
)

type prerequisiteAmendmentInput struct {
	ClientAmendmentID        uuid.UUID   `json:"clientAmendmentId"`
	AddedPrerequisiteTaskIDs []uuid.UUID `json:"addedPrerequisiteTaskIds"`
	Reason                   string      `json:"reason"`
	Reviewed                 bool        `json:"reviewed"`
}

// Readiness includes replacement leaves as well as explicit prerequisite edges.
func prerequisiteCycle(edges []dbgen.CaseTaskReadinessEdgesRow, task, prerequisite uuid.UUID) bool {
	pending := []uuid.UUID{prerequisite}
	seen := map[uuid.UUID]bool{}
	for len(pending) > 0 {
		node := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if node == task {
			return true
		}
		if seen[node] {
			continue
		}
		seen[node] = true
		for _, edge := range edges {
			if edge.Source == node {
				pending = append(pending, edge.Target)
			}
		}
	}
	return false
}

func amendmentResult(s dbgen.OpsPrerequisiteAmendment, taskVersion, caseVersion int64) any {
	return map[string]any{"id": s.ID, "taskId": s.TaskID, "addedPrerequisiteTaskIds": s.AddedTaskIds, "taskVersion": taskVersion, "caseVersion": caseVersion}
}
func (a *App) amendPrerequisites(w http.ResponseWriter, r *http.Request, actor *Actor) (any, int, error) {
	oid, err := id(r, "id")
	if err != nil {
		return nil, 0, err
	}
	version, err := expected(r)
	if err != nil {
		return nil, 0, err
	}
	var b prerequisiteAmendmentInput
	if err = decode(r, &b); err != nil {
		return nil, 0, err
	}
	b.Reason = strings.TrimSpace(b.Reason)
	if b.ClientAmendmentID == uuid.Nil || !b.Reviewed || !textValid(b.Reason, 10, 2000) || len(b.AddedPrerequisiteTaskIDs) == 0 || len(b.AddedPrerequisiteTaskIDs) >= maxRestorationTasks {
		return nil, 0, invalid("Choose one to seven new prerequisite tasks and record a reviewed reason")
	}
	slices.SortFunc(b.AddedPrerequisiteTaskIDs, func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) })
	for i, pid := range b.AddedPrerequisiteTaskIDs {
		if pid == uuid.Nil || pid == oid || i > 0 && pid == b.AddedPrerequisiteTaskIDs[i-1] {
			return nil, 0, invalid("Choose distinct prerequisite tasks other than this task")
		}
	}
	var result any
	err = a.transaction(r.Context(), actor, func(q *dbgen.Queries) error {
		if !actor.Has("COORDINATOR") {
			return forbidden()
		}
		o, err := q.LockObligation(r.Context(), oid)
		if err != nil {
			return err
		}
		c, err := q.LockCase(r.Context(), o.CaseID)
		if err != nil {
			return err
		}
		saved, err := q.AmendmentByClientID(r.Context(), dbgen.AmendmentByClientIDParams{TaskID: oid, ClientAmendmentID: b.ClientAmendmentID})
		if err == nil {
			if saved.ActorRef != actor.PrincipalID {
				return forbidden()
			}
			if saved.Reason != b.Reason || !slices.Equal(saved.AddedTaskIds, b.AddedPrerequisiteTaskIDs) {
				return failure(409, "PREREQUISITE_AMENDMENT_CONFLICT", "This amendment was already saved with different content. Refresh the case")
			}
			result = amendmentResult(saved, o.Version, c.Version)
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if o.Version != version {
			return conflict()
		}
		if c.State == "RESOLVED" || c.State == "WITHDRAWN" {
			return failure(409, "CASE_CLOSED", "Closed cases cannot receive prerequisite additions")
		}
		if o.State != "PROPOSED" || !o.RequiredForRestoration || o.ObligationType != "RESTORATION" {
			return failure(409, "INVALID_TRANSITION", "Prerequisites can only be added before agency acceptance")
		}
		pending, err := q.TaskHasPendingSplit(r.Context(), oid)
		if err != nil {
			return err
		}
		if pending {
			return failure(409, "TASK_SPLIT_PENDING", "Finish the pending scope review before adding prerequisites")
		}
		tasks, err := q.CaseObligations(r.Context(), c.ID)
		if err != nil {
			return err
		}
		edges, err := q.CaseTaskReadinessEdges(r.Context(), c.ID)
		if err != nil {
			return err
		}
		for _, pid := range b.AddedPrerequisiteTaskIDs {
			eligible := false
			for _, task := range tasks {
				eligible = eligible || task.ID == pid && task.RequiredForRestoration && task.ObligationType == "RESTORATION" && task.State != "CANCELLED"
			}
			if !eligible {
				return invalid("Choose existing required restoration tasks in this case")
			}
			for _, edge := range edges {
				if edge.Source == oid && edge.Target == pid {
					return failure(409, "TASK_PREREQUISITE_EXISTS", "This prerequisite is already recorded. Refresh the case")
				}
			}
			if prerequisiteCycle(edges, oid, pid) {
				return failure(422, "TASK_PREREQUISITE_CYCLE", "This addition would create a verification cycle, including split work")
			}
		}
		aid := uuid.New()
		if err = q.InsertPrerequisiteAmendment(r.Context(), dbgen.InsertPrerequisiteAmendmentParams{ID: aid, CaseID: c.ID, TaskID: oid, ClientAmendmentID: b.ClientAmendmentID, ActorRef: actor.PrincipalID, TaskVersion: version, AddedTaskIds: b.AddedPrerequisiteTaskIDs, Reason: b.Reason}); err != nil {
			return err
		}
		for _, pid := range b.AddedPrerequisiteTaskIDs {
			if err = q.InsertAmendedPrerequisite(r.Context(), dbgen.InsertAmendedPrerequisiteParams{CaseID: c.ID, TaskID: oid, PrerequisiteTaskID: pid, AmendmentID: &aid}); err != nil {
				return err
			}
		}
		if err = q.TouchObligation(r.Context(), oid); err != nil {
			return err
		}
		if err = q.ChangeCase(r.Context(), dbgen.ChangeCaseParams{ID: c.ID, State: c.State}); err != nil {
			return err
		}
		if err = caseEvent(r, q, actor, c.ID, "TASK_PREREQUISITES_ADDED", "COORDINATOR", map[string]any{"obligationId": oid, "amendmentId": aid, "addedPrerequisiteTaskIds": b.AddedPrerequisiteTaskIDs, "reason": b.Reason}); err != nil {
			return err
		}
		saved, err = q.AmendmentByClientID(r.Context(), dbgen.AmendmentByClientIDParams{TaskID: oid, ClientAmendmentID: b.ClientAmendmentID})
		if err != nil {
			return err
		}
		result = amendmentResult(saved, version+1, c.Version+1)
		return addEvent(r.Context(), q, "CASE", c.ID, c.Version+1, "ObligationChanged", map[string]any{})
	})
	return result, 201, err
}
func prerequisiteAmendmentData(rows []dbgen.OpsPrerequisiteAmendment) []any {
	result := []any{}
	for _, s := range rows {
		result = append(result, map[string]any{"id": s.ID, "taskId": s.TaskID, "addedPrerequisiteTaskIds": s.AddedTaskIds, "reason": s.Reason, "createdAt": timestamp(s.CreatedAt), "previousTaskVersion": s.TaskVersion})
	}
	return result
}

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

const maxRestorationTasks = 8

// ClientTaskID identifies an immutable proposal for the lifetime of its case.
// A retry may carry the original case version, even after other work advances.
type taskProposal struct {
	ClientTaskID        uuid.UUID   `json:"clientTaskId"`
	AgencyID            uuid.UUID   `json:"agencyId"`
	Scope               string      `json:"scope"`
	PrerequisiteTaskIDs []uuid.UUID `json:"prerequisiteTaskIds,omitempty"`
}

func (a *App) proposeObligation(w http.ResponseWriter, r *http.Request, actor *Actor) (any, int, error) {
	cid, err := id(r, "id")
	if err != nil {
		return nil, 0, err
	}
	version, err := expected(r)
	if err != nil {
		return nil, 0, err
	}
	var b taskProposal
	if err = decode(r, &b); err != nil {
		return nil, 0, err
	}
	b.Scope = strings.TrimSpace(b.Scope)
	if b.ClientTaskID == uuid.Nil || b.AgencyID == uuid.Nil || !textValid(b.Scope, 10, 1000) {
		return nil, 0, invalid("Choose an agency and describe the distinct work required")
	}
	if len(b.PrerequisiteTaskIDs) >= maxRestorationTasks {
		return nil, 0, invalid("Choose up to seven distinct prerequisite tasks")
	}
	slices.SortFunc(b.PrerequisiteTaskIDs, func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) })
	for i, pid := range b.PrerequisiteTaskIDs {
		if pid == uuid.Nil || i > 0 && pid == b.PrerequisiteTaskIDs[i-1] {
			return nil, 0, invalid("Choose distinct existing prerequisite tasks")
		}
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

		existing, err := q.ObligationByClientID(r.Context(), dbgen.ObligationByClientIDParams{CaseID: cid, ClientTaskID: &b.ClientTaskID})
		if err == nil {
			originalPrerequisites, err := q.OriginalTaskPrerequisites(r.Context(), existing.ID)
			if err != nil {
				return err
			}
			if existing.AgencyID == nil || *existing.AgencyID != b.AgencyID || existing.ScopeText != b.Scope || !slices.Equal(originalPrerequisites, b.PrerequisiteTaskIDs) {
				return failure(409, "TASK_PROPOSAL_CONFLICT", "This task proposal was already saved with different content. Refresh the case")
			}
			result = taskResult(existing.ID, existing.State, existing.Version, c.Version)
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if c.Version != version {
			return conflict()
		}
		if c.State == "RESOLVED" || c.State == "WITHDRAWN" {
			return failure(409, "CASE_CLOSED", "Closed cases cannot receive new tasks")
		}
		agencies, err := q.AgencyDirectory(r.Context())
		if err != nil {
			return err
		}
		active := false
		for _, agency := range agencies {
			active = active || agency.ID == b.AgencyID
		}
		if !active {
			return invalid("Choose an active agency")
		}
		tasks, err := q.CaseObligations(r.Context(), cid)
		if err != nil {
			return err
		}
		if len(tasks) >= maxRestorationTasks {
			return failure(409, "TASK_LIMIT", "A case can have up to eight tasks")
		}
		for _, task := range tasks {
			if task.AgencyID != nil && *task.AgencyID == b.AgencyID && strings.EqualFold(task.ScopeText, b.Scope) {
				return failure(409, "TASK_SCOPE_EXISTS", "This agency already has a task with that work scope. Review the existing task")
			}
		}
		for _, pid := range b.PrerequisiteTaskIDs {
			eligible := false
			for _, task := range tasks {
				if task.ID == pid && task.RequiredForRestoration && task.ObligationType == "RESTORATION" && task.State != "CANCELLED" {
					eligible = true
				}
			}
			if !eligible {
				return invalid("Choose existing required restoration tasks in this case as prerequisites")
			}
		}
		oid := uuid.New()
		if err = q.InsertScopedObligation(r.Context(), dbgen.InsertScopedObligationParams{ID: oid, CaseID: cid, AgencyID: &b.AgencyID, ScopeText: b.Scope, ClientTaskID: &b.ClientTaskID}); err != nil {
			return err
		}
		for _, pid := range b.PrerequisiteTaskIDs {
			if err = q.InsertTaskPrerequisite(r.Context(), dbgen.InsertTaskPrerequisiteParams{CaseID: cid, TaskID: oid, PrerequisiteTaskID: pid}); err != nil {
				return err
			}
		}
		// An additional proposal must not hide an outstanding completion claim.
		if err = q.ChangeCase(r.Context(), dbgen.ChangeCaseParams{ID: cid, State: c.State}); err != nil {
			return err
		}
		if err = caseEvent(r, q, actor, cid, "TASK_PROPOSED", "COORDINATOR", map[string]any{"obligationId": oid, "agencyId": b.AgencyID, "summary": b.Scope, "prerequisiteTaskIds": b.PrerequisiteTaskIDs}); err != nil {
			return err
		}
		result = taskResult(oid, "PROPOSED", 1, c.Version+1)
		// Existing workers already understand this event's versioned contract.
		return addEvent(r.Context(), q, "CASE", cid, c.Version+1, "ObligationChanged", map[string]any{})
	})
	return result, 201, err
}

type taskPrerequisiteStatus struct {
	IDs, BlockedIDs []uuid.UUID
}

// Readiness follows live independent verification, not acceptance or a claim.
func prerequisitesFor(rows []dbgen.CaseTaskPrerequisitesRow, oid uuid.UUID) taskPrerequisiteStatus {
	status := taskPrerequisiteStatus{IDs: []uuid.UUID{}, BlockedIDs: []uuid.UUID{}}
	for _, row := range rows {
		if row.TaskID == oid {
			status.IDs = append(status.IDs, row.PrerequisiteTaskID)
			if row.State != "VERIFIED" {
				status.BlockedIDs = append(status.BlockedIDs, row.PrerequisiteTaskID)
			}
		}
	}
	return status
}

func requirePrerequisiteVerification(r *http.Request, q *dbgen.Queries, cid, oid uuid.UUID) error {
	rows, err := q.CaseTaskPrerequisites(r.Context(), cid)
	if err != nil {
		return err
	}
	if len(prerequisitesFor(rows, oid).BlockedIDs) > 0 {
		return failure(409, "TASK_PREREQUISITES_PENDING", "Every prerequisite needs independent verification before this work can proceed. Refresh the case")
	}
	return nil
}

func taskResult(oid uuid.UUID, state string, version, caseVersion int64) any {
	return map[string]any{"id": oid, "state": state, "version": version, "caseVersion": caseVersion}
}

// Review remains available while other agencies accept or perform their work.
// Required cancelled work remains unresolved until a reviewed policy accounts
// for the remainder; cancellation alone is not restoration.
func restorationState(tasks []dbgen.CaseObligationsRow, fallback string) string {
	required, verified, claimed, started := 0, 0, false, false
	for _, task := range tasks {
		if !task.RequiredForRestoration {
			continue
		}
		required++
		switch task.State {
		case "VERIFIED":
			verified++
			started = true
		case "COMPLETION_CLAIMED":
			claimed = true
		case "ACCEPTED", "IN_PROGRESS":
			started = true
		}
	}
	if required > 0 && required == verified {
		return "RESOLVED"
	}
	if claimed {
		return "VERIFICATION_PENDING"
	}
	if fallback == "REOPENED" {
		return fallback
	}
	if started {
		return "ACTIVE"
	}
	return "OPEN"
}

// Keep single-task labels and aggregate multi-task progress independently of
// row order. One verified task cannot label the resident's whole report verified.
func ownerTaskProgress(states []string) string {
	if len(states) == 0 {
		return "AWAITING_AGENCY_ACCEPTANCE"
	}
	seen := map[string]bool{}
	for _, state := range states {
		seen[state] = true
	}
	if len(seen) == 1 && seen["VERIFIED"] {
		return "VERIFIED"
	}
	for _, state := range []string{"DISPUTED", "COMPLETION_CLAIMED", "IN_PROGRESS", "ACCEPTED"} {
		if seen[state] {
			return state
		}
	}
	if seen["VERIFIED"] || seen["CANCELLED"] {
		return "ACTIVE"
	}
	return "AWAITING_AGENCY_ACCEPTANCE"
}

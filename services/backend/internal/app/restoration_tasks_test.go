package app

import (
	"github.com/lavkushry/JanSetu-AI/services/backend/internal/store/dbgen"
	"testing"
)

func TestRestorationProgressDoesNotDependOnTaskOrder(t *testing.T) {
	for _, sample := range []struct {
		states                []string
		caseState, ownerState string
	}{
		{[]string{"VERIFIED", "PROPOSED"}, "ACTIVE", "ACTIVE"},
		{[]string{"COMPLETION_CLAIMED", "IN_PROGRESS"}, "VERIFICATION_PENDING", "COMPLETION_CLAIMED"},
		{[]string{"VERIFIED", "VERIFIED"}, "RESOLVED", "VERIFIED"},
		{[]string{"VERIFIED", "CANCELLED"}, "ACTIVE", "ACTIVE"},
		{[]string{"PROPOSED", "PROPOSED"}, "OPEN", "AWAITING_AGENCY_ACCEPTANCE"},
	} {
		for i := 0; i < 2; i++ {
			tasks := []dbgen.CaseObligationsRow{}
			for _, state := range sample.states {
				tasks = append(tasks, dbgen.CaseObligationsRow{State: state, RequiredForRestoration: true})
			}
			if got := restorationState(tasks, ""); got != sample.caseState {
				t.Fatalf("case progress %v: %s", sample.states, got)
			}
			if got := ownerTaskProgress(sample.states); got != sample.ownerState {
				t.Fatalf("owner progress %v: %s", sample.states, got)
			}
			sample.states[0], sample.states[1] = sample.states[1], sample.states[0]
		}
	}
	if got := restorationState(nil, ""); got == "RESOLVED" {
		t.Fatal("no required tasks fabricated restoration")
	}
	if got := restorationState([]dbgen.CaseObligationsRow{{State: "IN_PROGRESS", RequiredForRestoration: true}}, "REOPENED"); got != "REOPENED" {
		t.Fatal("failed inspection was hidden", got)
	}
}

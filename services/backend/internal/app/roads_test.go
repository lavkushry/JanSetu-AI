package app

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func roadReportInput() ReportInput {
	return ReportInput{ClientSubmissionID: uuid.New(), Statement: "A fictional pothole interrupts the left lane.", Category: "ROAD", LocationLabel: "Fictional school crossing", LanguageTag: "en-IN", PublicationPreference: "PRIVATE", RoadDetails: &RoadDetails{RoadName: "Fictional Main Road", IssueKind: "POTHOLE", RoadType: "UNKNOWN", TravelDirection: "Towards the fictional school"}}
}

func TestRoadObservationValidation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*ReportInput)
	}{
		{"missing details", func(b *ReportInput) { b.RoadDetails = nil }},
		{"other category", func(b *ReportInput) { b.Category = "FOOTPATH" }},
		{"empty road", func(b *ReportInput) { b.RoadDetails.RoadName = "  " }},
		{"overlong road", func(b *ReportInput) { b.RoadDetails.RoadName = strings.Repeat("r", 181) }},
		{"overlong direction", func(b *ReportInput) { b.RoadDetails.TravelDirection = strings.Repeat("d", 101) }},
		{"unknown road type", func(b *ReportInput) { b.RoadDetails.RoadType = "AUTOMATIC_MATCH" }},
		{"unsupported diagnosis", func(b *ReportInput) { b.RoadDetails.IssueKind = "MODEL_CONFIRMED" }},
		{"future observation", func(b *ReportInput) { d := time.Now().Add(time.Hour); b.RoadDetails.ObservedAt = &d }},
		{"zero observation", func(b *ReportInput) { b.RoadDetails.ObservedAt = &time.Time{} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := roadReportInput()
			tc.change(&b)
			if b.validate() == nil {
				t.Fatal("invalid resident observation accepted")
			}
		})
	}
	b := roadReportInput()
	b.RoadDetails.RoadName = "  ಕಾಲ್ಪನಿಕ ರಸ್ತೆ  "
	b.RoadDetails.IssueKind = "BROKEN_SURFACE"
	if e := b.validate(); e != nil || b.RoadDetails.RoadName != "ಕಾಲ್ಪನಿಕ ರಸ್ತೆ" {
		t.Fatalf("Unicode manual report should be usable: %v", e)
	}
}

func TestRoadGuidanceAbstainsFromResponsibility(t *testing.T) {
	for _, kind := range []string{"UNKNOWN", "KARNATAKA_PWD", "BENGALURU_CITY", "NHAI_HIGHWAY"} {
		g := guidanceForRoad(kind)
		if g.ContractorStatus != "UNAVAILABLE" || !strings.Contains(g.Note, "confirm") || !strings.Contains(g.ContractorNote, "does not prove") {
			t.Fatal("guidance must not assign road ownership or contractor responsibility")
		}
		if (kind == "UNKNOWN" || kind == "KARNATAKA_PWD") && (len(g.Contacts) > 0 || g.Status != "UNAVAILABLE") {
			t.Fatal("unverified coverage must abstain")
		}
		for _, c := range g.Contacts {
			if !strings.HasPrefix(c.SourceURL, "https://") || c.SourceCheckedAt == "" || c.Scope == "" {
				t.Fatal("contact entry points require source, review date and scope")
			}
		}
	}
}

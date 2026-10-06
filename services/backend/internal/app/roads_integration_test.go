package app

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestRoadReportSnapshotRetryPrivacyAndTriage(t *testing.T) {
	a := testApp(t)
	owner := login(t, a, 0)
	other := login(t, a, 1)
	staff := login(t, a, 2)
	input := roadReportInput()
	input.RoadDetails.RoadName += " PRIVATE " + uuid.NewString()
	input.RoadDetails.RoadType = "NHAI_HIGHWAY"
	w := owner.request("POST", "service-reports", input, 0, uuid.NewString())
	mustStatus(t, w, 201)
	rid := parsed[struct{ ID uuid.UUID }](t, w).ID
	w = owner.request("POST", "service-reports", input, 0, uuid.NewString())
	mustStatus(t, w, 201)
	if parsed[struct{ ID uuid.UUID }](t, w).ID != rid {
		t.Fatal("road report retry created a duplicate")
	}
	changed := roadReportInput()
	changed.ClientSubmissionID = input.ClientSubmissionID
	changed.RoadDetails.RoadType = "BENGALURU_CITY"
	mustStatus(t, owner.request("POST", "service-reports", changed, 0, uuid.NewString()), 409)
	w = owner.request("GET", "my-reports/"+rid.String(), nil, 0, "")
	mustStatus(t, w, 200)
	own := parsed[struct {
		RoadDetails   *RoadDetails `json:"roadDetails"`
		RoadGuidance  RoadGuidance `json:"roadGuidance"`
		LocationLabel string       `json:"locationLabel"`
	}](t, w)
	if own.RoadDetails == nil || own.RoadDetails.RoadName != input.RoadDetails.RoadName || own.LocationLabel != input.LocationLabel || own.RoadGuidance.Status != "ENTRY_POINTS_ONLY" || len(own.RoadGuidance.Contacts) != 1 || own.RoadGuidance.Contacts[0].Helpline != "1033" || own.RoadGuidance.ContractorStatus != "UNAVAILABLE" {
		t.Fatalf("owner lost the structured observation/source snapshot: %+v", own)
	}
	mustStatus(t, other.request("GET", "my-reports/"+rid.String(), nil, 0, ""), 404)
	mustStatus(t, (client{app: a}).request("GET", "my-reports/"+rid.String(), nil, 0, ""), 401)
	w = other.request("GET", "my-reports", nil, 0, "")
	mustStatus(t, w, 200)
	if strings.Contains(w.Body.String(), input.RoadDetails.RoadName) {
		t.Fatal("road observation leaked to another resident")
	}
	w = staff.request("GET", "authority/intake", nil, 0, "")
	mustStatus(t, w, 200)
	if !strings.Contains(w.Body.String(), input.RoadDetails.RoadName) || !strings.Contains(w.Body.String(), own.RoadGuidance.Version) {
		t.Fatal("coordinator lost private road details/provenance")
	}
	mustStatus(t, owner.request("GET", "authority/intake", nil, 0, ""), 403)
	triage := map[string]any{"category": "ROAD", "agencyId": "30000000-0000-4000-8000-000000000001", "urgencyTier": 2, "reason": "Fictional road observation manually assessed for demo restoration"}
	mustStatus(t, owner.request("POST", "authority/reports/"+rid.String()+"/triage", triage, 1, ""), 403)
	w = staff.request("POST", "authority/reports/"+rid.String()+"/triage", triage, 1, "")
	mustStatus(t, w, 201)
	cid := parsed[struct {
		CaseID uuid.UUID `json:"caseId"`
	}](t, w).CaseID
	w = staff.request("GET", "authority/cases/"+cid.String(), nil, 0, "")
	mustStatus(t, w, 200)
	if !strings.Contains(w.Body.String(), `"category":"ROAD"`) {
		t.Fatal("road surface category lost in operational handoff")
	}
	w = owner.request("GET", "feed?view=FOR_YOU", nil, 0, "")
	mustStatus(t, w, 200)
	if strings.Contains(w.Body.String(), input.RoadDetails.RoadName) {
		t.Fatal("private road details leaked to public projection")
	}
	var metadata []byte
	if e := integrationAdmin.QueryRow(context.Background(), `SELECT intake_metadata FROM ops.report WHERE id=$1`, rid).Scan(&metadata); e != nil {
		t.Fatal(e)
	}
	d, g, _ := roadMetadata(metadata)
	if d == nil || g == nil || d.RoadName != input.RoadDetails.RoadName || g.Version != own.RoadGuidance.Version {
		t.Fatal("triage changed original observation/source snapshot")
	}
}

func TestRoadGuidanceAndForgedReportSources(t *testing.T) {
	base := testApp(t)
	cfg := base.Config
	cfg.PotholeBinary = ""
	a := cloneTestApp(t, cfg)
	owner := login(t, a, 0)
	for _, kind := range []string{"UNKNOWN", "NHAI_HIGHWAY", "BENGALURU_CITY", "KARNATAKA_PWD"} {
		w := owner.request("GET", "road-guidance?roadType="+kind, nil, 0, "")
		mustStatus(t, w, 200)
		if parsed[RoadGuidance](t, w).RoadType != kind {
			t.Fatal("wrong selected-road guidance")
		}
	}
	mustStatus(t, owner.request("GET", "road-guidance?roadType=AUTO_MATCH", nil, 0, ""), 422)
	input := roadReportInput()
	forged := map[string]any{"clientSubmissionId": input.ClientSubmissionID, "statement": input.Statement, "category": "ROAD", "locationLabel": input.LocationLabel, "publicationPreference": "PRIVATE", "roadDetails": map[string]any{"roadName": "Fictional road", "roadType": "UNKNOWN", "issueKind": "POTHOLE", "contractor": "Forged named contractor"}}
	mustStatus(t, owner.request("POST", "service-reports", forged, 0, uuid.NewString()), 400)
	forged["roadDetails"] = input.RoadDetails
	forged["roadGuidance"] = map[string]any{"status": "CONFIRMED", "contacts": []any{"Forged official"}}
	mustStatus(t, owner.request("POST", "service-reports", forged, 0, uuid.NewString()), 400)
	w := owner.request("GET", "capabilities", nil, 0, "")
	mustStatus(t, w, 200)
	if !strings.Contains(w.Body.String(), `"kind":"POTHOLE_DETECTION"`) || !strings.Contains(w.Body.String(), "Pothole recognition is unavailable") {
		t.Fatal("must expose the hazard model readiness boundary")
	}
}

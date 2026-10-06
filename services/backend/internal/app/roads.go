package app

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// RoadDetails records a resident's observations, never an inferred diagnosis or
// an authority assignment. Guidance is curated separately and snapshotted by us.
type RoadDetails struct {
	RoadName        string     `json:"roadName"`
	IssueKind       string     `json:"issueKind"`
	RoadType        string     `json:"roadType"`
	TravelDirection string     `json:"travelDirection,omitempty"`
	ObservedAt      *time.Time `json:"observedAt,omitempty"`
}

func roadTypeValid(v string) bool {
	return v == "UNKNOWN" || v == "BENGALURU_CITY" || v == "NHAI_HIGHWAY" || v == "KARNATAKA_PWD"
}

func (d *RoadDetails) validate() error {
	d.RoadName = strings.TrimSpace(d.RoadName)
	d.TravelDirection = strings.TrimSpace(d.TravelDirection)
	if !textValid(d.RoadName, 3, 180) || !textValid(d.TravelDirection, 0, 100) || !roadTypeValid(d.RoadType) || (d.IssueKind != "POTHOLE" && d.IssueKind != "BROKEN_SURFACE") || (d.ObservedAt != nil && (d.ObservedAt.IsZero() || d.ObservedAt.After(time.Now().Add(time.Minute)))) {
		return invalid("Check the road name, observed surface issue, road type, direction and observation time")
	}
	return nil
}

type RoadContact struct {
	Name            string `json:"name"`
	Scope           string `json:"scope"`
	Helpline        string `json:"helpline"`
	SourceURL       string `json:"sourceUrl"`
	SourceCheckedAt string `json:"sourceCheckedAt"`
}

type RoadGuidance struct {
	Version           string        `json:"version"`
	RoadType          string        `json:"roadType"`
	Status            string        `json:"status"`
	Contacts          []RoadContact `json:"contacts"`
	Note              string        `json:"note"`
	ContractorStatus  string        `json:"contractorStatus"`
	ContractorNote    string        `json:"contractorNote"`
	ContractSourceURL *string       `json:"contractSourceUrl"`
}

func guidanceForRoad(kind string) RoadGuidance {
	g := RoadGuidance{Version: "road-entry-points-2026-10-06", RoadType: kind, Status: "UNAVAILABLE", Contacts: []RoadContact{}, Note: "The responsible road authority and officer have not been confirmed. A coordinator must review responsibility.", ContractorStatus: "UNAVAILABLE", ContractorNote: "No verified contractor or award match is available. A tender title or nearby work does not prove responsibility."}
	if kind == "BENGALURU_CITY" {
		g.Contacts = append(g.Contacts, RoadContact{Name: "Greater Bengaluru Authority", Scope: "General Bengaluru civic grievance entry point; the city corporation and road officer must be confirmed.", Helpline: "1533", SourceURL: "https://gba.karnataka.gov.in/", SourceCheckedAt: "2026-10-06"})
	} else if kind == "NHAI_HIGHWAY" {
		g.Contacts = append(g.Contacts, RoadContact{Name: "NHAI highway helpline", Scope: "NHAI-managed tolled highway stretches; confirm that this road is covered.", Helpline: "1033", SourceURL: "https://ihmcl.co.in/24x7-national-highways-helpline-1033-page/", SourceCheckedAt: "2026-10-06"})
	}
	if kind == "BENGALURU_CITY" || kind == "KARNATAKA_PWD" {
		u := "https://kppp.karnataka.gov.in/"
		g.ContractSourceURL = &u
	}
	if len(g.Contacts) > 0 {
		g.Status = "ENTRY_POINTS_ONLY"
		g.Note = "These are official contact entry points for the road type you selected. They do not confirm the road owner, an individual officer, contractor responsibility or agency acceptance."
	}
	return g
}

func (a *App) roadGuidance(w http.ResponseWriter, r *http.Request, _ *Actor) (any, int, error) {
	kind := r.URL.Query().Get("roadType")
	if kind == "" {
		kind = "UNKNOWN"
	}
	if !roadTypeValid(kind) {
		return nil, 0, invalid("Choose a supported road type or unsure")
	}
	return guidanceForRoad(kind), 200, nil
}

func roadMetadata(raw []byte) (*RoadDetails, *RoadGuidance, string) {
	var metadata struct {
		Location string        `json:"locationLabel"`
		Details  *RoadDetails  `json:"roadDetails"`
		Guidance *RoadGuidance `json:"roadGuidance"`
	}
	if json.Unmarshal(raw, &metadata) != nil {
		return nil, nil, ""
	}
	return metadata.Details, metadata.Guidance, metadata.Location
}

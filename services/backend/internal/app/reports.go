package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/lavkushry/JanSetu-AI/services/backend/internal/store/dbgen"
	"github.com/lavkushry/JanSetu-AI/services/backend/internal/vault"
)

type OCRCorrection struct {
	TaskID        uuid.UUID `json:"taskId"`
	RegionID      string    `json:"regionId"`
	OriginalText  string    `json:"originalText"`
	CorrectedText string    `json:"correctedText"`
	AppliedAt     time.Time `json:"appliedAt"`
}
type ReportInput struct {
	RoadDetails           *RoadDetails    `json:"roadDetails,omitempty"`
	MediaIDs              []uuid.UUID     `json:"mediaIds,omitempty"`
	OCRCorrections        []OCRCorrection `json:"ocrCorrections,omitempty"`
	ClientSubmissionID    uuid.UUID       `json:"clientSubmissionId"`
	Statement             string          `json:"statement"`
	LanguageTag           string          `json:"languageTag"`
	Category              string          `json:"category"`
	LocationLabel         string          `json:"locationLabel"`
	PublicationPreference string          `json:"publicationPreference"`
}

func (b *ReportInput) validate() error {
	if len(b.MediaIDs) > 4 || len(b.OCRCorrections) > 500 {
		return invalid("Attach up to four photos")
	}
	seen := map[uuid.UUID]bool{}
	for _, mid := range b.MediaIDs {
		if mid == uuid.Nil || seen[mid] {
			return invalid("Choose distinct uploaded photos")
		}
		seen[mid] = true
	}
	regions := map[string]bool{}
	for _, c := range b.OCRCorrections {
		key := c.TaskID.String() + ":" + c.RegionID
		if c.TaskID == uuid.Nil || !textValid(c.RegionID, 1, 50) || !textValid(c.OriginalText, 1, 500) || !textValid(c.CorrectedText, 0, 500) || c.AppliedAt.IsZero() || c.AppliedAt.After(time.Now().Add(time.Minute)) || regions[key] {
			return invalid("Check your reviewed OCR text")
		}
		regions[key] = true
	}
	b.Statement = strings.TrimSpace(b.Statement)
	b.LocationLabel = strings.TrimSpace(b.LocationLabel)
	if b.LanguageTag == "" {
		b.LanguageTag = "en-IN"
	}
	if b.ClientSubmissionID == uuid.Nil || !textValid(b.Statement, 10, 8000) || !textValid(b.LocationLabel, 3, 180) || len(b.LanguageTag) > 40 {
		return invalid("Add a description and a location or landmark")
	}
	if b.Category != "ROAD" && b.Category != "FOOTPATH" && b.Category != "LIGHT" && b.Category != "WASTE" && b.Category != "WATER" && b.Category != "OTHER" {
		return invalid("Choose a service category")
	}
	if b.Category == "ROAD" {
		if b.RoadDetails == nil {
			return invalid("Add the observed road issue details")
		}
		if e := b.RoadDetails.validate(); e != nil {
			return e
		}
	} else if b.RoadDetails != nil {
		return invalid("Road details belong to the road surface category")
	}
	if b.PublicationPreference != "PRIVATE" && b.PublicationPreference != "SANITIZED_RECEIPT" {
		return invalid("Choose a publication preference")
	}
	return nil
}

// Vault aliases are issued before the application transaction. A failed intake
// may leave an unused alias, but can never leave a report with a public identity.
func (a *App) aliasGrant(ctx context.Context, submission uuid.UUID) (vault.Grant, error) {
	s := scope(ctx)
	grant, err := a.Vault.Aliases(ctx, s.Session, s.Trace, submission)
	if errors.Is(err, vault.ErrUnauthenticated) {
		return grant, failure(401, "AUTH_REQUIRED", "Sign in to continue")
	}
	if err == nil {
		s.Claim = grant.Claim
		s.Signature = grant.Signature
	}
	return grant, err
}
func (a *App) reportAlias(ctx context.Context, submission uuid.UUID) (uuid.UUID, error) {
	grant, err := a.aliasGrant(ctx, submission)
	return grant.Alias, err
}
func (a *App) ownedAliases(ctx context.Context) ([]uuid.UUID, error) {
	grant, err := a.aliasGrant(ctx, uuid.Nil)
	return grant.Aliases, err
}
func reportAck(id uuid.UUID, received any) map[string]any {
	return map[string]any{"id": id, "receivedAt": received, "state": "PLATFORM_RECEIVED", "message": "Received by JanSetu. Agency acceptance has not been confirmed."}
}
func (a *App) submitReport(w http.ResponseWriter, r *http.Request, actor *Actor) (any, int, error) {
	if e := require(actor); e != nil {
		return nil, 0, e
	}
	if _, e := creationKey(r); e != nil {
		return nil, 0, e
	}
	var b ReportInput
	if e := decode(r, &b); e != nil {
		return nil, 0, e
	}
	if e := b.validate(); e != nil {
		return nil, 0, e
	}
	alias, e := a.reportAlias(r.Context(), b.ClientSubmissionID)
	if e != nil {
		return nil, 0, e
	}
	hash := sha256.Sum256(jsonBytes(b))
	data, e := a.createCommand(r, actor, "SubmitReport", b, func(q *dbgen.Queries) (uuid.UUID, any, error) {
		existing, e := q.ReportBySubmission(r.Context(), b.ClientSubmissionID)
		if e == nil {
			if existing.ReporterRef == nil || *existing.ReporterRef != alias {
				return uuid.Nil, nil, unavailable()
			}
			if !bytes.Equal(hash[:], existing.RequestHash) {
				return uuid.Nil, nil, failure(409, "SUBMISSION_CONFLICT", "This report ID was already used for different content")
			}
			return existing.ID, reportAck(existing.ID, timestamp(existing.ReceivedAt)), nil
		}
		if !errors.Is(e, pgx.ErrNoRows) {
			return uuid.Nil, nil, e
		}
		rid := uuid.New()
		fields := map[string]any{"category": b.Category, "locationLabel": b.LocationLabel, "ocrCorrections": b.OCRCorrections}
		if b.RoadDetails != nil {
			fields["roadDetails"] = b.RoadDetails
			fields["roadGuidance"] = guidanceForRoad(b.RoadDetails.RoadType)
		}
		metadata := jsonBytes(fields)
		if e = q.InsertReport(r.Context(), dbgen.InsertReportParams{ID: rid, ClientSubmissionID: b.ClientSubmissionID, ReporterRef: &alias, LanguageTag: b.LanguageTag, Statement: b.Statement, PublicationPreference: b.PublicationPreference, IntakeMetadata: metadata, RequestHash: hash[:]}); e != nil {
			return uuid.Nil, nil, e
		}
		for _, mid := range b.MediaIDs {
			allowed, err := q.ReportMediaAttachable(r.Context(), dbgen.ReportMediaAttachableParams{Mid: mid, Rid: rid})
			if err != nil {
				return uuid.Nil, nil, err
			}
			if !allowed {
				return uuid.Nil, nil, invalid("Only ready photos uploaded for this report can be attached")
			}
			if e = q.AttachReportMedia(r.Context(), dbgen.AttachReportMediaParams{ReportID: rid, MediaID: mid}); e != nil {
				return uuid.Nil, nil, e
			}
		}
		for _, c := range b.OCRCorrections {
			original, err := q.ReportOCRRegion(r.Context(), dbgen.ReportOCRRegionParams{Rid: rid, Tid: c.TaskID, Region: c.RegionID})
			if err != nil || original != c.OriginalText {
				return uuid.Nil, nil, invalid("The OCR region does not belong to this report or has changed")
			}
		}
		if e = q.InsertIntakeReview(r.Context(), rid); e != nil {
			return uuid.Nil, nil, e
		}
		if e = addEvent(r.Context(), q, "REPORT", rid, 1, "ReportReceived", map[string]any{}); e != nil {
			return uuid.Nil, nil, e
		}
		stored, e := q.ReportBySubmission(r.Context(), b.ClientSubmissionID)
		if e != nil {
			return uuid.Nil, nil, e
		}
		return rid, reportAck(rid, timestamp(stored.ReceivedAt)), nil
	})
	return data, 201, e
}
func (a *App) ownProgress(r *http.Request, actor *Actor, reportID uuid.UUID) ([]any, error) {
	if e := require(actor); e != nil {
		return nil, e
	}
	aliases, e := a.ownedAliases(r.Context())
	if e != nil {
		return nil, e
	}
	q := dbgen.New(a.store(r.Context()))
	rows, e := q.OwnReportProgress(r.Context(), dbgen.OwnReportProgressParams{AliasIds: aliases, ReportID: reportID})
	if e != nil {
		return nil, e
	}
	items := []any{}
	for _, v := range rows {
		progress := "PLATFORM_RECEIVED"
		responsibilities := []any{}
		if v.CaseID != nil {
			tasks, e := a.store(r.Context()).Query(r.Context(), "SELECT agency_name,state,required_for_restoration FROM authz.owner_restoration_tasks($1)", v.ID)
			if e != nil {
				return nil, e
			}
			states := []string{}
			for tasks.Next() {
				var agency, state string
				var required bool
				if e = tasks.Scan(&agency, &state, &required); e != nil {
					tasks.Close()
					return nil, e
				}
				responsibilities = append(responsibilities, map[string]any{"agency": agency, "state": state})
				if required {
					states = append(states, state)
				}
			}
			e = tasks.Err()
			tasks.Close()
			progress = ownerTaskProgress(states)
			if e != nil {
				return nil, e
			}

		}
		mediaIDs, err := q.ReportMediaIDs(r.Context(), v.ID)
		if err != nil {
			return nil, err
		}
		details, guidance, location := roadMetadata(v.IntakeMetadata)
		items = append(items, map[string]any{"locationLabel": location, "roadDetails": details, "roadGuidance": guidance, "hasPublicationRequest": v.HasPublicationRequest, "mediaIds": mediaIDs, "id": v.ID, "statement": v.Statement, "languageTag": v.LanguageTag, "receivedAt": timestamp(v.ReceivedAt), "state": progress, "receiptId": v.ReceiptID, "responsibilities": responsibilities})
	}
	return items, nil
}
func (a *App) myReports(w http.ResponseWriter, r *http.Request, actor *Actor) (any, int, error) {
	items, e := a.ownProgress(r, actor, uuid.Nil)
	return map[string]any{"items": items}, 200, e
}
func (a *App) myReport(w http.ResponseWriter, r *http.Request, actor *Actor) (any, int, error) {
	rid, e := id(r, "id")
	if e != nil {
		return nil, 0, e
	}
	items, e := a.ownProgress(r, actor, rid)
	if e != nil {
		return nil, 0, e
	}
	for _, v := range items {
		m := v.(map[string]any)
		if m["id"] == rid {
			return m, 200, nil
		}
	}
	return nil, 0, unavailable()
}
func (a *App) intakeQueue(w http.ResponseWriter, r *http.Request, actor *Actor) (any, int, error) {
	if !actor.Has("COORDINATOR") {
		return nil, 0, forbidden()
	}
	rows, e := dbgen.New(a.store(r.Context())).IntakeQueue(r.Context())
	items := []any{}
	for _, v := range rows {
		mediaIDs, err := dbgen.New(a.store(r.Context())).ReportMediaIDs(r.Context(), v.ID)
		if err != nil {
			return nil, 0, err
		}
		items = append(items, map[string]any{"mediaIds": mediaIDs, "id": v.ID, "statement": v.Statement, "languageTag": v.LanguageTag, "publicationPreference": v.PublicationPreference, "metadata": json.RawMessage(v.IntakeMetadata), "receivedAt": timestamp(v.ReceivedAt), "version": v.Version})
	}
	return map[string]any{"items": items}, 200, e
}
func (a *App) agencies(w http.ResponseWriter, r *http.Request, actor *Actor) (any, int, error) {
	if !actor.Has("COORDINATOR") {
		return nil, 0, forbidden()
	}
	rows, e := dbgen.New(a.store(r.Context())).AgencyDirectory(r.Context())
	return map[string]any{"items": rows}, 200, e
}
func (a *App) triage(w http.ResponseWriter, r *http.Request, actor *Actor) (any, int, error) {
	rid, e := id(r, "id")
	if e != nil {
		return nil, 0, e
	}
	version, e := expected(r)
	if e != nil {
		return nil, 0, e
	}
	var b struct {
		AgencyID    uuid.UUID `json:"agencyId"`
		Category    string    `json:"category"`
		UrgencyTier int16     `json:"urgencyTier"`
		Reason      string    `json:"reason"`
	}
	if e = decode(r, &b); e != nil {
		return nil, 0, e
	}
	if b.UrgencyTier < 0 || b.UrgencyTier > 3 || !textValid(b.Reason, 5, 1000) {
		return nil, 0, invalid("Provide an urgency assessment and reason")
	}
	if b.Category != "ROAD" && b.Category != "FOOTPATH" && b.Category != "LIGHT" && b.Category != "WASTE" && b.Category != "WATER" && b.Category != "OTHER" {
		return nil, 0, invalid("Choose a service category")
	}
	var result any
	e = a.transaction(r.Context(), actor, func(q *dbgen.Queries) error {
		if !actor.Has("COORDINATOR") {
			return forbidden()
		}
		report, e := q.LockReport(r.Context(), rid)
		if e != nil {
			return e
		}
		intake, e := q.LockIntake(r.Context(), rid)
		if e != nil {
			return e
		}
		if intake.State != "PENDING" || intake.Version != version {
			return conflict()
		}
		agencies, e := q.AgencyDirectory(r.Context())
		if e != nil {
			return e
		}
		valid := false
		for _, ag := range agencies {
			if ag.ID == b.AgencyID {
				valid = true
			}
		}
		if !valid {
			return invalid("Choose an active agency")
		}
		cid := uuid.New()
		if e = q.InsertCase(r.Context(), dbgen.InsertCaseParams{ID: cid, CategoryCode: b.Category, FirstValidReportAt: report.ReceivedAt, UrgencyTier: b.UrgencyTier}); e != nil {
			return e
		}
		if e = q.InsertObservation(r.Context(), dbgen.InsertObservationParams{CaseID: cid, ReportID: rid}); e != nil {
			return e
		}
		if e = q.InsertObligation(r.Context(), dbgen.InsertObligationParams{ID: uuid.New(), CaseID: cid, AgencyID: &b.AgencyID}); e != nil {
			return e
		}
		if e = q.AssignCoordinator(r.Context(), dbgen.AssignCoordinatorParams{CaseID: cid, PrincipalID: actor.PrincipalID}); e != nil {
			return e
		}
		if e = q.LinkIntake(r.Context(), dbgen.LinkIntakeParams{ReportID: rid, CaseID: &cid, ReviewedBy: &actor.PrincipalID}); e != nil {
			return e
		}
		if e = caseEvent(r, q, actor, cid, "INTAKE_REVIEWED", "COORDINATOR", map[string]any{"reason": b.Reason, "urgencyTier": b.UrgencyTier}); e != nil {
			return e
		}
		result = map[string]any{"caseId": cid, "state": "OPEN", "version": 1, "firstReportedAt": timestamp(report.ReceivedAt)}
		return addEvent(r.Context(), q, "CASE", cid, 1, "CaseCreated", map[string]any{})
	})
	return result, 201, e
}

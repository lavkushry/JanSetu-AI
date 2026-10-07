package stream

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math"
	"time"

	"github.com/google/uuid"
)

const Topic = "jansetu.recommendation.v1"

// Envelope deliberately has no text, private media, report, OCR or identity fields.
type Envelope struct {
	SchemaVersion  int       `json:"schemaVersion"`
	EventID        uuid.UUID `json:"eventId"`
	EventType      string    `json:"eventType"`
	EntityVersion  int64     `json:"entityVersion"`
	OccurredAt     time.Time `json:"occurredAt"`
	Subject        uuid.UUID `json:"subject,omitempty"`
	Generation     int64     `json:"generation,omitempty"`
	Enabled        bool      `json:"enabled,omitempty"`
	Deleted        bool      `json:"deleted,omitempty"`
	PostID         uuid.UUID `json:"postId,omitempty"`
	Revision       int32     `json:"revision,omitempty"`
	Eligible       bool      `json:"eligible,omitempty"`
	ExposureID     uuid.UUID `json:"exposureId,omitempty"`
	Action         string    `json:"action,omitempty"`
	NormalizedRead float64   `json:"normalizedRead,omitempty"`
	ModelVersion   string    `json:"modelVersion,omitempty"`
	PolicyVersion  string    `json:"policyVersion,omitempty"`
}

func Decode(data []byte) (Envelope, error) {
	var e Envelope
	if len(data) > 8192 {
		return e, errors.New("stream envelope too large")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(&e); err != nil {
		return e, err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return e, errors.New("trailing stream payload")
	}
	if e.SchemaVersion != 1 || e.EventID == uuid.Nil || e.EntityVersion < 1 || e.OccurredAt.IsZero() || e.OccurredAt.After(time.Now().Add(time.Minute)) {
		return e, errors.New("invalid stream identity or version")
	}
	switch e.EventType {
	case "CONTROL":
		if e.Subject == uuid.Nil || e.Generation < 1 || (e.Deleted && e.Enabled) || e.PostID != uuid.Nil || e.ExposureID != uuid.Nil || e.Action != "" || e.NormalizedRead != 0 || e.ModelVersion != "" || e.PolicyVersion != "" || e.Eligible || e.Revision != 0 {
			return e, errors.New("invalid consent control")
		}
	case "CONTENT":
		if e.PostID == uuid.Nil || e.Revision < 0 || (e.Eligible && e.Revision == 0) || e.Subject != uuid.Nil || e.Generation != 0 || e.ExposureID != uuid.Nil || e.Action != "" || e.NormalizedRead != 0 || e.ModelVersion != "" || e.PolicyVersion != "" || e.Enabled || e.Deleted {
			return e, errors.New("invalid content reference")
		}
	case "INTERACTION":
		validAction := e.Action == "READ" || e.Action == "SKIP" || e.Action == "MORE" || e.Action == "LESS" || e.Action == "SATISFIED" || e.Action == "DISSATISFIED"
		if !validAction || e.Subject == uuid.Nil || e.Generation < 1 || e.PostID == uuid.Nil || e.ExposureID == uuid.Nil || e.Revision < 1 || math.IsNaN(e.NormalizedRead) || math.IsInf(e.NormalizedRead, 0) || e.NormalizedRead < 0 || e.NormalizedRead > 1 || (e.Action != "READ" && e.NormalizedRead != 0) || len(e.ModelVersion) < 1 || len(e.ModelVersion) > 120 || len(e.PolicyVersion) < 1 || len(e.PolicyVersion) > 120 || e.Enabled || e.Deleted || e.Eligible {
			return e, errors.New("invalid consented interaction")
		}
	default:
		return e, errors.New("unsupported stream event")
	}
	return e, nil
}

func (e Envelope) Key() string {
	if e.EventType == "CONTENT" {
		return "post:" + e.PostID.String()
	}
	return "viewer:" + e.Subject.String()
}

// Package features defines revision-bound observations for shadow parity checks.
// It never authorizes content or consent, and contains no raw content or identity.
package features

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math"
	"strconv"
	"time"

	"github.com/google/uuid"
)

const (
	Namespace     = "jansetu:rec:v1"
	Retention     = 30 * 24 * time.Hour
	MaxReferences = 200
)

var Actions = [...]string{"READ", "SKIP", "MORE", "LESS", "SATISFIED", "DISSATISFIED"}

type Reference struct {
	PostID   uuid.UUID
	Revision int32
}

func Field(action string, ref Reference) string {
	return action + ":" + ref.PostID.String() + ":" + strconv.FormatInt(int64(ref.Revision), 10)
}

type Observation struct {
	EventID        uuid.UUID `json:"eventId"`
	PostID         uuid.UUID `json:"postId"`
	Revision       int32     `json:"revision"`
	Action         string    `json:"action"`
	OccurredAt     time.Time `json:"occurredAt"`
	NormalizedRead float64   `json:"normalizedRead"`
	Order          string    `json:"order"`
}

func (o Observation) Field() string { return Field(o.Action, Reference{o.PostID, o.Revision}) }
func (o Observation) OrderKey() string {
	return o.OccurredAt.UTC().Format("20060102150405.000000000") + "|" + o.EventID.String()
}

func Decode(data []byte) (Observation, error) {
	var o Observation
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if len(data) > 512 || d.Decode(&o) != nil || d.Decode(new(any)) != io.EOF {
		return o, errors.New("invalid feature observation")
	}
	validAction := false
	for _, action := range Actions {
		validAction = validAction || action == o.Action
	}
	if !validAction || o.PostID == uuid.Nil || o.EventID == uuid.Nil || o.Revision < 1 || o.OccurredAt.IsZero() ||
		o.Order != o.OrderKey() || math.IsNaN(o.NormalizedRead) || math.IsInf(o.NormalizedRead, 0) ||
		o.NormalizedRead < 0 || o.NormalizedRead > 1 || (o.Action != "READ" && o.NormalizedRead != 0) {
		return o, errors.New("invalid feature observation")
	}
	return o, nil
}

type Report struct {
	Matched, Missing, Extra, Different int
}

func Compare(expected, actual []Observation) Report {
	wanted := map[string]Observation{}
	for _, o := range expected {
		wanted[o.Field()] = o
	}
	var report Report
	for _, o := range actual {
		w, ok := wanted[o.Field()]
		if !ok {
			report.Extra++
			continue
		}
		if o.EventID != w.EventID || !o.OccurredAt.Equal(w.OccurredAt) || math.Abs(o.NormalizedRead-w.NormalizedRead) > 1e-12 {
			report.Different++
		} else {
			report.Matched++
		}
		delete(wanted, o.Field())
	}
	report.Missing = len(wanted)
	return report
}

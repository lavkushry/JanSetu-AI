package features

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestParitySeparatesLostExtraAndChangedObservations(t *testing.T) {
	a := Observation{EventID: uuid.New(), PostID: uuid.New(), Revision: 1, Action: "READ", OccurredAt: time.Now(), NormalizedRead: 0.4}
	b := a
	b.PostID = uuid.New()
	c := a
	c.PostID = uuid.New()
	d := a
	d.PostID = uuid.New()
	changed := b
	changed.NormalizedRead = 0.1
	r := Compare([]Observation{a, b, c}, []Observation{a, changed, d})
	if r != (Report{Matched: 1, Missing: 1, Extra: 1, Different: 1}) {
		t.Fatal("misleading feature parity", r)
	}
}

func TestObservationRejectsPrivateAndUnsupportedFeatures(t *testing.T) {
	o := Observation{EventID: uuid.New(), PostID: uuid.New(), Revision: 1, Action: "READ", OccurredAt: time.Now(), NormalizedRead: 0.4}
	o.Order = o.OrderKey()
	data, _ := json.Marshal(o)
	if _, err := Decode(data); err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	json.Unmarshal(data, &raw)
	for key, value := range map[string]any{"ocr": "private", "revision": 0, "action": "WATCH", "normalizedRead": 2, "order": "incorrect"} {
		copy := map[string]any{}
		for k, v := range raw {
			copy[k] = v
		}
		copy[key] = value
		encoded, _ := json.Marshal(copy)
		if _, err := Decode(encoded); err == nil {
			t.Fatal("invalid feature accepted", key)
		}
	}
}

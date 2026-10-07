package stream

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestStreamRejectsPrivateUnknownAndInvalidContracts(t *testing.T) {
	base := Envelope{SchemaVersion: 1, EventID: uuid.New(), EventType: "INTERACTION", EntityVersion: 1, OccurredAt: time.Now(), Subject: uuid.New(), Generation: 2, ExposureID: uuid.New(), PostID: uuid.New(), Revision: 1, Action: "MORE", ModelVersion: "rules-v1", PolicyVersion: "baseline-v1"}
	data, _ := json.Marshal(base)
	if _, err := Decode(data); err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	json.Unmarshal(data, &fields)
	for key, value := range map[string]any{"ocr": "private", "schemaVersion": 2, "generation": 0, "action": "WATCH", "revision": 0, "normalizedRead": 1, "subject": uuid.Nil, "occurredAt": time.Now().Add(time.Hour)} {
		copy := map[string]any{}
		for k, v := range fields {
			copy[k] = v
		}
		copy[key] = value
		payload, _ := json.Marshal(copy)
		if _, err := Decode(payload); err == nil {
			t.Fatal("accepted invalid field", key)
		}
	}
	if _, err := Decode(append(data, []byte(" {}")...)); err == nil {
		t.Fatal("accepted trailing payload")
	}
}

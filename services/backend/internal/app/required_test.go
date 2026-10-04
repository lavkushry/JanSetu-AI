package app

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDesiredStateRequiresAnExplicitValue(t *testing.T) {
	for _, raw := range []string{`{}`, `null`, `{"enabled":null}`, `{"enabled":false,"unknown":true}`} {
		var body struct {
			Enabled bool `json:"enabled"`
		}
		if e := decodeRequired(httptest.NewRequest("PUT", "/", strings.NewReader(raw)), &body, "enabled"); e == nil {
			t.Fatalf("accepted incomplete desired state: %s", raw)
		}
	}
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if e := decodeRequired(httptest.NewRequest("PUT", "/", strings.NewReader(`{"enabled":false}`)), &body, "enabled"); e != nil {
		t.Fatal("explicit removal was rejected", e)
	}
}

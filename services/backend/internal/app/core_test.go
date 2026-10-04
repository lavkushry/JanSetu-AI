package app

import (
	"github.com/google/uuid"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestStrictJSON(t *testing.T) {
	for _, body := range []string{`{"body":"ok","extra":true}`, `{"body":"ok"}{"body":"other"}`, `null`, strings.Repeat("x", 70000)} {
		r := httptest.NewRequest("POST", "/", strings.NewReader(body))
		var value CommentInput
		e := decode(r, &value)
		if body == "null" {
			e = value.validate()
		}
		if e == nil {
			t.Fatalf("accepted invalid request %q", body[:min(len(body), 30)])
		}
	}
}
func TestCursorBinding(t *testing.T) {
	a := &App{cursorKey: []byte("a-secret-test-key-with-enough-entropy")}
	viewer := uuid.New()
	c := feedCursor{Viewer: viewer, Query: "HOME", Expires: time.Now().Add(time.Minute).Unix(), Refs: []feedRef{{"POST", uuid.New()}}}
	token := a.encodeCursor(c)
	if _, e := a.decodeCursor(token, viewer, "HOME"); e != nil {
		t.Fatal(e)
	}
	for _, test := range []struct {
		raw    string
		viewer uuid.UUID
		query  string
	}{{token + "x", viewer, "HOME"}, {token, uuid.New(), "HOME"}, {token, viewer, "FOLLOWING"}} {
		if _, e := a.decodeCursor(test.raw, test.viewer, test.query); e == nil {
			t.Fatal("accepted cursor outside its binding")
		}
	}
	c.Expires = time.Now().Add(-time.Minute).Unix()
	if _, e := a.decodeCursor(a.encodeCursor(c), viewer, "HOME"); e == nil {
		t.Fatal("accepted expired cursor")
	}
}

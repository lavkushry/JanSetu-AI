package app

import (
	"testing"

	"github.com/google/uuid"
)

// A history belongs to one worker and viewer. First pages start new snapshots;
// rejected continuations leave the accepted history intact for retry.
type benchmarkPostHistory struct {
	seen map[uuid.UUID]bool
}

func (h *benchmarkPostHistory) accept(continuation bool, posts []uuid.UUID) bool {
	if continuation {
		for _, id := range posts {
			if h.seen[id] {
				return false
			}
		}
	}
	if !continuation || h.seen == nil {
		h.seen = map[uuid.UUID]bool{}
	}
	for _, id := range posts {
		h.seen[id] = true
	}
	return true
}

func TestBenchmarkPostHistory(t *testing.T) {
	a, b, c := uuid.New(), uuid.New(), uuid.New()
	history := &benchmarkPostHistory{}
	if !history.accept(false, []uuid.UUID{a}) || !history.accept(true, []uuid.UUID{b}) {
		t.Fatal("distinct pages should pass")
	}
	if history.accept(true, []uuid.UUID{c, a}) {
		t.Fatal("post repeated from first page was accepted")
	}
	if !history.accept(true, []uuid.UUID{c}) {
		t.Fatal("rejected page polluted history before retry")
	}
	if history.accept(true, []uuid.UUID{b}) {
		t.Fatal("post repeated from an earlier continuation was accepted")
	}
	if !history.accept(false, []uuid.UUID{a}) || !history.accept(true, []uuid.UUID{b}) {
		t.Fatal("new snapshot should reset history")
	}
	other := &benchmarkPostHistory{}
	if !other.accept(false, []uuid.UUID{a, b}) {
		t.Fatal("separate viewers/workers must not share history")
	}
}

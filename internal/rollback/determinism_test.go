// Package rollback_test: one journal plans one rollback.
package rollback_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/rollback"
)

// TestBuild_TheSameJournalPlansTheSameRollback proves the planner's order
// does not depend on Go's map order. On the Walk tier each device's Runner
// numbers its own entries, so one node's entries on two devices share a
// sequence; with nothing else deciding between them, which device was undone
// first changed from one call to the next (FAILURE_PATTERNS 962). Fifty calls
// make a coin toss between two orders all but certain to show.
func TestBuild_TheSameJournalPlansTheSameRollback(t *testing.T) {
	req := goldenRequest()
	plan := func() string {
		p, err := rollback.Build(req)
		var refused *rollback.RefusedError
		if err != nil && !errors.As(err, &refused) {
			t.Fatalf("Build: %v", err)
		}
		b, _ := json.Marshal(struct {
			Plan    rollback.Plan
			Refused *rollback.RefusedError
		}{p, refused})
		return string(b)
	}
	first := plan()
	for i := 0; i < 50; i++ {
		if got := plan(); got != first {
			t.Fatalf("call %d planned differently from the first:\nfirst %s\nnow   %s", i+2, first, got)
		}
	}
}

// Tests that a method name or node id a journal carried is printed escaped
// when it holds a character a reader could be misled by (printable.go).
package rollback_test

import (
	"strconv"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
)

// TestBuild_PrintsAStoredMethodNameAndNodeIdEscaped: a Walk row is the
// Runner's to write, so an FQCN or a node id holding a direction override
// reaches a refusal escaped, never raw.
func TestBuild_PrintsAStoredMethodNameAndNodeIdEscaped(t *testing.T) {
	registerSkew(t)
	override := string(rune(0x202e))
	odd := undo(entry("run-A", "tasks[0]", "h1", 1, "fi"+override+"le.rbmake", engine.OutcomeChanged), "file.rbremove", map[string]any{"path": "/x"})
	failed := entry("run-A", "tasks[1]"+override, "h1", 2, "fi"+override+"le.rbmake", engine.OutcomeFailed)
	failed.FailureStage = engine.FailureStageAction
	plan, err := planOf([]engine.JournalEntry{odd, failed}, nil)
	problems := refused(t, err)
	var said []string
	for _, p := range problems {
		said = append(said, p.Reason)
	}
	for _, g := range plan.Unknown {
		said = append(said, g.Reason)
	}
	joined := strings.Join(said, "\n")
	if strings.Contains(joined, override) || !strings.Contains(joined, strconv.Quote("fi"+override+"le.rbmake")) {
		t.Errorf("the refusal and gaps hold the method name raw, or not quoted:\n%s", joined)
	}
}

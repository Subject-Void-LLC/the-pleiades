// Package jobs: tests of the Jobs view's mode column.
package jobs

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"
)

// TestModeBadge_MarksACheckApart covers the jobs list's mode column: a
// check never shares a real run's badge, an unreadable mode reads as
// failed, and every class returned is one the view allows.
func TestModeBadge_MarksACheckApart(t *testing.T) {
	for mode, want := range map[string]string{
		"check":      "badge-skipped",
		"execute":    "badge-neutral",
		"unreadable": "badge-failed",
		"":           "badge-neutral",
	} {
		got := modeBadge(mode)
		if got != want {
			t.Errorf("modeBadge(%q) = %q, want %q", mode, got, want)
		}
		if !view.ValidBadgeClasses[got] {
			t.Errorf("modeBadge(%q) = %q, which the view does not allow", mode, got)
		}
	}
	if modeBadge("check") == modeBadge("execute") {
		t.Error("a check and a real run share a badge")
	}
}

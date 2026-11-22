// Package dispatch: the Controller's refusal of a runbook whose condition
// reads its device's stored facts, made at admission, before anything is
// published (Phase 41's read half, owner decision 2026-11-21).
//
// A fact's value never rides a dispatch: a payload is frozen when it is
// published and kept on the stream for its retention, and the store's facts
// are sealed at rest. A Runner will fetch the keys a runbook names at claim,
// over the channel the credential fetch builds, and until then it cannot read
// a stored fact at all. Admission refuses such a job on each device rather
// than dispatching it to fail at the task, after earlier tasks had changed
// the device; the Runner refuses it again before its first task, for a
// dispatch an older Controller sent (internal/validate's FactsRule). A
// rollback job runs its plan's undo steps, which read no fact (a rollback
// step takes no condition, and no parameter reads facts), so it is never
// refused here, whatever the runbook it undoes reads.
//
// When a Runner can read facts, a run reading them needs the launching
// identity to hold inventory:read on each device it reads, checked where the
// fetch is served: a condition is otherwise a yes or no answer about a
// stored fact to an author holding only runbook:execute.
package dispatch

import (
	"errors"
	"fmt"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
)

// ErrFactReads marks a runbook refused on the Walk tier because a task's
// condition reads stored facts, which no Runner can read in this release.
var ErrFactReads = errors.New("a task's condition reads stored facts, which no Runner can read in this release")

// RefuseFactReads returns an error wrapping ErrFactReads that names every
// task of reads and the keys each reads, and says nothing ran and what works
// instead, or nil when reads is empty. It names runbook text only, never a
// value.
func RefuseFactReads(reads []engine.FactRead) error {
	if len(reads) == 0 {
		return nil
	}
	tasks := make([]string, 0, len(reads))
	for _, r := range reads {
		tasks = append(tasks, fmt.Sprintf("task %s reads stored facts (%s)", r.Label, engine.QuotedFactKeys(r.Keys)))
	}
	return fmt.Errorf("refused before contact: %s, %s: %w", strings.Join(tasks, "; "), engine.ErrNoFactsOnRunner, ErrFactReads)
}

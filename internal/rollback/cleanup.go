// Package rollback: a change a block's always: task made, which a rollback
// leaves in place unless the task's runbook says how to undo it.
package rollback

import (
	"errors"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
)

// alwaysPart is how a node id names a task inside a block's always: part,
// at any depth ("tasks[1].always[0]", "tasks[0].block[2].always[1].block[0]").
// The builder synthesizes every node id (chain.go's block), so no author
// text can spell it.
var alwaysPart = "." + string(engine.PartAlways) + "["

// cleanupReason is why a plan leaves an always: task's change in place.
const cleanupReason = "it ran in a block's always:, which runs to clean up after the block, so undoing it would put back what the block meant to remove (a lab it tore down, a grant it revoked); it is left in place. Give the task a rollback: list in its runbook to undo it"

// isCleanup reports whether e is a change a block's always: task made whose
// runbook gives no undo of its own: no rollback: list when it ran, and none
// written since. Such a change is left in place by default, where any other
// change with no undo refuses the rollback.
//
// An always: task is almost always a cleanup (delete a temporary lab, revoke
// a grant, take a device out of maintenance), and
// the recorded undo of a cleanup is the opposite of what the block meant:
// it recreates the lab, grants the access again, puts the device back into
// maintenance. Replaying it by default would be the unsafe choice, and
// refusing the whole rollback until each one is named with --leave made
// every rollback of such a run need a flag for a change nobody wants back.
// The author's own rollback: list is the one sure word that an always:
// task's change should be undone, so it is planned like any task's; and a
// runbook lookup that fails for any reason but the runbook being absent is
// left to the ordinary path, which reports it.
func isCleanup(req Request, e engine.JournalEntry) bool {
	if !strings.Contains(e.NodeID, alwaysPart) || e.AuthoredRollback {
		return false
	}
	if req.Authored == nil {
		return true
	}
	tasks, err := req.Authored(e.NodeID)
	switch {
	case err != nil:
		return errors.Is(err, ErrNoRunbook)
	case tasks != nil:
		// A rollback: list written since the run, empty or not.
		return false
	}
	return true
}

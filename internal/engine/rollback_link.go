// Package engine: what marks a run as a rollback in its own journal.
//
// A rollback is an ordinary run of ordinary tasks, through the same
// executor, locks and transports as any other. What makes it a rollback is
// only this: each entry it journals names the run it undoes and the node,
// and step, of that run each of its own nodes undoes
// (JournalEntry.RollbackOf, UndoesNode, UndoesStep). That is what lets a
// later rollback of the same run skip what is already undone, and refuse
// to undo a rollback.
package engine

// Undo names what one node of a rollback run undoes: a node of the run
// being undone, on the same device, and which step of that node's undo it
// is (0 for a recorded undo, the position in a rollback: list for an
// authored one).
type Undo struct {
	// Node is the node id in the run being undone.
	Node string

	// Step is the step of that node's undo.
	Step int
}

// rollbackLink is WithRollback's configuration: the run being undone, and
// what each of this run's nodes undoes, keyed by this run's node id.
type rollbackLink struct {
	of     string
	undoes map[string]Undo
}

// WithRollback marks the run as a rollback of the run named of (its run id
// on the Crawl tier, its job id on the Walk tier), with undoes saying which
// node and step each of this run's nodes undoes, keyed by this run's node
// id. An empty of leaves the run an ordinary one.
func WithRollback(of string, undoes map[string]Undo) ExecutorOption {
	return func(x *Executor) {
		if of == "" {
			return
		}
		copied := make(map[string]Undo, len(undoes))
		for node, undo := range undoes {
			copied[node] = undo
		}
		x.rollback = rollbackLink{of: of, undoes: copied}
	}
}

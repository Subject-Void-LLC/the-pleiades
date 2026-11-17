// Package journal: the combinations an entry's read-only answer
// (engine.JournalEntry.ReadOnlyCall) cannot come in, which the strict reader
// refuses and the Walk tier's consumer clears.
package journal

import (
	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
)

// ReadOnlyCallProblem says why e's claim that its call only read is one no
// build writes, or "" when it is not one or could be: the engine records the
// answer only for a call of a first-party built-in method, never an engine
// action nor an external Collection program's method, and a call that only
// read changed nothing, so it never comes with a change.
func ReadOnlyCallProblem(e engine.JournalEntry) string {
	switch {
	case !e.ReadOnlyCall:
		return ""
	case e.ProviderProgram != "":
		return "it names an external Collection program"
	case e.Outcome == engine.OutcomeChanged || e.ActionChanged:
		return "it changed the device"
	case engine.IsBuiltinAction(e.FQCN):
		return "its method is an engine action"
	case e.FQCNUnresolved || !FirstPartyMethod(e.FQCN):
		return "its method is no first-party Collection's"
	}
	return ""
}

// Package rollback: the refusal over a run since the one being undone,
// by kind, each with the advice that clears it.
package rollback

import (
	"fmt"
	"strings"
)

// The kinds of later run a rollback can be refused over (Problem.Kind),
// each with its own advice.
const (
	// KindUndoFirst is a later run with changes of its own to undo: roll it
	// back first.
	KindUndoFirst = "undo_first"
	// KindCannotUndo is a later run whose changes on the shared devices
	// include ones with no undo, and none with one: rolling it back first
	// would refuse too.
	KindCannotUndo = "cannot_undo"
	// KindNothingToUndo is a later run whose every change on the shared
	// devices its runbook says needs no undo (rollback: []).
	KindNothingToUndo = "nothing_to_undo"
	// KindUnknownEffect is a later run that recorded no change on the shared
	// devices, or whose changes there its own rollback undid, but a task of
	// which failed there during its action, so it may have changed them
	// partway: read it, and step over it once the devices are known to be as
	// the run being undone left them.
	KindUnknownEffect = "unknown_effect"
	// KindNoRunbook is a change whose undo needs the runbook the run ran,
	// which was not found as it ran: naming it with --runbook may answer
	// it, where --leave gives the change up.
	KindNoRunbook = "no_runbook"
	// KindLeftInPlace is a later run with nothing left there that rolling it
	// back would undo: what it still has on the shared devices is a cleanup
	// its block's always: ran, which its rollback leaves in place (and the
	// rest, if any, its rollback already undid). Rolling it back first would
	// say nothing is left, so read it and step over it.
	KindLeftInPlace = "left_in_place"
)

// laterFacts is what one later run did on the devices a rollback undoes on,
// as laterRunProblems read it from that run's journal.
type laterFacts struct {
	// id names the run.
	id string
	// changed and mayHave name the devices it changed, and those it may
	// have changed where a task failed during its action.
	changed, mayHave []string
	// undoable says it has a change there that rolling it back would undo;
	// undone, that its rollback already undid one.
	undoable, undone bool
	// irreversible names the methods whose changes there recorded no undo,
	// cleanups those of the always: cleanups its rollback leaves in place,
	// and unknown those whose tasks failed during their action there.
	irreversible, cleanups, unknown []string
}

// laterRun is the refusal over the later run f describes: which kind it is,
// and what to do about it. Rolling the run back never clears a failed task,
// so a run with one is always stepped over with --despite-run once its
// devices are known to be as this run left them, whatever else it did.
func laterRun(f laterFacts) Problem {
	id, changed, mayHave, undoable, irreversible, unknown := f.id, f.changed, f.mayHave, f.undoable, f.irreversible, f.unknown
	var did []string
	if len(changed) > 0 {
		did = append(did, "changed "+strings.Join(changed, ", "))
	}
	if len(mayHave) > 0 {
		did = append(did, "may have changed "+strings.Join(mayHave, ", "))
	}
	what := fmt.Sprintf("run %s %s after this run began", id, strings.Join(did, " and "))
	head := what + ", so undoing this run beneath it could undo on top of its changes"
	accept := "--despite-run " + id
	failed, stepOver := "", ""
	if len(unknown) > 0 {
		failed = "; " + strings.Join(unknown, ", ") + " failed during its action there, so it may have changed part of what it meant to, and the journal cannot say what"
		stepOver = "read it (pleiades journal show " + id + ") and step over it with " + accept + " once those devices are known to be as this run left them"
	}
	p := Problem{Accept: accept, UnknownEffects: unknown}
	switch {
	case undoable && len(unknown) > 0:
		p.Kind = KindUndoFirst
		p.Reason = head + failed + "; it has changes of its own to undo, so roll it back first (pleiades rollback " + id + ", accepting the failed task with --allow-unknown), but that leaves what failed, so then " + stepOver
	case undoable:
		p.Kind = KindUndoFirst
		p.Reason = head + "; it has changes of its own to undo, so roll it back first (pleiades rollback " + id + "), or step over it with " + accept
	case len(irreversible) > 0 && len(unknown) > 0:
		p.Kind = KindCannotUndo
		p.Reason = head + failed + "; its changes there cannot be undone (" + strings.Join(irreversible, ", ") + " recorded no undo), so " + stepOver
	case len(irreversible) > 0:
		p.Kind = KindCannotUndo
		p.Reason = head + "; its changes there cannot be undone (" + strings.Join(irreversible, ", ") + " recorded no undo), so read it (pleiades journal show " + id + ") and step over it with " + accept
	case len(f.cleanups) > 0:
		p.Kind = KindLeftInPlace
		left := "; what it still has there is a cleanup its block's always: ran (" + strings.Join(f.cleanups, ", ") + "), which a rollback of it leaves in place"
		if f.undone {
			left += ", and its rollback already undid the rest"
		}
		left += ", so rolling it back has nothing left to undo there"
		if len(unknown) > 0 {
			p.Reason = head + left + failed + "; " + stepOver
		} else {
			p.Reason = head + left + "; read it (pleiades journal show " + id + ") and step over it with " + accept + " once those devices are known to be as this run left them"
		}
	case len(unknown) > 0:
		p.Kind = KindUnknownEffect
		p.Reason = what + failed + ", so " + stepOver
	default:
		p.Kind = KindNothingToUndo
		p.Reason = head + "; its runbook says none of its changes there needs undoing (rollback: []), so stepping over it with " + accept + " is safe when that still holds"
	}
	return p
}

// cancelsOut reports whether r and its rollback cancel out: r fully undone
// by its own rollback, or r that rollback of a fully undone run. byID holds
// every other run by id.
func cancelsOut(r Run, others []Run, byID map[string]Run) bool {
	if fullyUndone(r, others) {
		return true
	}
	if of := rollbackOf(r); of != "" {
		if undone, known := byID[of]; known && fullyUndone(undone, others) {
			return true
		}
	}
	return false
}

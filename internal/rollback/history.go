// Package rollback: what other runs mean for a rollback. An earlier
// rollback of the same run may already have undone part of it, and a run
// since may have changed the same devices.
package rollback

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
)

// stepKey names one step of one undo: the change it undoes, and its place
// in that undo.
type stepKey struct {
	pair  pair
	index int
}

// succeeded reports whether a rollback entry's step did its work: it
// changed the device, or found it already as the undo wants it.
func succeeded(e engine.JournalEntry) bool {
	return e.Outcome == engine.OutcomeChanged || e.Outcome == engine.OutcomeRan
}

// undoneSteps returns every step already done of target's undo, so a
// rollback that stopped partway resumes where it stopped rather than
// repeating what worked: by an earlier rollback of it, or by target itself,
// when a block's rollback_on_failure undid what the block completed. The
// value says which (byRun for the run itself).
func undoneSteps(target Run, others []Run) map[stepKey]string {
	done := map[stepKey]string{}
	for _, r := range append([]Run{target}, others...) {
		if r.ID != target.ID && rollbackOf(r) != target.ID {
			continue
		}
		for _, e := range undoEntries(r, target.ID) {
			if succeeded(e) {
				by := r.ID
				if r.ID == target.ID {
					by = byRun
				}
				done[stepKey{pair{e.UndoesNode, e.DeviceID}, e.UndoesStep}] = by
			}
		}
	}
	return done
}

// byRun marks a step the run being undone did itself (undoneSteps).
const byRun = "the run itself"

// undoEntries returns r's entries undoing a node of the run target: all of
// a rollback run's, or the steps a run of its own rollback_on_failure ran.
func undoEntries(r Run, target string) []engine.JournalEntry {
	var out []engine.JournalEntry
	for _, e := range r.Entries {
		if e.UndoesNode != "" && e.RollbackOf == target {
			out = append(out, e)
		}
	}
	return out
}

// fullyUndone reports whether a rollback of run undid every change it made
// that needed undoing (a change its runbook says needs none aside), and at
// least one did: each changed device's undo has a step on record, and
// every step's latest attempt succeeded. A step that failed and was then
// run again successfully, as a rollback that stopped partway and was run
// once more does, counts as done; one whose latest attempt failed does
// not.
func fullyUndone(run Run, others []Run) bool {
	changed, _ := changesOf(run)
	if len(changed) == 0 {
		return false
	}
	type attempt struct {
		at time.Time
		ok bool
	}
	latest := map[stepKey]attempt{}
	for _, r := range append([]Run{run}, others...) {
		if r.ID != run.ID && rollbackOf(r) != run.ID {
			continue
		}
		for _, e := range undoEntries(r, run.ID) {
			if !succeeded(e) && e.Outcome != engine.OutcomeFailed {
				continue
			}
			k := stepKey{pair{e.UndoesNode, e.DeviceID}, e.UndoesStep}
			if prev, seen := latest[k]; !seen || e.FinishedAt.After(prev.at) {
				latest[k] = attempt{at: e.FinishedAt, ok: succeeded(e)}
			}
		}
	}
	undone := false
	for _, e := range changed {
		if e.AuthoredRollbackEmpty {
			// Nothing to undo, so no step was ever run for it.
			continue
		}
		undone = true
		p := pair{e.NodeID, e.DeviceID}
		recorded := false
		for k, a := range latest {
			if k.pair != p {
				continue
			}
			recorded = true
			if !a.ok {
				return false
			}
		}
		if !recorded {
			return false
		}
	}
	// A run none of whose changes needed undoing was never rolled back at
	// all: laterRunProblems names it as nothing to undo instead.
	return undone
}

// laterRunProblems refuses undoing target beneath another run that has
// changed the same devices since target began: undoing target's changes
// then undoes them on top of the other run's. Runs that cancel out (a run
// fully undone by its own rollback, and that rollback) do not count, nor
// does a rollback of target itself.
func laterRunProblems(req Request, changed []engine.JournalEntry) []Problem {
	begun := firstStart(req.Target)
	devices := map[string]bool{}
	for _, e := range changed {
		devices[e.DeviceID] = true
	}
	byID := map[string]Run{}
	for _, r := range req.Others {
		byID[r.ID] = r
	}

	var problems []Problem
	for _, r := range req.Others {
		if r.ID == req.Target.ID || rollbackOf(r) == req.Target.ID || req.DespiteRuns[r.ID] {
			continue
		}
		if fullyUndone(r, req.Others) {
			continue
		}
		if of := rollbackOf(r); of != "" {
			if undone, known := byID[of]; known && fullyUndone(undone, req.Others) {
				continue
			}
		}
		if !lastFinish(r).After(begun) {
			continue
		}
		theirs, _ := changesOf(r)
		var shared, irreversible []string
		undoable := false
		for _, e := range theirs {
			if !devices[e.DeviceID] {
				continue
			}
			name := deviceName(req, e.DeviceID)
			if name == "" {
				name = e.DeviceID
			}
			if !slices.Contains(shared, name) {
				shared = append(shared, name)
			}
			switch {
			case e.AuthoredRollbackEmpty:
			case e.AuthoredRollback || e.InverseFQCN != "":
				undoable = true
			case !slices.Contains(irreversible, e.FQCN):
				irreversible = append(irreversible, e.FQCN)
			}
		}
		if len(shared) == 0 {
			continue
		}
		slices.Sort(shared)
		slices.Sort(irreversible)
		problems = append(problems, laterRun(r.ID, strings.Join(shared, ", "), undoable, irreversible))
	}
	return problems
}

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
	// KindNoRunbook is a change whose undo needs the runbook the run ran,
	// which was not found as it ran: naming it with --runbook may answer
	// it, where --leave gives the change up.
	KindNoRunbook = "no_runbook"
)

// laterRun is the refusal over run id, which changed devices (named) after
// the run being undone began: which kind it is, and what to do about it.
func laterRun(id, devices string, undoable bool, irreversible []string) Problem {
	head := fmt.Sprintf("run %s changed %s after this run began, so undoing this run beneath it could undo on top of its changes", id, devices)
	p := Problem{Accept: "--despite-run " + id}
	switch {
	case undoable:
		p.Kind = KindUndoFirst
		p.Reason = head + "; it has changes of its own to undo, so roll it back first (pleiades rollback " + id + "), or step over it"
	case len(irreversible) > 0:
		p.Kind = KindCannotUndo
		p.Reason = head + "; its changes there cannot be undone (" + strings.Join(irreversible, ", ") + " recorded no undo), so read it (pleiades journal show " + id + ") and step over it"
	default:
		p.Kind = KindNothingToUndo
		p.Reason = head + "; its runbook says none of its changes there needs undoing (rollback: []), so stepping over it is safe when that still holds"
	}
	return p
}

// firstStart is when a run's earliest recorded task began.
func firstStart(run Run) time.Time {
	var first time.Time
	for _, e := range run.Entries {
		if !e.StartedAt.IsZero() && (first.IsZero() || e.StartedAt.Before(first)) {
			first = e.StartedAt
		}
	}
	return first
}

// lastFinish is when a run's latest recorded task finished.
func lastFinish(run Run) time.Time {
	var last time.Time
	for _, e := range run.Entries {
		if e.FinishedAt.After(last) {
			last = e.FinishedAt
		}
	}
	return last
}

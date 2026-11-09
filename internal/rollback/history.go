// Package rollback: what other runs mean for a rollback. An earlier
// rollback of the same run may already have undone part of it, and a run
// since may have changed the same devices.
package rollback

import (
	"slices"

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
		entry engine.JournalEntry
		ok    bool
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
			if prev, seen := latest[k]; !seen || entryAfter(e, prev.entry) {
				latest[k] = attempt{entry: e, ok: succeeded(e)}
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
// fully undone by its own rollback, and that rollback) do not count, unless
// a task of the run failed during its action on one of those devices,
// which no rollback undoes; nor does a rollback of target itself.
//
// Which runs are later is decided by ticks and lease revisions (order.go),
// and by clock times only for a pair of runs one of which predates them: the
// ids of those runs are returned too, for the plan to say so.
func laterRunProblems(req Request, changed []engine.JournalEntry) ([]Problem, []string) {
	devices := map[string]bool{}
	for _, e := range changed {
		devices[e.DeviceID] = true
	}
	byID := map[string]Run{}
	for _, r := range req.Others {
		byID[r.ID] = r
	}

	var problems []Problem
	var byClockRuns []string
	for _, r := range req.Others {
		if r.ID == req.Target.ID || rollbackOf(r) == req.Target.ID || req.DespiteRuns[r.ID] {
			continue
		}
		// A task that failed during its action may have changed its device
		// partway (plan.go's Unknown), so the run counts as a later change
		// there exactly as a recorded change does: the journal cannot say
		// the device is as the run being undone left it.
		theirs, failedActing := changesOf(r)
		onDevices := func(e engine.JournalEntry) bool { return devices[e.DeviceID] }
		if cancelsOut(r, req.Others, byID) {
			if !slices.ContainsFunc(failedActing, onDevices) {
				continue
			}
			// Its rollback undid every change it recorded, but no rollback
			// undoes a task that failed during its action (one accepted with
			// --allow-unknown is left as it is), so what that task did is
			// still on the device: only those tasks count.
			theirs = nil
		}
		later, basis := laterOn(req.Target, r, devices)
		// A run ordered by clock is named only when the order decided
		// something: it changed, or may have changed, one of the devices
		// this rollback undoes on.
		if basis == byClock && (slices.ContainsFunc(theirs, onDevices) || slices.ContainsFunc(failedActing, onDevices)) {
			byClockRuns = append(byClockRuns, r.ID)
		}
		var changedOn, mayHaveOn, irreversible, unknown []string
		undoable := false
		// share notes e's device in list when the other run came later on
		// one this rollback undoes on, and reports whether it did.
		share := func(e engine.JournalEntry, list *[]string) bool {
			if !devices[e.DeviceID] || !later[e.DeviceID] {
				return false
			}
			name := deviceName(req, e.DeviceID)
			if name == "" {
				name = e.DeviceID
			}
			if !slices.Contains(*list, name) {
				*list = append(*list, name)
			}
			return true
		}
		for _, e := range theirs {
			if !share(e, &changedOn) {
				continue
			}
			switch {
			case e.AuthoredRollbackEmpty:
			case e.AuthoredRollback || e.InverseFQCN != "":
				undoable = true
			case !slices.Contains(irreversible, Printable(e.FQCN)):
				irreversible = append(irreversible, Printable(e.FQCN))
			}
		}
		for _, e := range failedActing {
			if share(e, &mayHaveOn) && !slices.Contains(unknown, Printable(e.FQCN)) {
				unknown = append(unknown, Printable(e.FQCN))
			}
		}
		if len(changedOn) == 0 && len(mayHaveOn) == 0 {
			continue
		}
		// A device the run both changed and failed on is named once, as
		// changed: the failure is said beside it.
		mayHaveOn = slices.DeleteFunc(mayHaveOn, func(d string) bool { return slices.Contains(changedOn, d) })
		slices.Sort(changedOn)
		slices.Sort(mayHaveOn)
		slices.Sort(irreversible)
		slices.Sort(unknown)
		p := laterRun(r.ID, changedOn, mayHaveOn, undoable, irreversible, unknown)
		switch basis {
		case byUnfinished:
			p.Reason += "; it never recorded its end on the project's run counter, and no run began after it stopped to record that it was gone, so it may have changed them at any time after this run began"
		case byClock:
			p.Reason += "; one of the two carries no run order (its journal predates run ordering, or it ran under a shared device lease, whose holders overlap), so this was decided by the journals' clock times"
		}
		problems = append(problems, p)
	}
	return problems, byClockRuns
}

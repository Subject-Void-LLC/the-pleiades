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

// fullyUndone reports whether every change run made has been undone by a
// rollback of it: each changed device's undo has a step on record, and
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
	for _, e := range changed {
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
	return true
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
		var shared []string
		for _, e := range theirs {
			if devices[e.DeviceID] {
				name := deviceName(req, e.DeviceID)
				if name == "" {
					name = e.DeviceID
				}
				if !slices.Contains(shared, name) {
					shared = append(shared, name)
				}
			}
		}
		if len(shared) == 0 {
			continue
		}
		slices.Sort(shared)
		problems = append(problems, Problem{
			Reason: fmt.Sprintf("run %s changed %s after this run began; undoing this run beneath it could undo on top of its changes, so roll it back first",
				r.ID, strings.Join(shared, ", ")),
			Accept: "--despite-run " + r.ID,
		})
	}
	return problems
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

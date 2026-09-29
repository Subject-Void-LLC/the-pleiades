// Package rollback: which journal entries are changes, and the steps that
// undo each one.
package rollback

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/fragment"
	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/termsafe"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// maxRecordedText is the journal's own bound on a recorded text value
// (engine.recordedValue); a longer one did not come from the projection.
const maxRecordedText = 256

// changesOf returns the entries of run that changed a device, newest
// first, and those whose effect is unknown: a failed action that was not
// read-only.
//
// On the Walk tier a device's dispatch can run several times (attempts),
// and the same node can report a change in more than one; only the most
// recent attempt that changed it is kept, since the undo it recorded
// reverses the device from the state it is in now.
func changesOf(run Run) (changed, unknown []engine.JournalEntry) {
	latest := map[pair]engine.JournalEntry{}
	for _, e := range run.Entries {
		switch {
		case e.Outcome == engine.OutcomeNotReached:
			continue
		case e.ActionChanged || e.Outcome == engine.OutcomeChanged:
			p := pair{e.NodeID, e.DeviceID}
			if prev, seen := latest[p]; !seen || e.Attempt > prev.Attempt {
				latest[p] = e
			}
		case e.Outcome == engine.OutcomeFailed && e.FailureStage == engine.FailureStageAction && !changesNothing(e.FQCN):
			unknown = append(unknown, e)
		}
	}
	for _, e := range latest {
		changed = append(changed, e)
	}
	sort.Slice(changed, func(i, j int) bool {
		if changed[i].Attempt != changed[j].Attempt {
			return changed[i].Attempt > changed[j].Attempt
		}
		return changed[i].Sequence > changed[j].Sequence
	})
	sort.Slice(unknown, func(i, j int) bool { return unknown[i].Sequence < unknown[j].Sequence })
	return changed, unknown
}

// changesNothing reports whether a task calling fqcn cannot have changed a
// device: a built-in method that declares itself read-only (which the
// engine holds it to), or an engine action that touches no device.
func changesNothing(fqcn string) bool {
	switch fqcn {
	case "noop", "set_metadata", "pleiades.builtin.set_metadata", "ios_backup":
		return true
	}
	desc, ok := collection.Lookup(fqcn)
	return ok && desc.Provider == nil && desc.Manifest.Reversibility.ReadOnly
}

// stepError is why one change cannot be undone, with the flag that
// accepts it.
type stepError struct {
	reason string
	accept string
}

// Error returns the reason.
func (e *stepError) Error() string { return e.reason }

// stepsFor returns the steps that undo one change: the runbook's authored
// rollback: steps when the entry says the task had them, otherwise the
// undo the method recorded, checked against what the method declares.
func stepsFor(req Request, e engine.JournalEntry) ([]Step, error) {
	leave := "--leave " + e.NodeID
	if e.AuthoredRollback {
		return authoredSteps(req, e, leave)
	}
	// A rollback: list written after the run counts too. The runbook's
	// version does not hash rollback:, so a runbook found at the journal's
	// version is still the one that ran, and its author's undo wins over a
	// recorded one exactly as it would have had it been there all along.
	var notFound error
	if req.Authored != nil {
		tasks, err := req.Authored(e.NodeID)
		switch {
		case err == nil && len(tasks) > 0:
			return authoredList(e, tasks), nil
		case errors.Is(err, ErrNoRunbook):
			notFound = err
		case err != nil:
			return nil, &stepError{reason: err.Error(), accept: leave}
		}
	}
	if e.InverseFQCN == "" {
		reason := fmt.Sprintf("%s changed the device and recorded no undo", e.FQCN)
		if desc, ok := collection.Lookup(e.FQCN); ok && !desc.Manifest.Reversibility.Reversible && desc.Manifest.Reversibility.Notes != "" {
			reason = fmt.Sprintf("%s changed the device and cannot be undone (%s)", e.FQCN, strings.TrimSuffix(desc.Manifest.Reversibility.Notes, "."))
		}
		reason += "; give the task a rollback: list in its runbook, or leave the change in place"
		if notFound != nil {
			reason += " (" + notFound.Error() + ")"
		}
		return nil, &stepError{reason: reason, accept: leave}
	}
	if err := checkRecorded(e); err != nil {
		return nil, &stepError{reason: err.Error(), accept: leave}
	}
	if !e.InverseComplete && !e.InversePartial {
		withheld := missingKeys(e)
		return nil, &stepError{
			reason: fmt.Sprintf("%s's undo through %s was not recorded in full (%s kept out of the journal), so it cannot be replayed; give the task a rollback: list, or leave the change in place",
				e.FQCN, e.InverseFQCN, strings.Join(withheld, ", ")),
			accept: leave,
		}
	}
	if e.InversePartial && !req.AllowPartial[e.NodeID] {
		return nil, &stepError{
			reason: fmt.Sprintf("%s's undo through %s is partial: it does not put back everything the task overwrote", e.FQCN, e.InverseFQCN),
			accept: "--allow-partial " + e.NodeID,
		}
	}
	if e.InversePartial && len(missingKeys(e)) > 0 {
		return nil, &stepError{reason: fmt.Sprintf("%s's partial undo through %s was not recorded in full, so it cannot be replayed", e.FQCN, e.InverseFQCN), accept: leave}
	}
	params := map[string]any{}
	for _, p := range e.InverseParams {
		v, _ := p.Value()
		params[p.Key] = v
	}
	return []Step{{
		Node: e.NodeID, Index: 0, Emitter: e.FQCN, FQCN: e.InverseFQCN, Params: params, Source: SourceRecorded,
		Name: fmt.Sprintf("undo %s (%s)", e.NodeID, labelOf(e)), Partial: e.InversePartial,
	}}, nil
}

// authoredSteps returns an entry's authored rollback: steps from the
// runbook.
func authoredSteps(req Request, e engine.JournalEntry, leave string) ([]Step, error) {
	if req.Authored == nil {
		return nil, &stepError{reason: "the task's undo is its rollback: list, and the runbook it ran from was not found; name it with --runbook", accept: leave}
	}
	tasks, err := req.Authored(e.NodeID)
	if err != nil {
		return nil, &stepError{reason: err.Error(), accept: leave}
	}
	if len(tasks) == 0 {
		return nil, &stepError{reason: "the journal says the task had a rollback: list, and the runbook no longer gives it one", accept: leave}
	}
	return authoredList(e, tasks), nil
}

// authoredList turns a node's rollback: list into its steps.
func authoredList(e engine.JournalEntry, tasks []engine.Task) []Step {
	steps := make([]Step, 0, len(tasks))
	for i, task := range tasks {
		name := task.Name
		if name == "" {
			name = task.FQCN
		}
		steps = append(steps, Step{
			Node: e.NodeID, Index: i, Emitter: e.FQCN, FQCN: task.FQCN, Params: maps.Clone(task.Params), Source: SourceAuthored,
			Name: fmt.Sprintf("undo %s (%s), step %d: %s", e.NodeID, labelOf(e), i+1, name),
		})
	}
	return steps
}

// labelOf names an entry's task for a rollback task's name: its own name,
// else its method.
func labelOf(e engine.JournalEntry) string {
	if e.TaskName != "" {
		return e.TaskName
	}
	return e.FQCN
}

// missingKeys returns the undo's parameter names whose values the journal
// does not hold.
func missingKeys(e engine.JournalEntry) []string {
	have := map[string]bool{}
	for _, p := range e.InverseParams {
		have[p.Key] = true
	}
	var missing []string
	for _, k := range e.InverseParamKeys {
		if !have[k] {
			missing = append(missing, k)
		}
	}
	if e.UndeclaredInverseParamCount > 0 {
		missing = append(missing, fmt.Sprintf("%d undeclared", e.UndeclaredInverseParamCount))
	}
	return missing
}

// checkRecorded holds a journaled undo to what its method declares,
// rather than trusting the journal: a file an operator's account can
// write, or a row a Runner published. The method must be a built-in one
// this build knows, declaring an undo through the recorded method; every
// recorded value must sit under a key it declares recordable and the
// target reads, and be a value the journal's own projection could have
// written.
func checkRecorded(e engine.JournalEntry) error {
	spec, target, err := declaredUndo(e.FQCN, e.InverseFQCN)
	if err != nil {
		return err
	}
	if e.InversePartial && !spec.MayBePartial {
		return fmt.Errorf("the journal marks %s's undo through %s partial, which %s never declares it can be, so it is not trusted", e.FQCN, e.InverseFQCN, e.FQCN)
	}
	declared := fragment.Declared(target)
	seen := map[string]bool{}
	for _, p := range e.InverseParams {
		switch {
		case seen[p.Key]:
			return fmt.Errorf("the journal records undo parameter %q twice", p.Key)
		case collection.IsReservedParam(p.Key) || !slices.Contains(spec.Record, p.Key) || !declared[p.Key]:
			return fmt.Errorf("the journal records a value for undo parameter %q, which %s does not declare recordable for %s, so it is not trusted", p.Key, e.FQCN, e.InverseFQCN)
		}
		seen[p.Key] = true
		if err := checkValue(p); err != nil {
			return fmt.Errorf("undo parameter %q: %w", p.Key, err)
		}
	}
	return nil
}

// checkValue holds one recorded value to the projection's own rules.
func checkValue(p engine.InverseParam) error {
	set := 0
	for _, isSet := range []bool{p.Text != nil, p.Number != nil, p.Bool != nil} {
		if isSet {
			set++
		}
	}
	if set != 1 {
		return fmt.Errorf("holds %d values, want one", set)
	}
	if p.Text != nil && (len(*p.Text) > maxRecordedText || !utf8.ValidString(*p.Text) || termsafe.CheckLine(*p.Text) != nil) {
		return fmt.Errorf("holds text the journal would never record")
	}
	if _, ok := p.Value(); !ok {
		return fmt.Errorf("holds a number that is not one")
	}
	return nil
}

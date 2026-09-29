// Package rollback plans the undoing of a run from its journal (Phase 40).
//
// A rollback is an ordinary run of ordinary tasks, through the same
// executor, locks and transports as any other. This package decides which
// tasks: for every device a run changed, the undo that run's task either
// recorded (sdk.RecordInverse, journaled as JournalEntry.InverseParams) or
// its runbook authored (Task.Rollback), in the reverse of the order the
// run made its changes. It is pure: it reads journal entries, the registry
// and what its caller tells it about the inventory and the runbook, and
// returns a plan or a refusal. The Crawl tier's `pleiades rollback` and
// the Walk tier's rollback job both call it, so the two tiers cannot
// disagree about what a rollback does.
//
// # Never a guess
//
// A rollback refuses as a whole, before it contacts any device, when any
// change it would need to undo cannot be undone exactly: no undo was
// recorded, the undo was not recorded in full, the record does not match
// what the method declares, the device has left the inventory, or a later
// run has changed the same device since. Each refusal names the node and
// the device, and the flag that accepts that gap by name (Request.Leave,
// AllowPartial, AllowUnknown, DespiteRuns), so an operator accepts exactly
// what they read and nothing else, as `--allow-unchecked` does for a check.
//
// A task that failed during its own action is different: it may have
// changed part of what it meant to, and the journal cannot say which,
// since an undo is recorded only when an action succeeds. The plan does not
// guess an undo for it; it reports the effect as unknown (Plan.Unknown),
// and a caller treats an unknown it was not told to accept as an
// incomplete rollback.
package rollback

import (
	"errors"
	"fmt"
	"sort"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
)

// Run is one run's journal, as the planner reads it.
type Run struct {
	// ID names the run: its run id on the Crawl tier, its job id on the
	// Walk tier.
	ID string

	// Entries are every journal entry the run wrote, in any order.
	Entries []engine.JournalEntry

	// Sealed says the journal is known complete: the run returned (the
	// Crawl tier's <run-id>.end), or every device's result is in (the Walk
	// tier). An unsealed journal may be missing its last level.
	Sealed bool
}

// Request is what a rollback was asked to do.
type Request struct {
	// Target is the run to undo.
	Target Run

	// Others are the other runs the caller could read, so the planner can
	// resume what an earlier rollback of Target already undid, and refuse
	// when a later run has changed the same device since.
	Others []Run

	// Devices resolves a journal's device id to the device's name in the
	// inventory now, reporting false when no device holds that id.
	Devices func(deviceID string) (name string, ok bool)

	// Authored returns node's rollback: steps from the runbook Target ran.
	// It is asked for every change, since a rollback: list may have been
	// added after the run; one it returns wins over a recorded undo. The
	// caller is responsible for the runbook being that one (its version
	// matches the journal's). An error wrapping ErrNoRunbook means no such
	// runbook was found, which blocks only a node whose entry says it had a
	// rollback: list; any other error is the node's problem.
	Authored func(node string) ([]engine.Task, error)

	// Leave names node ids whose changes the operator accepts leaving in
	// place, not undone: any change, whether or not it could be undone.
	Leave map[string]bool

	// AllowPartial names node ids whose partial undo the operator accepts
	// running (sdk.Inverse.Partial).
	AllowPartial map[string]bool

	// AllowUnknown names node ids whose unknown effect the operator
	// accepts, and "unsealed" for a journal that may be missing its end.
	AllowUnknown map[string]bool

	// DespiteRuns names later runs, by id, whose changes to the same
	// devices the operator accepts undoing Target beneath.
	DespiteRuns map[string]bool
}

// Unsealed is the name AllowUnknown takes for a journal with no seal.
const Unsealed = "unsealed"

// ErrNoRunbook is what Request.Authored wraps when it cannot find the
// runbook the run ran.
var ErrNoRunbook = errors.New("the runbook the run ran was not found")

// Step is one task of a rollback run.
type Step struct {
	// Node is the node, in the run being undone, this step undoes.
	Node string `json:"node"`

	// DeviceID and DeviceName name the device the step runs on: the one
	// the node changed.
	DeviceID   string `json:"device_id"`
	DeviceName string `json:"device"`

	// Index is the step's place in the node's undo: 0 for a recorded undo,
	// its position in the rollback: list for an authored one.
	Index int `json:"index"`

	// Emitter is the method the node ran: what recorded the undo, or
	// whose runbook task carries the rollback: list.
	Emitter string `json:"emitter"`

	// FQCN and Params are the task to run.
	FQCN   string         `json:"method"`
	Params map[string]any `json:"params"`

	// Name is the task's name in the rollback run.
	Name string `json:"name"`

	// Source says where the step came from: "recorded" or "authored".
	Source string `json:"source"`

	// Partial marks a recorded undo that does not put everything back,
	// run because the operator named it in AllowPartial.
	Partial bool `json:"partial,omitempty"`
}

// Gap is a change the plan does not undo, and why.
type Gap struct {
	Node       string `json:"node"`
	DeviceID   string `json:"device_id,omitempty"`
	DeviceName string `json:"device,omitempty"`
	Reason     string `json:"reason"`
}

// Plan is a rollback the planner will stand behind.
type Plan struct {
	// Of is the run being undone.
	Of string `json:"of"`

	// Levels are the rollback run's tasks in the order they run: one
	// level per forward node (and per step of an authored undo), newest
	// first, with that node's devices side by side.
	Levels [][]Step `json:"levels"`

	// Left are changes the operator named in Leave.
	Left []Gap `json:"left,omitempty"`

	// Unknown are effects nobody can say were changes or not: a failed
	// action, or a journal with no seal. Each is accepted when its node is
	// in AllowUnknown; one that is not makes the rollback incomplete.
	Unknown []Gap `json:"unknown,omitempty"`

	// UnacceptedUnknown counts the Unknown gaps AllowUnknown does not
	// name.
	UnacceptedUnknown int `json:"unaccepted_unknown"`

	// AlreadyUndone are changes an earlier rollback of this run undid.
	AlreadyUndone []Gap `json:"already_undone,omitempty"`
}

// Steps returns every step of the plan in the order it runs.
func (p Plan) Steps() []Step {
	var out []Step
	for _, level := range p.Levels {
		out = append(out, level...)
	}
	return out
}

// Problem is one reason the plan was refused.
type Problem struct {
	Node       string `json:"node,omitempty"`
	DeviceID   string `json:"device_id,omitempty"`
	DeviceName string `json:"device,omitempty"`
	Reason     string `json:"reason"`
	// Accept names the flag, and its argument, that accepts this problem,
	// empty when nothing can.
	Accept string `json:"accept,omitempty"`
}

// RefusedError is the planner's refusal: every problem found, so an
// operator fixes them together rather than one run at a time.
type RefusedError struct {
	Problems []Problem
}

// Error summarizes the refusal.
func (e *RefusedError) Error() string {
	return fmt.Sprintf("the rollback was refused, before any device was contacted, for %d reason(s)", len(e.Problems))
}

// pair names one device one node changed.
type pair struct{ node, device string }

// Build plans req's rollback, or refuses it.
func Build(req Request) (Plan, error) {
	plan := Plan{Of: req.Target.ID}
	var problems []Problem

	if of := rollbackOf(req.Target); of != "" {
		return plan, &RefusedError{Problems: []Problem{{
			Reason: fmt.Sprintf("this run is a rollback of %s; undoing it is not a rollback, so run that runbook again instead", of),
		}}}
	}

	changed, unknown := changesOf(req.Target)
	for _, u := range unknown {
		gap := Gap{Node: u.NodeID, DeviceID: u.DeviceID, DeviceName: deviceName(req, u.DeviceID),
			Reason: fmt.Sprintf("%s failed during its action, so it may have changed part of what it meant to; the journal cannot say what, and nothing undoes it", u.FQCN)}
		plan.Unknown = append(plan.Unknown, gap)
		if !req.AllowUnknown[u.NodeID] {
			plan.UnacceptedUnknown++
		}
	}
	if !req.Target.Sealed {
		plan.Unknown = append(plan.Unknown, Gap{Node: Unsealed,
			Reason: "the run's journal has no seal, so the process may have ended mid-level, and whatever that level did is not recorded"})
		if !req.AllowUnknown[Unsealed] {
			plan.UnacceptedUnknown++
		}
	}

	done := undoneSteps(req.Target.ID, req.Others)
	problems = append(problems, laterRunProblems(req, changed)...)

	type levelKey struct {
		order int
		index int
	}
	levels := map[levelKey][]Step{}
	for order, e := range changed {
		p := pair{e.NodeID, e.DeviceID}
		name, known := deviceLookup(req, e.DeviceID)
		steps, err := stepsFor(req, e)
		// A change named to leave stays as it is, whether or not it could
		// be undone: the operator decides what to take back out.
		if req.Leave[e.NodeID] {
			reason := "named to leave in place"
			if err != nil {
				reason = err.Error()
			}
			plan.Left = append(plan.Left, Gap{Node: e.NodeID, DeviceID: e.DeviceID, DeviceName: name, Reason: reason})
			continue
		}
		switch {
		case err != nil:
			accept := "--leave " + e.NodeID
			var se *stepError
			if errors.As(err, &se) && se.accept != "" {
				accept = se.accept
			}
			problems = append(problems, Problem{Node: e.NodeID, DeviceID: e.DeviceID, DeviceName: name,
				Reason: err.Error(), Accept: accept})
			continue
		}
		var remaining []Step
		for _, s := range steps {
			if !done[stepKey{p, s.Index}] {
				remaining = append(remaining, s)
			}
		}
		if len(remaining) == 0 {
			plan.AlreadyUndone = append(plan.AlreadyUndone, Gap{Node: e.NodeID, DeviceID: e.DeviceID, DeviceName: name,
				Reason: "an earlier rollback of this run undid it"})
			continue
		}
		if !known {
			problems = append(problems, Problem{Node: e.NodeID, DeviceID: e.DeviceID,
				Reason: "the device this changed is no longer in the inventory, so nothing can reach it to undo it", Accept: "--leave " + e.NodeID})
			continue
		}
		for _, s := range remaining {
			s.DeviceID, s.DeviceName = e.DeviceID, name
			k := levelKey{order, s.Index}
			levels[k] = append(levels[k], s)
		}
	}

	// Nothing left but what an earlier rollback undid and what the
	// operator named to leave: a refusal saying so, rather than a run of
	// nothing that reads as a rollback done.
	if len(plan.AlreadyUndone) > 0 && len(plan.AlreadyUndone)+len(plan.Left) == len(changed) && len(problems) == 0 {
		reason := "every change this run made has already been undone by an earlier rollback of it; there is nothing left to undo"
		if len(plan.Left) > 0 {
			reason = "every change this run made has already been undone by an earlier rollback of it, apart from those named to leave in place; there is nothing left to undo"
		}
		problems = append(problems, Problem{Reason: reason})
	}
	if len(problems) > 0 {
		return plan, &RefusedError{Problems: problems}
	}

	keys := make([]levelKey, 0, len(levels))
	for k := range levels {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].order != keys[j].order {
			return keys[i].order < keys[j].order
		}
		return keys[i].index < keys[j].index
	})
	for _, k := range keys {
		level := levels[k]
		sort.Slice(level, func(i, j int) bool { return level[i].DeviceName < level[j].DeviceName })
		plan.Levels = append(plan.Levels, level)
	}
	return plan, nil
}

// deviceLookup resolves a device id through req.Devices. The empty id is
// a controller-side task's, which reaches no device, and is always
// reachable: its undo runs controller-side too.
func deviceLookup(req Request, deviceID string) (string, bool) {
	if deviceID == "" {
		return "", true
	}
	if req.Devices == nil {
		return "", false
	}
	return req.Devices(deviceID)
}

// deviceName is deviceLookup's name alone, for a message.
func deviceName(req Request, deviceID string) string {
	name, _ := deviceLookup(req, deviceID)
	return name
}

// rollbackOf returns the run a run is a rollback of, or "".
func rollbackOf(run Run) string {
	for _, e := range run.Entries {
		if e.RollbackOf != "" {
			return e.RollbackOf
		}
	}
	return ""
}

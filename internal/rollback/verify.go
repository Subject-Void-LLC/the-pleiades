// Package rollback: holding a step to the runbook that ran.
package rollback

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"unicode/utf8"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/fragment"
	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/termsafe"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// The two places a step comes from (Step.Source).
const (
	SourceRecorded = "recorded"
	SourceAuthored = "authored"
)

// Verify holds one step to dag, the runbook the undone run ran, at the
// version its journal records: the node exists and runs the step's
// emitter; a recorded step is an undo that method declares, carrying only
// values it declares recordable, each one the journal could have held; an
// authored step is that node's rollback: list entry exactly as written.
//
// The journal a plan is read from was published by Runners, so on the
// Walk tier the Controller asks this before it creates a rollback job and
// the Runner asks again before it runs one: a row claiming a node ran a
// method it does not run cannot steer either.
func Verify(dag *engine.DAG, s Step) error {
	task, ok := dag.Nodes[s.Node]
	if !ok {
		return fmt.Errorf("the runbook has no node %s", s.Node)
	}
	if task.FQCN != s.Emitter {
		return fmt.Errorf("node %s runs %s, and the rollback says it ran %s", s.Node, task.FQCN, s.Emitter)
	}
	switch s.Source {
	case SourceRecorded:
		if s.Index != 0 {
			return fmt.Errorf("a recorded undo of node %s is one step, and this is step %d", s.Node, s.Index)
		}
		return verifyRecorded(s)
	case SourceAuthored:
		if s.Index < 0 || s.Index >= len(task.Rollback) {
			return fmt.Errorf("node %s has %d rollback: step(s), and this is step %d", s.Node, len(task.Rollback), s.Index)
		}
		want := task.Rollback[s.Index]
		if want.FQCN != s.FQCN || !sameParams(want.Params, s.Params) {
			return fmt.Errorf("step %d of node %s's rollback: list is not what the runbook says", s.Index, s.Node)
		}
		return nil
	default:
		return fmt.Errorf("a step of node %s comes from %q, which is neither recorded nor authored", s.Node, s.Source)
	}
}

// verifyRecorded holds a recorded step to what its emitter declares.
func verifyRecorded(s Step) error {
	spec, target, err := declaredUndo(s.Emitter, s.FQCN)
	if err != nil {
		return err
	}
	declared := fragment.Declared(target)
	for k, v := range s.Params {
		if collection.IsReservedParam(k) || !slices.Contains(spec.Record, k) || !declared[k] {
			return fmt.Errorf("the undo of %s through %s carries parameter %q, which %s does not declare recordable", s.Emitter, s.FQCN, k, s.Emitter)
		}
		if !recordableValue(v) {
			return fmt.Errorf("the undo of %s through %s carries a value for %q the journal would never hold", s.Emitter, s.FQCN, k)
		}
	}
	return nil
}

// declaredUndo returns emitter's declaration of an undo through target,
// and target's descriptor, refusing a method that is not a built-in this
// build runs, an undo it does not declare, or an undo method that is not
// implemented.
func declaredUndo(emitter, target string) (*sdk.InverseSpec, collection.Descriptor, error) {
	desc, ok := collection.Lookup(emitter)
	if !ok || desc.Manifest.Status != collection.StatusImplemented || desc.Provider != nil {
		return nil, collection.Descriptor{}, fmt.Errorf("the journal names %s, which is not a built-in method this build runs, so the undo it records is not trusted", emitter)
	}
	var spec *sdk.InverseSpec
	for i := range desc.Manifest.Reversibility.Inverses {
		if desc.Manifest.Reversibility.Inverses[i].FQCN == target {
			spec = &desc.Manifest.Reversibility.Inverses[i]
		}
	}
	if spec == nil {
		return nil, collection.Descriptor{}, fmt.Errorf("the journal records an undo of %s through %s, which %s does not declare, so it is not trusted", emitter, target, emitter)
	}
	undo, ok := collection.Lookup(target)
	if !ok || undo.Manifest.Status != collection.StatusImplemented {
		return nil, collection.Descriptor{}, fmt.Errorf("the undo method %s is not an implemented method in this build", target)
	}
	return spec, undo, nil
}

// recordableValue reports whether v is a value the journal's projection
// could have recorded, as it arrives after a trip through JSON or YAML.
func recordableValue(v any) bool {
	switch v := v.(type) {
	case string:
		return len(v) <= maxRecordedText && utf8.ValidString(v) && termsafe.CheckLine(v) == nil
	case bool, int, int64, float64, json.Number:
		return true
	default:
		return false
	}
}

// sameParams compares two parameter maps as JSON sees them, so an integer
// the runbook holds and the same number after a trip through a dispatch
// payload compare equal.
func sameParams(a, b map[string]any) bool {
	normal := func(m map[string]any) any {
		if len(m) == 0 {
			return nil
		}
		raw, err := json.Marshal(m)
		if err != nil {
			return err
		}
		var out any
		if err := json.Unmarshal(raw, &out); err != nil {
			return err
		}
		return out
	}
	na, nb := normal(a), normal(b)
	if _, bad := na.(error); bad {
		return false
	}
	return reflect.DeepEqual(na, nb)
}

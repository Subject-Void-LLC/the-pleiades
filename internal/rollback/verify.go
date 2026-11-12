// Package rollback: holding a step to the runbook that ran.
package rollback

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/fragment"
	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
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
// values it declares recordable, each one the journal could have held and
// of the type its method declares, recorded under this build's
// declarations unless the plan accepted the skew by name (SkewAccepted);
// an authored step is that node's rollback: list entry exactly as written,
// except for the values the run rendered for its templates (verifyRendered).
//
// The journal a plan is read from was published by Runners, so on the
// Walk tier the Controller asks this before it creates a rollback job and
// the Runner asks again before it runs one: a row claiming a node ran a
// method it does not run cannot steer either.
func Verify(dag *engine.DAG, s Step) error {
	task, ok := dag.NodeFor(s.Node)
	if !ok {
		return fmt.Errorf("the runbook has no node %s", Printable(s.Node))
	}
	if task.FQCN != s.Emitter {
		return fmt.Errorf("node %s runs %s, and the rollback says it ran %s", Printable(s.Node), Printable(task.FQCN), Printable(s.Emitter))
	}
	switch s.Source {
	case SourceRecorded:
		if s.Index != 0 {
			return fmt.Errorf("a recorded undo of node %s is one step, and this is step %d", Printable(s.Node), s.Index)
		}
		return verifyRecorded(s)
	case SourceAuthored:
		if s.Index < 0 || s.Index >= len(task.Rollback) {
			return fmt.Errorf("node %s has %d rollback: step(s), and this is step %d", Printable(s.Node), len(task.Rollback), s.Index)
		}
		want := task.Rollback[s.Index]
		if want.FQCN != s.FQCN {
			return fmt.Errorf("step %d of node %s's rollback: list is not what the runbook says", s.Index, Printable(s.Node))
		}
		if len(s.Rendered) > 0 {
			return verifyRendered(s, want)
		}
		if !sameParams(want.Params, s.Params) {
			return fmt.Errorf("step %d of node %s's rollback: list is not what the runbook says", s.Index, Printable(s.Node))
		}
		return nil
	default:
		return fmt.Errorf("a step of node %s comes from %q, which is neither recorded nor authored", Printable(s.Node), s.Source)
	}
}

// verifyRecorded holds a recorded step to what its emitter declares in
// this build, and to the declarations it was recorded under (skew.go): a
// step whose undo digest (or, with none recorded, whose Collection digest)
// is not this build's runs only when the plan accepted the skew by name.
// SkewAccepted relaxes that comparison alone, never the checks of each
// value against this build's declarations (its type and choices), nor a
// parameter this build's undo method requires that the step does not carry.
// The identity is held
// to Shape, never Valid, and a group this build routes elsewhere is not
// refused: a minor release may move a method (FAILURE_PATTERNS 988).
func verifyRecorded(s Step) error {
	// No program's undo is ever planned, so a step carrying a program's
	// identity is not one a Controller made.
	if err := checkIdentity(s.Emitter, s.Collection, ""); err != nil {
		return err
	}
	spec, target, err := declaredUndo(s.Emitter, s.FQCN)
	if err != nil {
		return err
	}
	declared := fragment.ByNameIn(target)
	have := map[string]bool{}
	for k, v := range s.Params {
		param, isDeclared := declared[k]
		if collection.IsReservedParam(k) || !slices.Contains(spec.Record, k) || !isDeclared {
			return fmt.Errorf("the undo of %s through %s carries parameter %q, which %s does not declare recordable", Printable(s.Emitter), Printable(s.FQCN), k, Printable(s.Emitter))
		}
		if !recordableValue(v, engine.ListParam(s.FQCN, k)) {
			return fmt.Errorf("the undo of %s through %s carries a value for %q the journal would never hold", Printable(s.Emitter), Printable(s.FQCN), k)
		}
		if err := checkParamValue(k, v, param, s.FQCN); err != nil {
			return err
		}
		have[k] = true
	}
	// No group cache: a skew digests the group only for a step that recorded
	// no undo digest, or for a message (groupDigests).
	k := skewOf(s.Emitter, s.Collection, s.UndoDigest, *spec, target, nil)
	if err := k.missingRequired(s.Emitter, s.FQCN, target, have); err != nil {
		return err
	}
	if !k.skewed() {
		return nil
	}
	if !s.SkewAccepted {
		return fmt.Errorf("the undo of %s through %s was recorded under other declarations than this Runner's, and the rollback was not planned with allow_skew naming %s (--allow-skew on the CLI): %s",
			Printable(s.Emitter), Printable(s.FQCN), Printable(s.Node), k.describe())
	}
	return nil
}

// declaredUndo returns emitter's declaration of an undo through target,
// and target's manifest (a manifest, not a descriptor: nothing here holds
// its implementation, so nothing can ask one), refusing a
// method that is not a built-in this build knows, an undo it does not
// declare, or an undo method that is not implemented. A Controller registers
// no Collection, so both are read through collection.BuiltinManifest and
// ManifestOf, which fall back to the module catalog it installs; reading the
// registry alone refused every recorded undo there (FAILURE_PATTERNS 841).
func declaredUndo(emitter, target string) (*sdk.InverseSpec, collection.Manifest, error) {
	emits, ok := collection.BuiltinManifest(emitter)
	if !ok || emits.Status != collection.StatusImplemented {
		return nil, collection.Manifest{}, fmt.Errorf("the journal names %s, which is not a built-in method this build runs, so the undo it records is not trusted", Printable(emitter))
	}
	var spec *sdk.InverseSpec
	for i := range emits.Reversibility.Inverses {
		if emits.Reversibility.Inverses[i].FQCN == target {
			spec = &emits.Reversibility.Inverses[i]
		}
	}
	if spec == nil {
		return nil, collection.Manifest{}, fmt.Errorf("the journal records an undo of %s through %s, which %s does not declare, so it is not trusted", Printable(emitter), Printable(target), Printable(emitter))
	}
	undo, ok := collection.ManifestOf(target)
	if !ok || undo.Status != collection.StatusImplemented {
		return nil, collection.Manifest{}, fmt.Errorf("the undo method %s is not an implemented method in this build", Printable(target))
	}
	return spec, undo, nil
}

// recordableValue reports whether v is a value the journal's projection
// could have recorded, as it arrives after a trip through JSON or YAML; a
// list only for a parameter the undo method declares one (asList).
func recordableValue(v any, asList bool) bool {
	switch v := v.(type) {
	case string:
		return engine.RecordableText(v)
	case []string, []any:
		_, ok := engine.RecordableTexts(v)
		return ok && asList
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

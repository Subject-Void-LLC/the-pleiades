// Package engine: the facts root as one evaluation of one condition reads
// it, a CEL map that answers each key from the run's knowledge of the
// device (facts_overlay.go) and records every key it was asked for.
//
// An unknown key (a device never gathered for it, a store that cannot be
// read, a run with no store) is an outcome, never a value: the map answers it
// as missing, so the expression evaluates as CEL would, and the condition's
// evaluation is then failed whatever it came to (factsView.failure). Nothing
// in the expression can turn it into an answer: has(), "k" in facts, the
// is defined test, facts[?"k"].orValue(...) and a short-circuiting || or &&
// that never needed the value all touch the key, and a touch is what fails
// the task. An absent key, on a device a method that emits it has observed,
// is missing in the ordinary way, so a test for it is honest.
//
// The map is consulted only through the methods CEL calls on a map value
// (Find, Contains, Get), each implemented here with nothing embedded, so no
// read can reach a value without being recorded. Anything treating the root
// as a whole (iterating, counting, comparing, converting) is refused when the
// runbook is built; reaching it anyway fails the evaluation.
package engine

import (
	"context"
	"fmt"
	"reflect"
	"sort"

	"cel.dev/cel-go/common/types"
	"cel.dev/cel-go/common/types/ref"
	"cel.dev/cel-go/common/types/traits"
)

// factMiss is one key a condition read that had no answer.
type factMiss struct {
	key string
	// err is why, when the store could not be read; nil for a device never
	// gathered for the key.
	err error
}

// factsView is the facts root for one evaluation on one device.
type factsView struct {
	ctx     context.Context
	r       *run
	device  string
	checked bool
	// named are the keys the condition names, the only ones it may read.
	named map[string]bool

	// What the evaluation touched. Each evaluation has its own view and
	// CEL evaluates on one goroutine, so these need no lock.
	misses  []factMiss
	absent  []string
	present bool
	whole   bool
	// asked is every key looked up, so settle asks the rest.
	asked map[string]bool
}

var _ traits.Mapper = (*factsView)(nil)

// newFactsView is the facts root task's condition reads on device ("" for a
// task with no device, which has no facts).
func (r *run) newFactsView(ctx context.Context, task *Task, device string, checked bool) *factsView {
	named := map[string]bool{}
	for _, k := range task.conditionFactKeys() {
		named[k] = true
	}
	return &factsView{ctx: ctx, r: r, device: device, checked: checked, named: named}
}

// used reports whether the evaluation read the root at all.
func (v *factsView) used() bool {
	return v.present || v.whole || len(v.misses) > 0 || len(v.absent) > 0
}

// settle asks every key the condition names that its evaluation did not
// reach, and records each with no answer: a short-circuiting ||, && or list
// item that never needed a key does not make an unknown one known, so the
// condition fails as surely as one that read it. What settle finds present or
// absent is not recorded, since the evaluation did not read it.
func (v *factsView) settle() {
	keys := make([]string, 0, len(v.named))
	for k := range v.named {
		if !v.asked[k] {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		present, absent := v.present, len(v.absent)
		v.lookup(k)
		v.present, v.absent = present, v.absent[:absent]
	}
}

// lookup answers key, recording what it was.
func (v *factsView) lookup(key string) (ref.Val, bool) {
	if v.asked == nil {
		v.asked = map[string]bool{}
	}
	v.asked[key] = true
	if !v.named[key] {
		// Never expected: the condition names every key it reads.
		v.misses = append(v.misses, factMiss{key: key, err: fmt.Errorf("the condition does not name it as a literal key")})
		return nil, false
	}
	if v.device == "" {
		v.misses = append(v.misses, factMiss{key: key, err: errNoFactDevice})
		return nil, false
	}
	if v.r.knowledge == nil {
		v.misses = append(v.misses, factMiss{key: key, err: ErrNoFactReader})
		return nil, false
	}
	a, err := v.r.knowledge.answer(v.ctx, v.r, v.device, key, v.checked)
	switch {
	case err != nil:
		v.misses = append(v.misses, factMiss{key: key, err: err})
		return nil, false
	case a.State == FactPresent:
		v.present = true
		// A number is a json.Number in the store and the overlay alike,
		// which CEL reads as an int when it is whole and a double
		// otherwise, so facts["mem_mb"] > 4000 compares the same in the
		// run that gathered it and in every run after.
		return types.DefaultTypeAdapter.NativeToValue(a.Value), true
	case a.State == FactAbsent:
		v.absent = append(v.absent, key)
		return nil, false
	}
	v.misses = append(v.misses, factMiss{key: key})
	return nil, false
}

// Find implements traits.Mapper: every read of a key, has() and the optional
// forms included, comes here.
func (v *factsView) Find(key ref.Val) (ref.Val, bool) {
	s, ok := key.(types.String)
	if !ok {
		v.whole = true
		return types.NewErr("facts is read by a string key"), false
	}
	return v.lookup(string(s))
}

// Contains implements traits.Container: "key" in facts.
func (v *factsView) Contains(key ref.Val) ref.Val {
	val, found := v.Find(key)
	if types.IsError(val) {
		return val
	}
	return types.Bool(found)
}

// Get implements traits.Indexer.
func (v *factsView) Get(key ref.Val) ref.Val {
	val, found := v.Find(key)
	if types.IsError(val) {
		return val
	}
	if !found {
		return types.NewErr("no such key: %v", key)
	}
	return val
}

// Iterator implements traits.Iterable: the root as a whole, refused.
func (v *factsView) Iterator() traits.Iterator {
	v.whole = true
	return types.NewStringList(types.DefaultTypeAdapter, nil).Iterator()
}

// Size implements traits.Sizer: the root as a whole, refused.
func (v *factsView) Size() ref.Val {
	v.whole = true
	return types.NewErr("facts cannot be counted")
}

// ConvertToNative implements ref.Val: the root as a whole, refused.
func (v *factsView) ConvertToNative(reflect.Type) (any, error) {
	v.whole = true
	return nil, fmt.Errorf("facts cannot be used as a value")
}

// ConvertToType implements ref.Val: the root as a whole, refused.
func (v *factsView) ConvertToType(ref.Type) ref.Val {
	v.whole = true
	return types.NewErr("facts cannot be converted")
}

// Equal implements ref.Val: the root as a whole, refused.
func (v *factsView) Equal(ref.Val) ref.Val {
	v.whole = true
	return types.NewErr("facts cannot be compared")
}

// Type implements ref.Val: a map, which is what lets CEL index it.
func (v *factsView) Type() ref.Type { return types.MapType }

// Value implements ref.Val: the root as a whole, refused, so it answers
// nothing.
func (v *factsView) Value() any {
	v.whole = true
	return map[string]any{}
}

// missedKeys is the keys the evaluation read with no answer, each once, in
// order.
func (v *factsView) missedKeys() []string {
	seen := map[string]bool{}
	var keys []string
	for _, m := range v.misses {
		if !seen[m.key] {
			seen[m.key] = true
			keys = append(keys, m.key)
		}
	}
	sort.Strings(keys)
	return keys
}

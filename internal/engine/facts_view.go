// Package engine: the facts root as one evaluation of one condition reads
// it, a CEL map over the keys the condition names, each answered before CEL
// runs from the run's knowledge of the device (facts_overlay.go).
//
// The condition is three-valued (facts_condition.go). A present key holds
// its value, an absent one is missing in the ordinary way, so a test for it
// is honest, and a key with no answer (a device never gathered for it, a
// store that cannot be read, a run with no store, or in a check a key an
// earlier checked task would emit) is a CEL unknown: CEL decides what it can
// without it (false && unknown is false, true || unknown is true) and leaves
// the rest unknown, and nothing can turn an unknown into an answer: has(),
// in, facts[?"k"].hasValue(), .orValue(), a ternary, type(), a comprehension
// and a function of it all stay unknown. A task fails only when its
// condition's result is unknown, naming the keys the result depended on, and
// a result that is known never fails for a key it did not need.
//
// The map is consulted only through the methods CEL calls on a map value
// (Find, Contains, Get), each implemented here with nothing embedded, so no
// read can reach a value without being recorded; an unknown key never reaches
// them, since CEL meets it as unknown first. Anything treating the root as a
// whole (iterating, counting, comparing, converting) is refused when the
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

	// answers are the named keys' answers, unknown each key with no answer
	// and why (nil for a key nothing holds), and tainted each key a check
	// cannot know, with the reason, all settled by resolve before CEL runs.
	answers map[string]FactAnswer
	unknown map[string]error
	tainted map[string]string

	// What the evaluation touched. Each evaluation has its own view and
	// CEL evaluates on one goroutine, so these need no lock.
	misses  []factMiss
	absent  []string
	present bool
	whole   bool
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

// resolve answers every key the condition names, once, before CEL runs: the
// run's snapshot is read for all of them together anyway, and an answer is
// only looked up here; whether the condition needed it is CEL's to decide. A
// tainted key is unknown whatever is stored for it.
func (v *factsView) resolve() {
	v.answers, v.unknown = map[string]FactAnswer{}, map[string]error{}
	for _, key := range sortedKeys(v.named) {
		switch {
		case v.tainted[key] != "":
		case v.device == "":
			v.unknown[key] = errNoFactDevice
		case v.r.knowledge == nil:
			v.unknown[key] = ErrNoFactReader
		default:
			a, err := v.r.knowledge.answer(v.ctx, v.r, v.device, key, v.checked)
			if err != nil || a.State == FactUnknown {
				v.unknown[key] = err
				continue
			}
			v.answers[key] = a
		}
	}
}

// unknowns are the keys CEL meets as unknown: every named key with no
// answer, and every tainted one.
func (v *factsView) unknowns() []UnknownRegister {
	var out []UnknownRegister
	for _, key := range sortedKeys(v.named) {
		if _, unknown := v.unknown[key]; unknown || v.tainted[key] != "" {
			out = append(out, UnknownRegister{Name: key, Fact: true})
		}
	}
	return out
}

// lookup answers key from what resolve settled, recording what it was.
func (v *factsView) lookup(key string) (ref.Val, bool) {
	if !v.named[key] {
		// Never expected: the condition names every key it reads.
		v.misses = append(v.misses, factMiss{key: key, err: fmt.Errorf("the condition does not name it as a literal key")})
		return nil, false
	}
	a, answered := v.answers[key]
	switch {
	case !answered:
		// Never expected either: CEL meets a key with no answer as unknown
		// before it asks the map.
		v.misses = append(v.misses, factMiss{key: key, err: v.unknown[key]})
		return nil, false
	case a.State == FactPresent:
		v.present = true
		// A number is a json.Number in the store and the overlay alike,
		// which CEL reads as an int when it is whole and a double
		// otherwise, so facts["mem_mb"] > 4000 compares the same in the
		// run that gathered it and in every run after.
		return types.DefaultTypeAdapter.NativeToValue(a.Value), true
	}
	v.absent = append(v.absent, key)
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

// Package render: running a compiled template's node tree, with the
// render's scope, its step budget and its output limit.
package render

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
	"unicode/utf8"
)

// The evaluator: walks a compiled node tree against the caller's
// variables, holding every bound in limits.go as it goes.

// renderState is one render in progress. It is built per call and never
// shared, so the compiled template it reads stays immutable.
type renderState struct {
	// vars are the caller's variables.
	vars map[string]any

	// scopes are the names statements bound, innermost last: the template's
	// own top-level scope first, then one per for loop iteration running.
	// A set writes into the innermost one, which is what keeps a set inside
	// a loop from leaking out of it, as Jinja2 scopes it.
	scopes []map[string]any

	// out is the text written so far.
	out strings.Builder

	// iterations and evaluations count toward maxLoopIterations and
	// maxEvaluations.
	iterations  int
	evaluations int
}

// lookup implements lookup: the innermost bound name first, then the
// caller's variables.
func (st *renderState) lookup(name string) (any, bool) {
	for i := len(st.scopes) - 1; i >= 0; i-- {
		if v, ok := st.scopes[i][name]; ok {
			return v, true
		}
	}
	v, ok := st.vars[name]
	return v, ok
}

// step counts one evaluation step and refuses past the bound.
func (st *renderState) step() error { return st.charge(1) }

// charge implements env: it spends n steps of the budget.
func (st *renderState) charge(n int) error {
	st.evaluations += n
	if st.evaluations > maxEvaluations {
		return fmt.Errorf("%w: rendering took more than %d steps", ErrTooLarge, maxEvaluations)
	}
	return nil
}

// write appends text and holds the output bound after every write, so a
// loop stops at the bound rather than building its whole output first.
func (st *renderState) write(s string) error {
	st.out.WriteString(s)
	if st.out.Len() > maxOutputBytes {
		return fmt.Errorf("%w: output passed %d bytes", ErrTooLarge, maxOutputBytes)
	}
	return nil
}

// run evaluates nodes in order.
func (st *renderState) run(nodes []node) error {
	for i := range nodes {
		if err := st.step(); err != nil {
			return err
		}
		if err := st.runNode(&nodes[i]); err != nil {
			return err
		}
	}
	return nil
}

// runNode evaluates one node.
func (st *renderState) runNode(n *node) error {
	switch n.kind {
	case nodeText:
		return st.write(n.text)

	case nodeOutput:
		s, err := n.expr.eval(st)
		if err != nil {
			return err
		}
		return st.write(s)

	case nodeSet:
		value, err := n.expr.evalValue(st)
		if err != nil {
			return err
		}
		if len(st.scopes) == 0 {
			st.scopes = append(st.scopes, map[string]any{})
		}
		st.scopes[len(st.scopes)-1][n.name] = value
		return nil

	case nodeIf:
		for _, br := range n.branches {
			if br.cond != nil {
				value, err := br.cond.evalValue(st)
				if err != nil {
					return err
				}
				if !truthy(value) {
					continue
				}
			}
			// An if opens no scope of its own, as in Jinja2: a set inside one
			// is seen after it.
			return st.run(br.body)
		}
		return nil

	case nodeFor:
		return st.runFor(n.loop)
	}
	return nil
}

// runFor evaluates one for loop: its body once per item, each iteration in
// a fresh scope holding the loop's names and the loop variable, or its else
// when there was no item.
func (st *renderState) runFor(loop *forLoop) error {
	value, err := loop.iter.evalValue(st)
	if err != nil {
		return err
	}
	items, err := iterate(value)
	if err != nil {
		return fmt.Errorf("%w (in %q)", err, loop.iter.source)
	}
	// Refused before the first iteration rather than at the bound, so a loop
	// too long to finish does no work at all.
	if st.iterations+len(items) > maxLoopIterations {
		return fmt.Errorf("%w: for loops would run more than %d iterations", ErrTooLarge, maxLoopIterations)
	}
	if len(items) == 0 {
		st.scopes = append(st.scopes, map[string]any{})
		defer func() { st.scopes = st.scopes[:len(st.scopes)-1] }()
		return st.run(loop.otherwise)
	}

	for i, item := range items {
		st.iterations++
		scope, err := bindTargets(loop.targets, item)
		if err != nil {
			return fmt.Errorf("%w (in %q)", err, loop.iter.source)
		}
		scope[loopName] = loopVariable(i, len(items))
		st.scopes = append(st.scopes, scope)
		err = st.run(loop.body)
		st.scopes = st.scopes[:len(st.scopes)-1]
		if err != nil {
			return err
		}
	}
	return nil
}

// loopVariable is the loop variable for item i of n, with Jinja2's names.
func loopVariable(i, n int) map[string]any {
	return map[string]any{
		"index":     i + 1,
		"index0":    i,
		"revindex":  n - i,
		"revindex0": n - i - 1,
		"first":     i == 0,
		"last":      i == n-1,
		"length":    n,
	}
}

// bindTargets binds one item to a loop's names. Two names unpack an item
// that is a pair, as .items() yields.
func bindTargets(targets []string, item any) (map[string]any, error) {
	if len(targets) == 1 {
		return map[string]any{targets[0]: item}, nil
	}
	pair, err := iterate(item)
	if err != nil || len(pair) != len(targets) {
		return nil, fmt.Errorf("%w: a for loop with %d names needs items of %d values each", ErrNotIterable, len(targets), len(targets))
	}
	return map[string]any{targets[0]: pair[0], targets[1]: pair[1]}, nil
}

// iterate returns the items a for loop walks over value. A list gives its
// elements, a mapping its keys in sorted order, and a string its
// characters, as in Jinja2. Anything else has no items.
func iterate(value any) ([]any, error) {
	switch v := value.(type) {
	case []any:
		return v, nil
	case []string:
		out := make([]any, len(v))
		for i, s := range v {
			out[i] = s
		}
		return out, nil
	case string:
		// Counted before a string is split, so a megabyte of text does not
		// become a million items only to be refused.
		if utf8.RuneCountInString(v) > maxLoopIterations {
			return nil, fmt.Errorf("%w: a for loop over text longer than %d characters", ErrTooLarge, maxLoopIterations)
		}
		out := make([]any, 0, len(v))
		for _, r := range v {
			out = append(out, string(r))
		}
		return out, nil
	case nil:
		return nil, fmt.Errorf("%w: the value is null", ErrNotIterable)
	}

	rv := reflect.ValueOf(value)
	switch rv.Kind() {
	case reflect.Slice, reflect.Array:
		out := make([]any, rv.Len())
		for i := range out {
			out[i] = rv.Index(i).Interface()
		}
		return out, nil
	case reflect.Map:
		return mappingItems(value, "keys")
	}
	return nil, fmt.Errorf("%w: a %T has no items", ErrNotIterable, value)
}

// mappingItems returns a mapping's keys, values or key-value pairs, in
// sorted key order. Only a mapping with string keys qualifies, which is
// every mapping decoded YAML or JSON produces.
// method is items, keys or values.
func mappingItems(value any, method string) ([]any, error) {
	rv := reflect.ValueOf(value)
	if value == nil || rv.Kind() != reflect.Map || rv.Type().Key().Kind() != reflect.String {
		return nil, fmt.Errorf("%w: .items(), .keys() and .values() need a mapping with text keys, not a %T", ErrNotIterable, value)
	}
	keys := make([]string, 0, rv.Len())
	for _, k := range rv.MapKeys() {
		keys = append(keys, k.String())
	}
	sort.Strings(keys)

	out := make([]any, len(keys))
	for i, k := range keys {
		mv := rv.MapIndex(reflect.ValueOf(k).Convert(rv.Type().Key())).Interface()
		switch method {
		case "items":
			out[i] = []any{k, mv}
		case "values":
			out[i] = mv
		default:
			out[i] = k
		}
	}
	return out, nil
}

// truthy is Jinja2's truth test: false, null, zero, and an empty string,
// list or mapping are false, and every other value is true.
func truthy(value any) bool {
	switch v := value.(type) {
	case nil:
		return false
	case bool:
		return v
	case string:
		return v != ""
	}
	rv := reflect.ValueOf(value)
	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return rv.Int() != 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return rv.Uint() != 0
	case reflect.Float32, reflect.Float64:
		return rv.Float() != 0
	case reflect.Slice, reflect.Array, reflect.Map, reflect.String:
		return rv.Len() != 0
	case reflect.Pointer, reflect.Interface:
		return !rv.IsNil()
	}
	return true
}

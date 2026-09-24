// Package playbook: unrolling a loop over a list this converter knows.
package playbook

import (
	"fmt"
	"strings"

	"go.yaml.in/yaml/v3"
)

// loopSpec is a task's loop, resolved to its items.
type loopSpec struct {
	present           bool
	items             []any
	loopVar, indexVar string
}

// loopOf resolves a task's loop to its items, when the list is written in
// the playbook or comes from a variable with one literal value. A loop
// over anything known only at run time, one that registers, and one using
// a loop_control option this does not reproduce block the task.
func (t *translator) loopOf(entries []entry, spec leafSpec, ctx taskCtx) (loopSpec, []string, []string) {
	var key entry
	for _, e := range entries {
		if e.key == "loop" || strings.HasPrefix(e.key, "with_") {
			key = e
		}
	}
	if key.key == "" {
		return loopSpec{}, nil, nil
	}
	at := nodePos(key.keyAt, t.file)
	block := func(code Code, why string) (loopSpec, []string, []string) {
		return loopSpec{}, []string{t.raise(code, at, spec.name, why)}, nil
	}
	switch key.key {
	case "loop", "with_items", "with_list", "with_dict":
	default:
		return block("loop.runtime", key.key+" is a lookup this converter does not evaluate")
	}
	if spec.register != "" {
		return block("loop.register", "a looped task registers its result")
	}
	loop := loopSpec{present: true, loopVar: "item"}
	if lc := deref(lookup(nodeOfEntries(entries), "loop_control")); lc != nil {
		lcEntries, _ := mapEntries(lc)
		for _, e := range lcEntries {
			v := deref(e.value)
			switch e.key {
			case "loop_var":
				loop.loopVar = v.Value
			case "index_var":
				loop.indexVar = v.Value
			case "label":
			default:
				return block("loop.control", "loop_control: "+e.key+" is not reproduced")
			}
		}
	}
	v, rerr := t.goValue(key.value, nil, at)
	if rerr != nil {
		return block("loop.runtime", rerr.why)
	}
	items, ok := loopItems(key.key, v, t.dictOrder(key.value))
	switch {
	case !ok:
		return block("loop.runtime", "the loop's value is not a list known here")
	case len(items) > maxLoopItems:
		return block("loop.too_long", fmt.Sprintf("the loop has %d items", len(items)))
	}
	loop.items = items
	return loop, nil, []string{t.raise("loop.unrolled", at, spec.name, fmt.Sprintf("the loop became %d tasks", len(items)))}
}

// loopItems turns a loop's resolved value into its items as Ansible
// iterates them: with_items flattens one level of nested lists, and
// with_dict yields {key, value} maps in the map's written order, which
// order gives (nil when it could not be read, which refuses the loop).
func loopItems(key string, v any, order []string) ([]any, bool) {
	switch key {
	case "with_dict":
		m, ok := v.(map[string]any)
		if !ok || len(order) != len(m) {
			return nil, false
		}
		items := make([]any, 0, len(m))
		for _, k := range order {
			items = append(items, map[string]any{"key": k, "value": m[k]})
		}
		return items, true
	case "with_items":
		list, ok := v.([]any)
		if !ok {
			return nil, false
		}
		var items []any
		for _, item := range list {
			if inner, ok := item.([]any); ok {
				items = append(items, inner...)
			} else {
				items = append(items, item)
			}
		}
		return items, true
	}
	list, ok := v.([]any)
	return list, ok
}

// nodeOfEntries rebuilds a map node from entries, for lookup.
func nodeOfEntries(entries []entry) *yaml.Node {
	m := &yaml.Node{Kind: yaml.MappingNode}
	for _, e := range entries {
		m.Content = append(m.Content, e.keyAt, e.value)
	}
	return m
}

// dictOrder returns the key order of the map a with_dict names: written
// inline, or the one literal definition of the single variable it names.
// A Go map has no order, and the tasks a loop unrolls into run in order,
// so the order is read from the YAML itself.
func (t *translator) dictOrder(n *yaml.Node) []string {
	n = deref(n)
	if n != nil && n.Kind == yaml.ScalarNode {
		m := wholeVar.FindStringSubmatch(strings.TrimSpace(n.Value))
		if m == nil {
			return nil
		}
		def, rerr := t.ix.literal(m[1])
		if rerr != nil {
			return nil
		}
		n = deref(def.value)
	}
	entries, _ := mapEntries(n)
	order := make([]string, 0, len(entries))
	for _, e := range entries {
		order = append(order, e.key)
	}
	return order
}

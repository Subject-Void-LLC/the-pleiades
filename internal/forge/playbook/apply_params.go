// Package playbook: setting one argument's value on the native calls a
// task becomes, converted to each parameter's declared type.
package playbook

import (
	"errors"
	"fmt"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/fragment"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// mapArg sets rule's native parameter on every call, or, for ArgUnroll
// given several items, makes one copy of the calls per item.
func (t *translator) mapArg(calls []plannedCall, rule Arg, in argIn, bindings map[string]any, p *plan) ([]plannedCall, *blockReason) {
	if rule.Handling == ArgUnroll {
		items, blocked := t.unrollItems(in, bindings)
		if blocked != nil {
			return nil, blocked
		}
		if items != nil {
			return t.unroll(calls, rule, in, items, bindings, p)
		}
	}
	// The argument goes to every call whose method declares its parameter
	// (systemd's name goes to start and enable, not to daemon_reload), and
	// blocks the task only when none does.
	set := 0
	for i := range calls {
		if _, declared := paramTypes(calls[i].call.FQCN)[rule.To]; !declared && len(calls) > 1 {
			continue
		}
		if err := t.setParam(&calls[i], rule.To, nil, in, bindings); err != nil {
			return nil, err
		}
		set++
	}
	if set == 0 {
		return nil, &blockReason{"args.unmapped", in.at, fmt.Sprintf("no native call here takes %s", in.name)}
	}
	return calls, nil
}

// unrollItems returns what an ArgUnroll argument lists, as Ansible's list
// type reads it: a written list's items (each still its node, so it
// converts by how it was written), a template's resolved list, or a
// text's comma-separated names. A single value returns nil.
func (t *translator) unrollItems(in argIn, bindings map[string]any) ([]any, *blockReason) {
	if n := deref(in.node); n != nil && n.Kind == yaml.SequenceNode {
		items := make([]any, len(n.Content))
		for i, item := range n.Content {
			items[i] = item
		}
		return items, nil
	}
	v, rerr := t.goValue(in.node, bindings, in.at)
	if rerr != nil {
		return nil, &blockReason{rerr.code, in.at, fmt.Sprintf("%s: %s", in.name, rerr.why)}
	}
	switch x := v.(type) {
	case []any:
		items := make([]any, len(x))
		for i, item := range x {
			items[i] = resolvedItem{item}
		}
		return items, nil
	case string:
		if !strings.Contains(x, ",") {
			return nil, nil
		}
		parts := strings.Split(x, ",")
		items := make([]any, len(parts))
		for i, part := range parts {
			if part = strings.TrimSpace(part); part == "" {
				return nil, &blockReason{"args.value", in.at, fmt.Sprintf("%s has an empty name between its commas", in.name)}
			}
			items[i] = resolvedItem{part}
		}
		return items, nil
	}
	return nil, nil
}

// unroll sets one item on each call, or, given several, makes one copy of
// the calls per item.
func (t *translator) unroll(calls []plannedCall, rule Arg, in argIn, items []any, bindings map[string]any, p *plan) ([]plannedCall, *blockReason) {
	switch {
	case len(items) == 0:
		return nil, &blockReason{"args.value", in.at, fmt.Sprintf("%s lists nothing", in.name)}
	case len(items)*len(calls) > maxLoopItems:
		return nil, &blockReason{"loop.too_long", in.at, fmt.Sprintf("%s lists %d items", in.name, len(items))}
	case len(items) > 1:
		p.notes = append(p.notes, note{"args.list_unrolled", in.at, fmt.Sprintf("%s lists %d names; each becomes its own task", in.name, len(items))})
	}
	var out []plannedCall
	for _, c := range calls {
		for _, item := range items {
			copied := plannedCall{call: c.call, params: fixed(&Call{Fixed: c.params})}
			if err := t.setParam(&copied, rule.To, item, in, bindings); err != nil {
				return nil, err
			}
			if len(items) > 1 {
				copied.label = fmt.Sprint(copied.params[rule.To])
			}
			out = append(out, copied)
		}
	}
	return out, nil
}

// resolvedItem is one unrolled item whose value a template or a comma
// split produced, so it no longer carries how it was written.
type resolvedItem struct{ v any }

// setParam converts in to param's declared native type and sets it on
// c. item, when not nil, is one unrolled element instead: a written node,
// or a resolvedItem.
func (t *translator) setParam(c *plannedCall, param string, item any, in argIn, bindings map[string]any) *blockReason {
	types := paramTypes(c.call.FQCN)
	typ, declared := types[param]
	if !declared {
		return &blockReason{"args.unmapped", in.at, fmt.Sprintf("%s has no parameter for %s", c.call.FQCN, in.name)}
	}
	node := in.node
	if n, ok := item.(*yaml.Node); ok {
		node = n
	}
	var v any
	var err error
	switch r, resolved := item.(resolvedItem); {
	case resolved:
		v, err = coerceGo(r.v, typ, param)
	case hasTemplateNode(node):
		raw, rerr := t.goValue(node, bindings, in.at)
		if rerr != nil {
			return &blockReason{rerr.code, in.at, fmt.Sprintf("%s: %s", in.name, rerr.why)}
		}
		v, err = coerceGo(raw, typ, param)
	default:
		v, err = toNative(node, typ, param)
	}
	if err != nil {
		var rerr *resolveError
		if errors.As(err, &rerr) {
			return &blockReason{rerr.code, in.at, fmt.Sprintf("%s: %s", in.name, rerr.why)}
		}
		return &blockReason{"args.value", in.at, fmt.Sprintf("%s: %v", in.name, err)}
	}
	c.params[param] = v
	return nil
}

// paramTypes returns fqcn's declared parameters and their types, its
// fragments' included.
func paramTypes(fqcn string) map[string]string {
	d, ok := collection.Lookup(fqcn)
	if !ok {
		return nil
	}
	out := map[string]string{}
	shared, _ := fragment.Params(d.Manifest.Doc.Fragments)
	for _, p := range append(shared, d.Manifest.Doc.Params...) {
		out[p.Name] = p.Type
	}
	return out
}

// checkRequired blocks a call missing a parameter its method requires.
func checkRequired(c plannedCall, taskAt Position) *blockReason {
	d, ok := collection.Lookup(c.call.FQCN)
	if !ok {
		return &blockReason{"module.unmapped", taskAt, fmt.Sprintf("%s is not a registered native method", c.call.FQCN)}
	}
	for _, p := range d.Manifest.Doc.Params {
		if _, set := c.params[p.Name]; p.Required && !set {
			return &blockReason{"args.unmapped", taskAt, fmt.Sprintf("%s requires %s, which the task does not give", c.call.FQCN, p.Name)}
		}
	}
	return nil
}

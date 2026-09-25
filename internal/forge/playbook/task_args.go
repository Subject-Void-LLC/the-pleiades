// Package playbook: a task's module arguments, from a map, a free-form
// string, and the args: keyword.
package playbook

import (
	"fmt"
	"slices"

	"go.yaml.in/yaml/v3"
)

// moduleArgs returns the arguments spec passes its module. A map is read
// as written; a string is split as Ansible splits free-form arguments
// (kv.go); args: supplies any key the module's own value does not set.
func (t *translator) moduleArgs(e *Entry, spec leafSpec) ([]argIn, error) {
	var args []argIn
	given := map[string]bool{}
	add := func(name string, at Position, node *yaml.Node) {
		if !given[name] {
			given[name] = true
			args = append(args, argIn{name: name, at: at, node: node})
		}
	}
	v := deref(spec.modNode)
	switch {
	case v == nil || (v.Kind == yaml.ScalarNode && v.ShortTag() == "!!null"):
	case v.Kind == yaml.MappingNode:
		entries, repeated := mapEntries(v)
		if len(repeated) > 0 {
			return nil, fmt.Errorf("argument %s is repeated", repeated[0].key)
		}
		for _, e := range entries {
			add(e.key, nodePos(e.keyAt, t.file), e.value)
		}
	case v.Kind == yaml.ScalarNode:
		var rawKeys map[string]bool
		if e.FreeForm == FreeFormCommand {
			rawKeys = map[string]bool{}
			for _, k := range e.RawKeys {
				rawKeys[k] = true
			}
		}
		kv, raw, err := parseKV(v.Value, rawKeys)
		if err != nil {
			return nil, err
		}
		keys := make([]string, 0, len(kv))
		for k := range kv {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		for _, k := range keys {
			add(k, spec.modAt, textNode(kv[k], v))
		}
		if raw != "" {
			if e.RawArg == "" {
				return nil, fmt.Errorf("free-form text where %s takes only key=value arguments", e.Module)
			}
			add(e.RawArg, spec.modAt, textNode(raw, v))
		}
	default:
		return nil, fmt.Errorf("the module's arguments are a list")
	}
	if a := deref(spec.args); a != nil {
		if a.Kind != yaml.MappingNode {
			return nil, fmt.Errorf("args: is not a map")
		}
		entries, _ := mapEntries(a)
		for _, e := range entries {
			add(e.key, nodePos(e.keyAt, t.file), e.value)
		}
	}
	return args, nil
}

// textNode is a quoted scalar holding s at src's position: a free-form
// value is text to Ansible, never a YAML boolean or number.
func textNode(s string, src *yaml.Node) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: s, Style: yaml.DoubleQuotedStyle, Line: src.Line, Column: src.Column}
}

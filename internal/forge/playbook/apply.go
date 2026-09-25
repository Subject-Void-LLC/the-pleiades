// Package playbook: applying a module table entry to one task's
// arguments.
package playbook

import (
	"fmt"
	"slices"
	"strconv"

	"go.yaml.in/yaml/v3"
)

// argIn is one argument as the task wrote it.
type argIn struct {
	name string
	at   Position
	node *yaml.Node
}

// blockReason is why a task is not converted.
type blockReason struct {
	code Code
	at   Position
	why  string
}

// note is a finding raised without blocking the task.
type note struct {
	code Code
	at   Position
	why  string
}

// plannedCall is one native call a task becomes.
type plannedCall struct {
	call   Call
	params map[string]any
	// label tells copies apart when one task becomes several ("curl").
	label string
}

// plan is what an entry makes of a task.
type plan struct {
	calls    []plannedCall
	blocked  *blockReason
	notes    []note
	computed bool
}

// apply maps args onto e's native calls.
func (t *translator) apply(e *Entry, args []argIn, bindings map[string]any, taskAt Position) plan {
	var p plan
	rules := map[string]*Arg{}
	for i := range e.Args {
		a := &e.Args[i]
		rules[a.Name] = a
		for _, alias := range a.Aliases {
			rules[alias] = a
		}
	}
	given := map[string]argIn{}
	for _, in := range args {
		rule, ok := rules[in.name]
		switch {
		case !ok:
			p.blocked = &blockReason{"args.unmapped", in.at, fmt.Sprintf("argument %s has no native equivalent here", in.name)}
			return p
		case rule.Handling == ArgBlock:
			p.blocked = &blockReason{rule.Code, in.at, fmt.Sprintf("argument %s: %s", in.name, rule.Reason)}
			return p
		case rule.Handling == ArgDrop:
			p.notes = append(p.notes, note{rule.Code, in.at, fmt.Sprintf("argument %s dropped: %s", in.name, rule.Reason)})
		default:
			given[rule.Name] = in
		}
	}
	calls, ignored, blocked, computed := t.selectCalls(e, given, bindings, taskAt, &p)
	if blocked != nil {
		p.blocked, p.computed = blocked, computed
		return p
	}
	for _, rule := range e.Args {
		in, ok := given[rule.Name]
		if !ok || (rule.Handling != ArgMap && rule.Handling != ArgUnroll) {
			continue
		}
		if ignored[rule.Name] {
			p.notes = append(p.notes, note{"args.ignored", in.at, fmt.Sprintf("argument %s dropped: Ansible ignores it here", in.name)})
			continue
		}
		if rule.Note != "" {
			p.notes = append(p.notes, note{"module.semantics", in.at, fmt.Sprintf("argument %s: %s", in.name, rule.Note)})
		}
		if calls, blocked = t.mapArg(calls, rule, in, bindings, &p); blocked != nil {
			p.blocked = blocked
			return p
		}
	}
	for i := range calls {
		if e.Adjust != nil {
			if why := e.Adjust(calls[i].params); why != "" {
				p.notes = append(p.notes, note{"module.semantics", taskAt, why})
			}
		}
		if blocked := checkRequired(calls[i], taskAt); blocked != nil {
			p.blocked = blocked
			return p
		}
	}
	p.calls = calls
	return p
}

// selectCalls picks the calls e's selectors choose, and the arguments
// every chosen value ignores.
func (t *translator) selectCalls(e *Entry, given map[string]argIn, bindings map[string]any, taskAt Position, p *plan) ([]plannedCall, map[string]bool, *blockReason, bool) {
	var calls []plannedCall
	var chosen []*Choice
	for _, sel := range e.Selectors {
		value, at := sel.Absent, taskAt
		if in, ok := given[sel.Arg]; ok {
			v, rerr := t.selectorValue(in, sel.Bool, bindings)
			if rerr != nil {
				code, computed := rerr.code, false
				if code == "template.unresolved" {
					code, computed = "state.computed", true
				}
				return nil, nil, &blockReason{code, in.at, fmt.Sprintf("%s: %s", sel.Arg, rerr.why)}, computed
			}
			value, at = v, in.at
		}
		if value == "" {
			continue
		}
		choice := findChoice(sel, value)
		switch {
		case choice == nil:
			// The value is the playbook's, so it is pointed at, not printed.
			return nil, nil, &blockReason{"args.value", at, fmt.Sprintf("%s: this value has no native equivalent here", sel.Arg)}, false
		case choice.Call == nil && choice.Code == "":
			// A value that asks for nothing (daemon_reload: false).
			continue
		case choice.Call == nil:
			return nil, nil, &blockReason{choice.Code, at, fmt.Sprintf("%s=%s: %s", sel.Arg, value, choice.Reason)}, false
		}
		chosen = append(chosen, choice)
		calls = append(calls, t.planCall(choice.Call, at, p))
	}
	if len(calls) == 0 {
		if e.Default == nil {
			return nil, nil, &blockReason{"args.unmapped", taskAt, "the task gives none of the arguments that choose what it does"}, false
		}
		calls = append(calls, t.planCall(e.Default, taskAt, p))
	}
	return calls, ignoredByAll(chosen), nil, false
}

// planCall starts a planned call from c, raising its note.
func (t *translator) planCall(c *Call, at Position, p *plan) plannedCall {
	if c.Note != "" {
		p.notes = append(p.notes, note{"module.semantics", at, c.Note})
	}
	return plannedCall{call: *c, params: fixed(c)}
}

// ignoredByAll returns the arguments every chosen value ignores. With no
// choice made (the entry's default call), nothing is ignored.
func ignoredByAll(chosen []*Choice) map[string]bool {
	out := map[string]bool{}
	if len(chosen) == 0 {
		return out
	}
	for _, name := range chosen[0].Ignores {
		out[name] = true
	}
	for _, c := range chosen[1:] {
		for name := range out {
			if !slices.Contains(c.Ignores, name) {
				delete(out, name)
			}
		}
	}
	return out
}

// findChoice returns the choice listing value, or nil.
func findChoice(sel Selector, value string) *Choice {
	for i := range sel.Choices {
		if slices.Contains(sel.Choices[i].Values, value) {
			return &sel.Choices[i]
		}
	}
	return nil
}

// fixed copies a call's fixed parameters.
func fixed(c *Call) map[string]any {
	out := map[string]any{}
	for k, v := range c.Fixed {
		out[k] = v
	}
	return out
}

// selectorValue reads a selector argument as text, a template resolved:
// a boolean argument by Ansible's boolean rules as true or false.
func (t *translator) selectorValue(in argIn, isBool bool, bindings map[string]any) (string, *resolveError) {
	v, rerr := t.goValue(in.node, bindings, in.at)
	if rerr != nil {
		return "", rerr
	}
	if isBool {
		b, err := coerceGo(v, "bool", in.name)
		if err != nil {
			return "", &resolveError{"args.value", "the value is not a boolean"}
		}
		v = b
	}
	switch x := v.(type) {
	case bool:
		return strconv.FormatBool(x), nil
	case string:
		return x, nil
	case int64:
		return strconv.FormatInt(x, 10), nil
	}
	return "", &resolveError{"args.value", "the value is not a single word"}
}

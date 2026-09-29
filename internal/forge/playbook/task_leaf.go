// Package playbook: translating a task that runs one module.
package playbook

import (
	"fmt"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/Subject-Void-LLC/the-pleiades/internal/termsafe"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// leafSpec is everything a leaf task says, read once, before any loop
// item is bound.
type leafSpec struct {
	name      string
	at        Position
	module    string
	modNode   *yaml.Node
	modAt     Position
	args      *yaml.Node
	whens     []*yaml.Node
	register  string
	tags      []string
	checkMode bool
	hasLoop   bool
	// local is set when the task is written to run on the Ansible
	// controller (delegate_to: localhost, local_action, connection: local);
	// localRun decides it once the native methods are known.
	local localMark
	// blocks are finding IDs that stop the task; cites are findings its
	// comment names without stopping it.
	blocks, cites []string
}

// localMark records how a task asked to run on the Ansible controller, and
// where, or nothing.
type localMark struct {
	// key is the spelling the task used: "delegate_to: localhost",
	// "local_action" or "connection: local"; empty when it asked nothing.
	key string
	at  Position
}

// translateLeaf translates a task that runs one module.
func (t *translator) translateLeaf(node *yaml.Node, entries []entry, name string, ctx taskCtx) []*outTask {
	spec := t.readLeaf(node, entries, name, ctx)
	if spec.module == "" {
		return []*outTask{t.placeholder(name, "task", spec.at, ctx, append(spec.blocks, spec.cites...)...)}
	}
	if out, handled := t.engineModule(spec, ctx); handled {
		return out
	}
	if len(ctx.blockers) > 0 || len(spec.blocks) > 0 {
		return []*outTask{t.placeholder(name, spec.module, spec.at, ctx, append(append(ctx.blockers, spec.blocks...), spec.cites...)...)}
	}
	entry, ok := lookupModule(spec.module, ctx.collections)
	if !ok {
		id := t.raise("module.unmapped", spec.modAt, name, fmt.Sprintf("module %s has no native equivalent here", spec.module))
		return []*outTask{t.placeholder(name, spec.module, spec.at, ctx, append(spec.cites, id)...)}
	}
	if entry.Rating == RatingManual {
		id := t.raise(entry.Code, spec.modAt, name, fmt.Sprintf("%s: %s", spec.module, entry.Reason))
		return []*outTask{t.placeholder(name, spec.module, spec.at, ctx, append(spec.cites, id)...)}
	}
	if t.localRun(entry, &spec) {
		return []*outTask{t.placeholder(name, spec.module, spec.at, ctx, append(spec.blocks, spec.cites...)...)}
	}
	args, err := t.moduleArgs(entry, spec)
	if err != nil {
		id := t.raise("args.unparsable", spec.modAt, name, err.Error())
		return []*outTask{t.placeholder(name, spec.module, spec.at, ctx, append(spec.cites, id)...)}
	}
	loop, loopBlocks, loopCites := t.loopOf(entries, spec, ctx)
	spec.cites = append(spec.cites, loopCites...)
	if len(loopBlocks) > 0 {
		return []*outTask{t.placeholder(name, spec.module, spec.at, ctx, append(loopBlocks, spec.cites...)...)}
	}
	if !loop.present {
		return t.convertOnce(entry, spec, args, nil, "", ctx)
	}
	var out []*outTask
	for i, item := range loop.items {
		bindings := map[string]any{loop.loopVar: item}
		if loop.indexVar != "" {
			bindings[loop.indexVar] = int64(i)
		}
		out = append(out, t.convertOnce(entry, spec, args, bindings, fmt.Sprintf("item %d of %d", i+1, len(loop.items)), ctx)...)
	}
	return out
}

// readLeaf reads a leaf task's keywords, raising a finding for each one
// that is dropped or that blocks it.
func (t *translator) readLeaf(node *yaml.Node, entries []entry, name string, ctx taskCtx) leafSpec {
	spec := leafSpec{name: name, at: nodePos(node, t.file), checkMode: ctx.checkMode}
	var modules []entry
	become := false
	for _, e := range entries {
		at := nodePos(e.keyAt, t.file)
		if isModuleKey(e.key) {
			modules = append(modules, e)
			continue
		}
		if strings.HasPrefix(e.key, "with_") || e.key == "loop" {
			spec.hasLoop = true
			if e.key != "loop" {
				continue
			}
		}
		rule := taskKeywords[e.key]
		switch {
		case rule.handling == kwDrop && !literalFalse(e.value):
			spec.cites = append(spec.cites, t.raise(rule.code, at, name, e.key+" dropped"))
			become = become || rule.code == "keyword.become"
		case rule.handling == kwBlock && !literalFalse(e.value):
			spec.blocks = append(spec.blocks, t.raise(rule.code, at, name, e.key+" has no native equivalent"))
		case rule.handling == kwSpecial:
			t.readSpecial(&spec, e, at)
		}
	}
	if bu := deref(lookup(node, "become_user")); become && bu != nil && bu.Value != "root" {
		spec.blocks = append(spec.blocks, t.raise("keyword.become_user", nodePos(bu, t.file), name, "become_user names a user other than root"))
	}
	t.readModule(&spec, node, modules)
	return spec
}

// readSpecial reads one kwSpecial keyword into spec.
func (t *translator) readSpecial(spec *leafSpec, e entry, at Position) {
	v := deref(e.value)
	switch e.key {
	case "when":
		if v != nil && v.Kind == yaml.SequenceNode {
			spec.whens = append(spec.whens, v.Content...)
		} else {
			spec.whens = append(spec.whens, v)
		}
	case "register":
		if v == nil || v.Kind != yaml.ScalarNode || hasTemplate(v.Value) || v.Value == "" {
			spec.blocks = append(spec.blocks, t.raise("args.value", at, spec.name, "register must name a variable"))
			return
		}
		spec.register = v.Value
	case "tags":
		tags, ok := readTags(v)
		if !ok {
			spec.blocks = append(spec.blocks, t.raise("keyword.tags", at, spec.name, "tags are templated, or name all, tagged or untagged"))
			return
		}
		spec.tags = tags
	case "check_mode":
		switch {
		case v != nil && v.Kind == yaml.ScalarNode && !hasTemplate(v.Value) && readScalar(v).kind == "bool" && readScalar(v).b:
			spec.checkMode = true
		case literalFalse(v):
			spec.cites = append(spec.cites, t.raise("keyword.check_mode_false", at, spec.name, "check_mode: false dropped"))
		default:
			spec.blocks = append(spec.blocks, t.raise("keyword.check_mode", at, spec.name, "check_mode is not a literal true or false"))
		}
	case "failed_when":
		if literalFalse(v) {
			spec.cites = append(spec.cites, t.raise("keyword.ignore_errors", at, spec.name, "failed_when: false dropped"))
		} else {
			spec.blocks = append(spec.blocks, t.raise("keyword.failed_when", at, spec.name, "failed_when has no native equivalent"))
		}
	case "delegate_to":
		// Delegating to the controller is what a controller-side method
		// already does (PLAN.md Section 14), so it is decided once the
		// native method is known; delegating to another host has no
		// native equivalent at all.
		if isLocalHost(v) {
			spec.local = localMark{key: "delegate_to: localhost", at: at}
		} else if !literalFalse(e.value) {
			spec.blocks = append(spec.blocks, t.raise("keyword.delegate_to", at, spec.name, "delegate_to runs the task on another host"))
		}
	case "local_action":
		spec.local = localMark{key: "local_action", at: at}
	case "connection":
		if v != nil && v.Value == "local" {
			spec.local = localMark{key: "connection: local", at: at}
		} else {
			spec.cites = append(spec.cites, t.raise("keyword.connection", at, spec.name, "connection dropped"))
		}
	case "args":
		spec.args = e.value
	}
}

// readModule finds the one module a task runs, from its keys or an
// action: form, blocking a task with none or several.
func (t *translator) readModule(spec *leafSpec, node *yaml.Node, modules []entry) {
	for _, key := range []string{"action", "local_action"} {
		if a := lookup(node, key); a != nil {
			modules = append(modules, entry{key: key, value: a, keyAt: deref(a)})
		}
	}
	if len(modules) != 1 {
		spec.blocks = append(spec.blocks, t.raise("module.ambiguous", spec.at, spec.name, fmt.Sprintf("the task names %d modules", len(modules))))
		return
	}
	m := modules[0]
	spec.modNode, spec.modAt, spec.module = m.value, nodePos(m.keyAt, t.file), m.key
	if m.key == "action" || m.key == "local_action" {
		t.readAction(spec, m.value)
	}
}

// readAction reads action: as a module name and its arguments: a string
// whose first word is the module, or a map with a module key.
func (t *translator) readAction(spec *leafSpec, v *yaml.Node) {
	v = deref(v)
	switch {
	case v != nil && v.Kind == yaml.ScalarNode:
		module, rest, _ := strings.Cut(strings.TrimSpace(v.Value), " ")
		spec.module = module
		spec.modNode = &yaml.Node{Kind: yaml.ScalarNode, Value: rest, Line: v.Line, Column: v.Column}
	case v != nil && v.Kind == yaml.MappingNode:
		args := &yaml.Node{Kind: yaml.MappingNode, Line: v.Line, Column: v.Column}
		entries, _ := mapEntries(v)
		for _, e := range entries {
			if e.key == "module" {
				spec.module = deref(e.value).Value
				continue
			}
			args.Content = append(args.Content, e.keyAt, e.value)
		}
		spec.modNode = args
	default:
		spec.module = ""
	}
	if spec.module == "" {
		spec.blocks = append(spec.blocks, t.raise("module.ambiguous", spec.modAt, spec.name, "action names no module"))
	}
}

// literalFalse reports whether v is written as false: a YAML 1.1 false,
// zero, or empty. A template is not false: it may resolve either way.
func literalFalse(v *yaml.Node) bool {
	v = deref(v)
	if v == nil {
		return true
	}
	if v.Kind != yaml.ScalarNode || hasTemplate(v.Value) {
		return false
	}
	s := readScalar(v)
	return (s.kind == "bool" && !s.b) || (s.kind == "int" && s.i == 0) || s.kind == "null" || (s.quoted && s.text == "")
}

// readTags reads a tags value: a list or a comma-separated string, with
// no template and none of the filter-only names.
func readTags(v *yaml.Node) ([]string, bool) {
	var raw []string
	switch {
	case v == nil:
		return nil, true
	case v.Kind == yaml.ScalarNode:
		raw = strings.Split(v.Value, ",")
	case v.Kind == yaml.SequenceNode:
		for _, item := range v.Content {
			if item = deref(item); item.Kind != yaml.ScalarNode {
				return nil, false
			}
			raw = append(raw, item.Value)
		}
	default:
		return nil, false
	}
	var tags []string
	for _, tag := range raw {
		tag = strings.TrimSpace(tag)
		switch tag {
		case "":
			continue
		case "all", "tagged", "untagged":
			return nil, false
		}
		if hasTemplate(tag) || termsafe.Check(tag) != nil {
			return nil, false
		}
		tags = append(tags, tag)
	}
	return tags, true
}

// isLocalHost reports whether v names the Ansible controller itself,
// literally: a templated host could resolve anywhere.
func isLocalHost(v *yaml.Node) bool {
	v = deref(v)
	if v == nil || v.Kind != yaml.ScalarNode || hasTemplate(v.Value) {
		return false
	}
	switch v.Value {
	case "localhost", "127.0.0.1", "::1":
		return true
	}
	return false
}

// localRun decides a task written to run on the Ansible controller, and
// reports whether that blocks it. Every native method the task can become
// must run in the host process (collection.SiteController), which is what
// running on the controller means here, and the marker is then dropped
// with a finding, since the method's execution context already says it.
// A method that runs on the device blocks the task, naming the method.
func (t *translator) localRun(entry *Entry, spec *leafSpec) bool {
	if spec.local.key == "" {
		return false
	}
	code := Code("keyword.local_action")
	if spec.local.key == "delegate_to: localhost" {
		code = "keyword.delegate_to"
	}
	for _, fqcn := range entryMethods(entry) {
		desc, ok := collection.Lookup(fqcn)
		if !ok || desc.Manifest.ExecutionContext.Site != collection.SiteController {
			spec.blocks = append(spec.blocks, t.raise(code, spec.local.at, spec.name,
				fmt.Sprintf("%s runs the task on the controller, and %s runs on the device", spec.local.key, fqcn)))
			return true
		}
	}
	spec.cites = append(spec.cites, t.raise("keyword.local_satisfied", spec.local.at, spec.name,
		spec.local.key+" dropped: the native method already runs in the host process"))
	return false
}

// entryMethods returns every native method entry can become, its default
// call and each selector choice's, once each.
func entryMethods(entry *Entry) []string {
	var out []string
	add := func(c *Call) {
		if c != nil && !slices.Contains(out, c.FQCN) {
			out = append(out, c.FQCN)
		}
	}
	add(entry.Default)
	for _, sel := range entry.Selectors {
		for _, choice := range sel.Choices {
			add(choice.Call)
		}
	}
	return out
}

// Package playbook: translating a task that runs one module.
package playbook

import (
	"fmt"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/Subject-Void-LLC/the-pleiades/internal/termsafe"
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
	// blocks are finding IDs that stop the task; cites are findings its
	// comment names without stopping it.
	blocks, cites []string
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
	case "connection":
		if v != nil && v.Value == "local" {
			spec.blocks = append(spec.blocks, t.raise("keyword.local_action", at, spec.name, "connection: local runs the task on the controller"))
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

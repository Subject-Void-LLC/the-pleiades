// Package playbook: translating a block, with its rescue and always.
package playbook

import (
	"slices"

	"go.yaml.in/yaml/v3"
)

// translateBlock translates a block task.
func (t *translator) translateBlock(node *yaml.Node, entries []entry, name string, ctx taskCtx) []*outTask {
	at := nodePos(node, t.file)
	inner := ctx
	inner.depth++
	inner.whens = slices.Clone(ctx.whens)
	inner.blockers = slices.Clone(ctx.blockers)
	var tags, cites []string
	var rescue, always entry
	for _, e := range entries {
		keyAt := nodePos(e.keyAt, t.file)
		rule, known := taskKeywords[e.key]
		switch {
		case e.key == "block" || e.key == "name" || rule.handling == kwScope:
		case e.key == "rescue":
			rescue = e
		case e.key == "always":
			always = e
		case e.key == "when":
			whens, blocked, extra := t.whens(nil, whenNodes(e.value), nil, name)
			cites = append(cites, extra...)
			if blocked != "" {
				inner.blockers = append(inner.blockers, blocked)
			}
			inner.whens = append(inner.whens, whens...)
		case e.key == "tags":
			if v, ok := readTags(deref(e.value)); ok {
				tags = v
			} else {
				inner.blockers = append(inner.blockers, t.raise("keyword.tags", keyAt, name, "the block's tags are templated or name a filter-only tag"))
			}
		case e.key == "check_mode":
			if v := deref(e.value); v != nil && readScalar(v).kind == "bool" && readScalar(v).b {
				inner.checkMode = true
			} else if !literalFalse(e.value) {
				inner.blockers = append(inner.blockers, t.raise("keyword.check_mode", keyAt, name, "check_mode on a block is not a literal true"))
			}
		case known && rule.handling == kwDrop:
			if !literalFalse(e.value) {
				cites = append(cites, t.raise(rule.code, keyAt, name, e.key+" dropped from a block and every task in it"))
			}
		case isDelegation(e.key):
			// A task decides delegate_to: localhost by its own native
			// method (localRun); a block's applies to tasks with different
			// methods, so it still blocks them, as it always did.
			if !literalFalse(e.value) {
				inner.blockers = append(inner.blockers, t.raise(delegationCode(e.key), keyAt, name, e.key+" on a block applies to every task in it"))
			}
		case known && rule.handling == kwBlock:
			if !literalFalse(e.value) {
				inner.blockers = append(inner.blockers, t.raise(rule.code, keyAt, name, e.key+" on a block applies to every task in it"))
			}
		default:
			inner.blockers = append(inner.blockers, t.raise("module.ambiguous", keyAt, name, "a block also names "+e.key))
		}
	}
	out := &outTask{name: name, tags: tags, cites: cites, at: at}
	out.block = t.translateTasks(lookup(node, "block"), inner)
	if rescue.key != "" {
		out.cites = append(out.cites, t.raise("block.rescue", nodePos(rescue.keyAt, t.file), name, "rescue converted but not yet run by the engine"))
		out.rescue = t.translateTasks(rescue.value, inner)
	}
	if always.key != "" {
		out.cites = append(out.cites, t.raise("block.always", nodePos(always.keyAt, t.file), name, "always converted but not yet run by the engine"))
		out.always = t.translateTasks(always.value, inner)
	}
	if len(out.block) == 0 {
		// Every task inside was dropped (a block of debug tasks): a native
		// block must hold a task, so the block goes, and its rescue and
		// always tasks with it, since nothing is left for them to guard.
		return nil
	}
	return []*outTask{out}
}

// whenNodes returns a when value's conditions: a list, or one scalar.
func whenNodes(v *yaml.Node) []*yaml.Node {
	if v = deref(v); v != nil && v.Kind == yaml.SequenceNode {
		return v.Content
	}
	return []*yaml.Node{v}
}

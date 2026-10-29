// Package playbook: the engine actions a runbook has natively: set_fact,
// assert and fail, and the keywords a block or a play puts on every task in
// it.
package playbook

import (
	"fmt"
	"slices"

	"go.yaml.in/yaml/v3"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/redact"
)

// actionArgs reads an engine action's arguments: a map as written, or the
// key=value text of a free-form call.
func actionArgs(spec leafSpec) ([]entry, bool) {
	v := deref(spec.modNode)
	switch {
	case v == nil:
		return nil, true
	case v.Kind == yaml.MappingNode:
		entries, repeated := mapEntries(v)
		return entries, len(repeated) == 0
	case v.Kind == yaml.ScalarNode:
		kv, raw, err := parseKV(v.Value, nil)
		if err != nil || raw != "" {
			return nil, false
		}
		var out []entry
		for k, val := range kv {
			out = append(out, entry{key: k, keyAt: v, value: textNode(val, v)})
		}
		return out, true
	}
	return nil, false
}

// actionBlocked returns the finding that stops an engine action: a keyword
// that applies to a method call only, or a blocker from around it.
func (t *translator) actionBlocked(spec leafSpec, ctx taskCtx) []string {
	ids := append(slices.Clone(ctx.blockers), spec.blocks...)
	k := spec.nk
	reason := ""
	switch {
	case spec.hasLoop:
		reason = "a loop over " + shortName(spec.module) + " has no native form"
	case k.needsResult() || k.retries != nil || k.delay != nil:
		reason = "until, changed_when and failed_when read a method's result, which " + shortName(spec.module) + " has none of"
	case spec.register != "":
		reason = shortName(spec.module) + " registers nothing"
	case spec.timeout > 0:
		reason = "a timeout bounds a method call, and " + shortName(spec.module) + " is not one"
	}
	if reason != "" {
		ids = append(ids, t.raise("assert.unmapped", spec.at, spec.name, reason))
	}
	return ids
}

// actionTask builds the native task for an engine action: its conditions, its
// tags, its ignore_errors and its vars.
func (t *translator) actionTask(spec leafSpec, ctx taskCtx, fqcn string, params map[string]any, cites []string) []*outTask {
	whens, blockedBy, extra := t.whens(ctx.whens, spec.whens, nil, spec.name)
	cites = append(cites, extra...)
	if blockedBy != "" {
		return []*outTask{t.placeholder(spec.name, spec.module, spec.at, ctx, append(cites, blockedBy)...)}
	}
	task := &outTask{name: spec.name, fqcn: fqcn, params: params, when: whens, tags: spec.tags, checkMode: spec.checkMode, at: spec.at}
	task.ignoreErrors = ctx.ignoreErrors
	if spec.nk.ignoreSet {
		task.ignoreErrors = spec.nk.ignore
	}
	if spec.nk.vars != nil {
		vars, blocked := t.nativeVarsMap(spec.nk.vars, scopeTask, spec.name)
		if blocked != "" {
			return []*outTask{t.placeholder(spec.name, spec.module, spec.at, ctx, append(cites, blocked)...)}
		}
		task.vars = vars
	}
	cites = append(cites, t.takeNoted()...)
	task.cites = cites
	t.emitted++
	task.result = &TaskResult{
		At: spec.at, Name: spec.name, Module: spec.module, Runbook: ctx.runbook, FQCN: fqcn,
		ParamKeys: sortedKeys(params), Params: params,
		Class: ClassObserve, ClassBasis: "it evaluates conditions and sets run variables, and reaches no device",
		Outcome: outcomeOf(t, cites), Findings: cites,
	}
	return []*outTask{task}
}

// setFact converts a set_fact. One that is unconditional, written as a map of
// variables each defined once is folded into the places it is read, as it
// always was; any other becomes the set_fact action, whose facts last for the
// run on the device they were set for.
func (t *translator) setFact(spec leafSpec, ctx taskCtx) []*outTask {
	name := spec.name
	cites := slices.Clone(spec.cites)
	args := deref(spec.modNode)
	if args == nil || args.Kind != yaml.MappingNode || spec.hasLoop {
		id := t.raise("set_fact.runtime", spec.modAt, name, "set_fact is looped or is not a map of variables, so its values exist only at run time")
		return []*outTask{t.placeholder(name, spec.module, spec.at, ctx, append(cites, id)...)}
	}
	if ids := t.actionBlocked(spec, ctx); len(ids) > 0 {
		return []*outTask{t.placeholder(name, spec.module, spec.at, ctx, append(ids, cites...)...)}
	}
	entries, repeated := mapEntries(args)
	if len(repeated) > 0 {
		id := t.raise("yaml.duplicate_key", nodePos(repeated[0].keyAt, t.file), name, "key "+repeated[0].key+" is repeated")
		return []*outTask{t.placeholder(name, spec.module, spec.at, ctx, append(cites, id)...)}
	}
	if len(spec.whens) == 0 && len(ctx.whens) == 0 && t.foldableFacts(entries) {
		t.raise("set_fact.resolved", spec.modAt, name, "its variables are read from the playbook where they are used")
		return nil
	}
	params := map[string]any{}
	for _, e := range entries {
		at := nodePos(e.keyAt, t.file)
		switch {
		case e.key == "cacheable":
			cites = append(cites, t.raise("set_fact.cacheable", at, name, "cacheable dropped"))
			continue
		case e.key == engine.SetFactGlobal || e.key == engine.SetFactNoOverride || engine.CheckVarNames([]string{e.key}) != nil || redact.SecretName(e.key):
			id := t.raise("vars.unreadable", at, name, "a fact name the runbook cannot carry")
			return []*outTask{t.placeholder(name, spec.module, spec.at, ctx, append(cites, id)...)}
		case isVault(e.value):
			id := t.raise("vault.value", at, name, "a vault-encrypted value is never copied")
			return []*outTask{t.placeholder(name, spec.module, spec.at, ctx, append(cites, id)...)}
		}
		v, rerr := t.foldOrNative(e.value, nil, "", at)
		if rerr == nil {
			rerr = t.checkRewritten(v, "", "")
		}
		if rerr == nil && secretValue(v) {
			rerr = &resolveError{"template.secret", "a value shaped like a secret is not copied into a runbook"}
		}
		if rerr != nil {
			id := t.raise(rerr.code, at, name, fmt.Sprintf("%s: %s", e.key, rerr.why))
			return []*outTask{t.placeholder(name, spec.module, spec.at, ctx, append(cites, id)...)}
		}
		params[e.key] = v
	}
	if len(params) == 0 {
		id := t.raise("assert.unmapped", spec.modAt, name, "set_fact sets nothing")
		return []*outTask{t.placeholder(name, spec.module, spec.at, ctx, append(cites, id)...)}
	}
	for key := range params {
		t.ix.demote(key)
	}
	return t.actionTask(spec, ctx, engine.SetFactName, params, cites)
}

// foldableFacts reports whether every variable the set_fact sets has this one
// definition, a literal, so reading it anywhere gives the same value.
func (t *translator) foldableFacts(entries []entry) bool {
	for _, e := range entries {
		if len(t.ix.defs[e.key]) != 1 || t.ix.defs[e.key][0].value == nil || e.key == "cacheable" {
			return false
		}
	}
	return t.ix.unknownSource == ""
}

// assertTask converts assert: its that conditions through the one condition
// translator, and its messages as text.
func (t *translator) assertTask(spec leafSpec, ctx taskCtx) []*outTask {
	name := spec.name
	cites := slices.Clone(spec.cites)
	block := func(code Code, at Position, why string) []*outTask {
		return []*outTask{t.placeholder(name, spec.module, spec.at, ctx, append(cites, t.raise(code, at, name, why))...)}
	}
	if ids := t.actionBlocked(spec, ctx); len(ids) > 0 {
		return []*outTask{t.placeholder(name, spec.module, spec.at, ctx, append(ids, cites...)...)}
	}
	entries, ok := actionArgs(spec)
	if !ok {
		return block("args.unparsable", spec.modAt, "assert takes a map of arguments")
	}
	var that []*yaml.Node
	params := map[string]any{}
	for _, e := range entries {
		at := nodePos(e.keyAt, t.file)
		switch e.key {
		case "that":
			that = whenNodes(e.value)
		case "fail_msg", "msg", "success_msg":
			key := e.key
			if key == "msg" {
				key = "fail_msg"
			}
			text, rerr := t.messageText(e.value, at)
			if rerr != nil {
				return block(rerr.code, at, e.key+": "+rerr.why)
			}
			params[key] = text
		case "quiet":
			cites = append(cites, t.raise("assert.quiet", at, name, "quiet ignored"))
		default:
			return block("assert.unmapped", at, "assert has no argument "+e.key+" here")
		}
	}
	if len(that) == 0 {
		return block("assert.unmapped", spec.modAt, "assert gives no that")
	}
	conds, blockedBy, extra := t.whens(nil, that, nil, name)
	cites = append(cites, extra...)
	if blockedBy != "" {
		return []*outTask{t.placeholder(name, spec.module, spec.at, ctx, append(cites, blockedBy)...)}
	}
	if len(conds) == 0 {
		conds = []string{"true"}
	}
	list := make([]any, len(conds))
	for i, c := range conds {
		list[i] = c
	}
	params["that"] = list
	return t.actionTask(spec, ctx, engine.AssertName, params, cites)
}

// failTask converts fail.
func (t *translator) failTask(spec leafSpec, ctx taskCtx) []*outTask {
	name := spec.name
	cites := slices.Clone(spec.cites)
	block := func(code Code, at Position, why string) []*outTask {
		return []*outTask{t.placeholder(name, spec.module, spec.at, ctx, append(cites, t.raise(code, at, name, why))...)}
	}
	if ids := t.actionBlocked(spec, ctx); len(ids) > 0 {
		return []*outTask{t.placeholder(name, spec.module, spec.at, ctx, append(ids, cites...)...)}
	}
	entries, ok := actionArgs(spec)
	if !ok {
		return block("args.unparsable", spec.modAt, "fail takes a map of arguments")
	}
	params := map[string]any{}
	for _, e := range entries {
		at := nodePos(e.keyAt, t.file)
		if e.key != "msg" {
			return block("assert.unmapped", at, "fail has no argument "+e.key+" here")
		}
		text, rerr := t.messageText(e.value, at)
		if rerr != nil {
			return block(rerr.code, at, "msg: "+rerr.why)
		}
		params["msg"] = text
	}
	return t.actionTask(spec, ctx, engine.FailName, params, cites)
}

// messageText reads a message: text, a template folded or rewritten.
func (t *translator) messageText(n *yaml.Node, at Position) (string, *resolveError) {
	n = deref(n)
	if n == nil || n.Kind != yaml.ScalarNode {
		return "", &resolveError{"args.value", "a message that is not one text"}
	}
	v, rerr := t.foldOrNative(n, nil, "", at)
	if rerr == nil {
		rerr = t.checkRewritten(v, "", "")
	}
	if rerr != nil {
		return "", rerr
	}
	s, ok := v.(string)
	if !ok {
		s = fmt.Sprint(v)
	}
	return s, nil
}

// scopeKey handles the keywords a block or a play puts on every task inside
// it that the runbook carries natively. It reports whether key was one.
// ignore_errors: true is inherited by the tasks; the keywords that read one
// task's result cannot be shared, and block the tasks.
func (t *translator) scopeKey(key string, value *yaml.Node, at Position, name, where string, ctx *taskCtx, cites *[]string) bool {
	code := Code("")
	switch key {
	case "ignore_errors":
		switch {
		case literalFalse(value):
		case literalTrue(deref(value)):
			ctx.ignoreErrors = true
			*cites = append(*cites, t.raise("keyword.ignore_errors", at, name, "ignore_errors on "+where+" applies to every task in it"))
		default:
			ctx.blockers = append(ctx.blockers, t.raise("keyword.ignore_errors", at, name, "ignore_errors on "+where+" is templated"))
		}
		return true
	case "failed_when":
		if literalFalse(value) {
			ctx.ignoreErrors = true
			*cites = append(*cites, t.raise("keyword.ignore_errors", at, name, "failed_when: false on "+where+" became ignore_errors"))
			return true
		}
		code = "keyword.failed_when"
	case "changed_when":
		code = "keyword.changed_when"
	case "until", "retries", "delay":
		code = "keyword.retries"
	default:
		return false
	}
	ctx.blockers = append(ctx.blockers, t.raise(code, at, name, key+" on "+where+" reads one task's result, so it cannot be shared by every task in it"))
	return true
}

// Package playbook: the task keywords the runbook carries natively
// (ignore_errors, changed_when, failed_when, until, retries, delay, vars) and
// the conditions they hold.
package playbook

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/redact"
)

// nativeKeys are the native-carried keywords one task wrote, read before the
// task's method is known.
type nativeKeys struct {
	// ignore is the task's own ignore_errors, ignoreSet whether it wrote one.
	ignore, ignoreSet bool
	// changed, failed and until are the conditions as written.
	changed, failed, until []*yaml.Node
	// retries and delay are literal counts; nil when not written.
	retries, delay *int
	// vars is the task's vars: map.
	vars *yaml.Node
	// loop is the task's native loop, when it becomes one.
	loop *nativeLoop
	// at holds the position of each keyword by name, for its findings.
	at map[string]Position
}

// needsResult reports whether the task reads its own result.
func (k nativeKeys) needsResult() bool {
	return len(k.changed) > 0 || len(k.failed) > 0 || len(k.until) > 0
}

// readNativeKey reads e when it is a native-carried keyword, reporting
// whether it was one.
func (t *translator) readNativeKey(spec *leafSpec, e entry, at Position) bool {
	v := deref(e.value)
	k := &spec.nk
	if k.at == nil {
		k.at = map[string]Position{}
	}
	k.at[e.key] = at
	switch e.key {
	case "ignore_errors":
		switch {
		case literalFalse(e.value):
			k.ignore, k.ignoreSet = false, true
		case literalTrue(v):
			k.ignore, k.ignoreSet = true, true
			spec.cites = append(spec.cites, t.raise("keyword.ignore_errors", at, spec.name, "ignore_errors converted"))
		default:
			spec.blocks = append(spec.blocks, t.raise("keyword.ignore_errors", at, spec.name, "ignore_errors is templated, so whether it holds is decided at run time"))
		}
	case "failed_when":
		if literalFalse(e.value) {
			// A task that never fails is a task whose failure the run goes on past.
			k.ignore, k.ignoreSet = true, true
			spec.cites = append(spec.cites, t.raise("keyword.ignore_errors", at, spec.name, "failed_when: false became ignore_errors"))
		} else {
			k.failed = whenNodes(e.value)
		}
	case "changed_when":
		k.changed = whenNodes(e.value)
	case "until":
		k.until = whenNodes(e.value)
	case "retries":
		k.retries = t.readCount(spec, e, at, engine.MaxRetries)
	case "delay":
		k.delay = t.readCount(spec, e, at, engine.MaxDelaySeconds)
	case "vars":
		k.vars = v
	default:
		return false
	}
	return true
}

// literalTrue reports whether v is written as true.
func literalTrue(v *yaml.Node) bool {
	v = deref(v)
	if v == nil || v.Kind != yaml.ScalarNode || hasTemplate(v.Value) {
		return false
	}
	s := readScalar(v)
	return s.kind == "bool" && s.b
}

// readCount reads retries or delay: a literal whole number from 0 to max.
func (t *translator) readCount(spec *leafSpec, e entry, at Position, max int) *int {
	v := deref(e.value)
	if v != nil && v.Kind == yaml.ScalarNode && !hasTemplate(v.Value) {
		if n, err := strconv.Atoi(strings.TrimSpace(v.Value)); err == nil && n >= 0 && n <= max {
			return &n
		}
	}
	spec.blocks = append(spec.blocks, t.raise("keyword.retries", at, spec.name, fmt.Sprintf("%s is not a literal whole number from 0 to %d", e.key, max)))
	return nil
}

// ownConditions translates the conditions of key (until, changed_when,
// failed_when) over the task's own result, registered as own, into CEL
// reading result.<own>.<field>. It returns the conditions, and the finding
// that blocks the task when one does not translate.
func (t *translator) ownConditions(key string, nodes []*yaml.Node, own string, p produced, task string) ([]string, string) {
	code := Code("keyword.changed_when")
	switch key {
	case "failed_when":
		code = "keyword.failed_when"
	case "until":
		code = "keyword.retries"
	}
	var out []string
	for _, w := range nodes {
		w = deref(w)
		at := nodePos(w, t.file)
		if w == nil || w.Kind != yaml.ScalarNode {
			return nil, t.raise(code, at, task, key+": a condition that is not text")
		}
		cel, _, cerr := t.conditionOwn(w.Value, at, own, map[string]produced{own: p})
		if cerr != nil {
			return nil, t.raise(code, at, task, key+": "+cerr.why)
		}
		if cel == "" {
			cel = "true"
		}
		out = append(out, cel)
	}
	return out, ""
}

// synthRegister names a register for a task that wrote changed_when or
// failed_when over constants and no register of its own, which the runbook's
// keys need to read.
func (t *translator) synthRegister() string {
	for {
		t.synth++
		name := fmt.Sprintf("forge_result_%d", t.synth)
		if len(t.ix.defs[name]) == 0 {
			return name
		}
	}
}

// applyKeys sets the native keywords on task, the one native task spec
// became through e, and returns the finding that blocks it, if any.
func (t *translator) applyKeys(e *Entry, spec leafSpec, ctx taskCtx, task *outTask) string {
	k := spec.nk
	task.ignoreErrors = ctx.ignoreErrors
	if k.ignoreSet {
		task.ignoreErrors = k.ignore
	}
	if k.vars != nil {
		vars, blocked := t.nativeVarsMap(k.vars, scopeTask, spec.name)
		if blocked != "" {
			return blocked
		}
		task.vars = vars
	}
	if k.retries != nil || k.delay != nil {
		if len(k.until) == 0 {
			// Ansible ignores both without until.
			t.raise("args.ignored", spec.at, spec.name, "retries and delay do nothing without until")
			k.retries, k.delay = nil, nil
		}
	}
	if !k.needsResult() {
		return ""
	}
	own := spec.register
	if own == "" {
		if len(k.until) > 0 {
			return t.raise("keyword.retries", k.at["until"], spec.name, "until reads the task's own result, which needs a register")
		}
		own = t.synthRegister()
	}
	p := produced{fqcn: task.fqcn, returns: e.Returns}
	for _, c := range []struct {
		key   string
		nodes []*yaml.Node
		into  *[]string
	}{{"until", k.until, &task.until}, {"changed_when", k.changed, &task.changedWhen}, {"failed_when", k.failed, &task.failedWhen}} {
		if len(c.nodes) == 0 {
			continue
		}
		conds, blocked := t.ownConditions(c.key, c.nodes, own, p, spec.name)
		if blocked != "" {
			return blocked
		}
		*c.into = conds
	}
	if spec.register == "" {
		task.register = own
	}
	task.retries, task.delay = k.retries, k.delay
	return ""
}

// nativeVarsMap converts a vars: map written at scope into the runbook's,
// refusing a name the runbook cannot read as vars.<name> and a value that is
// or holds a secret. Templates inside a value are folded when they can be and
// rewritten to read vars otherwise.
func (t *translator) nativeVarsMap(n *yaml.Node, scope varScope, task string) (map[string]any, string) {
	n = deref(n)
	if n == nil || n.Kind != yaml.MappingNode {
		return nil, t.raise("vars.unreadable", nodePos(n, t.file), task, string(scope)+" vars is not a map")
	}
	entries, _ := mapEntries(n)
	out := map[string]any{}
	blocked := ""
	for _, e := range entries {
		at := nodePos(e.keyAt, t.file)
		fail := func(why string) {
			if id := t.raise("vars.unreadable", at, task, why); blocked == "" {
				blocked = id
			}
		}
		switch {
		case engine.CheckVarNames([]string{e.key}) != nil:
			fail("a variable name the runbook cannot read as vars.<name>")
			continue
		case redact.SecretName(e.key):
			fail("a variable named like a secret is not copied into a runbook")
			continue
		case isVault(e.value):
			fail("a vault-encrypted value is never copied")
			continue
		}
		v, rerr := t.foldOrNative(e.value, nil, "", at)
		if rerr == nil {
			rerr = t.checkRewritten(v, "", "")
		}
		switch {
		case rerr != nil:
			fail(fmt.Sprintf("%s: %s", e.key, rerr.why))
		case secretValue(v):
			fail("a value shaped like a secret is not copied into a runbook")
		default:
			out[e.key] = v
		}
	}
	return out, blocked
}

// foldOrNative reads a YAML value with its templates folded into the
// playbook's literal values when they can be, and rewritten to read the
// runbook's roots when they cannot (a variable set at several scopes, a
// set_fact, a registered result, the task's loop).
func (t *translator) foldOrNative(n *yaml.Node, names []string, quote string, at Position) (any, *resolveError) {
	if !hasTemplateNode(n) {
		v, err := naturalValue(n)
		if err != nil {
			return nil, &resolveError{"vault.value", err.Error()}
		}
		return v, nil
	}
	if !mentions(n, names) {
		v, rerr := t.goValue(n, nil, at)
		if rerr == nil {
			return v, nil
		}
		if rerr.code != "template.unresolved" {
			return nil, rerr
		}
		nv, nerr := t.nativeNode(n, names, quote, at)
		switch {
		case nerr == nil:
			return nv, nil
		case nerr.code == "template.unresolved":
			return nil, rerr
		}
		return nil, nerr
	}
	return t.nativeNode(n, names, quote, at)
}

// pruneVars drops, from tasks and their blocks, each variable no template
// reads as vars.<name>: its value is already folded wherever it was read, so
// carrying it would only repeat it.
func (t *translator) pruneVars(tasks []*outTask) {
	for _, o := range tasks {
		for name := range o.vars {
			if !t.usedVars[name] {
				delete(o.vars, name)
			}
		}
		if len(o.vars) == 0 {
			o.vars = nil
		}
		t.pruneVars(o.block)
		t.pruneVars(o.rescue)
		t.pruneVars(o.always)
	}
}

// pruned returns vars without the names no template reads.
func (t *translator) pruned(vars map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range vars {
		if t.usedVars[k] {
			out[k] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// flagPrecedence raises vars.precedence for each variable the playbook
// defines at more than one scope, since Ansible and the runbook can pick
// different winners.
func (t *translator) flagPrecedence() {
	names := make([]string, 0, len(t.ix.defs))
	for name := range t.ix.defs {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		defs := t.ix.defs[name]
		scopes := map[varScope]bool{}
		for _, d := range defs {
			if d.scope != "" {
				scopes[d.scope] = true
			}
		}
		if len(scopes) < 2 {
			continue
		}
		list := make([]string, 0, len(scopes))
		for s := range scopes {
			list = append(list, string(s))
		}
		sort.Strings(list)
		why := "Ansible lets a play's vars beat the inventory's group_vars and host_vars; here a device's inventory variable beats the runbook's vars, so check any name the inventory also sets"
		if scopes[scopeFact] && (scopes[scopeBlock] || scopes[scopeTask]) {
			why = "Ansible lets a set_fact beat block and task vars; here the narrower scope wins, so the block's or the task's vars beat the set_fact"
		}
		t.raise("vars.precedence", defs[len(defs)-1].at, "", fmt.Sprintf("%s is set at %s: %s", name, strings.Join(list, ", "), why))
	}
}

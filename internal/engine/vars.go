// Authored variables (Phase 87): vars: at the runbook, block and task level,
// the set_fact action, and the one order they are merged in.
//
// A task reads them as {{ vars.name }} in a parameter and as vars.name in a
// condition. The rule is the one every language has: the same name in two
// scopes is read from the narrower, the local one. From the widest scope to
// the narrowest, a name is taken from:
//
//  1. the runbook's vars:;
//  2. a global set_fact (set_fact with global: true), which assigns in the
//     runbook's own scope, so a later assignment replaces the declaration;
//  3. the device's own vars property (the static inventory's group_vars and
//     host_vars, which the loader folds into it), since a device is narrower
//     than the runbook;
//  4. a set_fact for the device the task runs on;
//  5. a block's vars:, outer block first (propagateVars copies them down);
//  6. the task's own vars:, the most local;
//  7. the run's extra variables, which are not a scope but an explicit
//     override from outside the runbook (a launch's, or pleiades run
//     --extra-vars), and so win over every scope, as they do in Ansible.
//
// Note the difference from Ansible, where a play's vars: beat inventory vars
// and a set_fact beats a task's vars:. Here the narrower scope always wins.
// Only the authored tiers (1, 3, 5 and 6) are rendered; the facts and the
// extra variables are values, so a launch value that holds "{{" stays text.
// Registered results are not in this map; they are read as nodes and result.
//
// No tier carries a secret. The extra variables arrive without the ones a
// bound credential injected (internal/adapters/native's injectedVariables),
// a device's vars property drops any name or value shaped like a secret, and
// a fact is masked wherever its value is already a known secret, as a
// registered result is.
package engine

import (
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"sync"

	"github.com/Subject-Void-LLC/the-pleiades/internal/redact"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// The names of the set_fact action: the bare one a runbook writes and the
// namespaced one, both dispatched identically like set_metadata's.
const (
	SetFactName = "set_fact"
	SetFactFQCN = "pleiades.builtin.set_fact"
)

// DeviceVarsProperty is the device property that holds the variables a device
// brings: its host_vars merged over its groups' group_vars.
const DeviceVarsProperty = "vars"

// hostVarsRoot is the internal key under which a per-device evaluation hands
// the device's own variables to mergedVars. It is removed before anything
// renders, so a template can never read it.
const hostVarsRoot = "\x00hostvars"

// maxVarPasses bounds how many times mergedVars retries a variable whose
// template reads another that was not rendered yet, which is also the longest
// chain of variables reading each other that it resolves.
const maxVarPasses = 16

// factName is what a variable name may be: a letter or underscore, then
// letters, digits and underscores, so {{ vars.name }} always reads it.
var factName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// isSetFact reports whether fqcn is the set_fact action.
func isSetFact(fqcn string) bool { return fqcn == SetFactName || fqcn == SetFactFQCN }

// CheckVarNames reports the first of names that cannot be a variable name.
func CheckVarNames(names []string) error {
	sort.Strings(names)
	for _, n := range names {
		if !factName.MatchString(n) {
			return fmt.Errorf("%q is not a variable name: use letters, digits and underscores, not starting with a digit", n)
		}
	}
	return nil
}

// The two params of set_fact that are not facts, and so the two names no fact
// may have: global makes the facts it sets visible to every device, not only
// the one the task ran for, and no_override keeps the value a fact already
// has, so the first setting stands.
const (
	SetFactGlobal     = "global"
	SetFactNoOverride = "no_override"
)

// factOptions are a set_fact's two flags.
type factOptions struct {
	global, noOverride bool
}

// splitFacts separates a set_fact's params into its facts and its flags,
// holding the facts to variable names, to at least one, and each flag to a
// boolean.
func splitFacts(params map[string]any) (facts map[string]any, opts factOptions, err error) {
	facts = make(map[string]any, len(params))
	names := make([]string, 0, len(params))
	for k, v := range params {
		if k == SetFactGlobal || k == SetFactNoOverride {
			b, ok := v.(bool)
			if !ok {
				return nil, factOptions{}, fmt.Errorf("%s must be true or false, not %v", k, v)
			}
			if k == SetFactGlobal {
				opts.global = b
			} else {
				opts.noOverride = b
			}
			continue
		}
		facts[k] = v
		names = append(names, k)
	}
	if len(facts) == 0 {
		return nil, factOptions{}, fmt.Errorf("set_fact needs at least one variable to set")
	}
	return facts, opts, CheckVarNames(names)
}

// optionsOf returns the flags task, a set_fact, was written with.
func optionsOf(task *Task) factOptions {
	g, _ := task.Params[SetFactGlobal].(bool)
	n, _ := task.Params[SetFactNoOverride].(bool)
	return factOptions{global: g, noOverride: n}
}

// factStore is the facts a run's set_fact tasks have set, by device id, with
// the empty id holding those set for no device.
type factStore struct {
	mu    sync.Mutex
	facts map[string]map[string]any
	// writers remembers, for each global fact, which device set it in which
	// node and to what, so two devices of one task setting it differently is
	// an error rather than whichever finished last.
	writers map[string]globalWrite
}

// globalWrite is one device's setting of a global fact in one node.
type globalWrite struct {
	node, device string
	value        any
}

// newFactStore returns an empty factStore.
func newFactStore() *factStore {
	return &factStore{facts: map[string]map[string]any{}, writers: map[string]globalWrite{}}
}

// set records facts for device. With noOverride a fact the device already has
// keeps its value.
func (f *factStore) set(device string, facts map[string]any, noOverride bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.facts[device] == nil {
		f.facts[device] = map[string]any{}
	}
	for k, v := range facts {
		if _, have := f.facts[device][k]; have && noOverride {
			continue
		}
		f.facts[device][k] = v
	}
}

// setGlobal records facts for every device, as set by device in node. A fact
// another device of the same node already set to a different value is an
// error: which device's value won would depend on which finished first. With
// noOverride a fact set by an earlier node keeps its value.
func (f *factStore) setGlobal(node, device string, facts map[string]any, noOverride bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for k, v := range facts {
		if w, ok := f.writers[k]; ok && w.node == node && w.device != device && !reflect.DeepEqual(w.value, v) {
			return fmt.Errorf("global fact %q was set to different values by devices %q and %q in one task", k, w.device, device)
		}
	}
	if f.facts[""] == nil {
		f.facts[""] = map[string]any{}
	}
	for k, v := range facts {
		if w, have := f.writers[k]; have && noOverride && w.node != node {
			continue
		}
		f.facts[""][k] = v
		f.writers[k] = globalWrite{node: node, device: device, value: v}
	}
	return nil
}

// layers returns the facts set for no device and those set for device, each
// as a new map.
func (f *factStore) layers(device string) (global, local map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	global, local = map[string]any{}, map[string]any{}
	for k, v := range f.facts[""] {
		global[k] = v
	}
	if device != "" {
		for k, v := range f.facts[device] {
			local[k] = v
		}
	}
	return global, local
}

// usesSetFact reports whether any task of def, at any depth, is a set_fact.
func usesSetFact(def WorkflowDef) bool {
	var walk func([]Task) bool
	walk = func(tasks []Task) bool {
		for i := range tasks {
			t := &tasks[i]
			if isSetFact(t.FQCN) {
				return true
			}
			for _, sub := range [][]Task{t.Block, t.Rescue, t.Always, t.Parallel} {
				if walk(sub) {
					return true
				}
			}
		}
		return false
	}
	return walk(def.PreTasks) || walk(def.Tasks) || walk(def.PostTasks)
}

// propagateVars copies every block's (and parallel group's) vars: down onto
// the tasks it holds, a task's own name winning, at any depth, so each node
// carries its whole scope and the executor reads only Task.Vars. It runs
// after resolveImportTasks, like propagateCheckMode.
func propagateVars(def *WorkflowDef) {
	for _, list := range [][]Task{def.PreTasks, def.Tasks, def.PostTasks} {
		propagateVarsInto(list, nil)
	}
}

// propagateVarsInto merges inherited under each task's own vars and recurses.
func propagateVarsInto(tasks []Task, inherited map[string]any) {
	for i := range tasks {
		t := &tasks[i]
		if len(inherited) > 0 {
			merged := make(map[string]any, len(inherited)+len(t.Vars))
			for k, v := range inherited {
				merged[k] = v
			}
			for k, v := range t.Vars {
				merged[k] = v
			}
			t.Vars = merged
		}
		for _, sub := range [][]Task{t.Block, t.Rescue, t.Always, t.Parallel} {
			propagateVarsInto(sub, t.Vars)
		}
	}
}

// deviceVars returns the variables d brings (DeviceVarsProperty), without any
// whose name or text is shaped like a secret, which the template roots never
// carry (deviceRoot holds the same line).
func deviceVars(d inventory.InventoryItem) map[string]any {
	if d == nil {
		return nil
	}
	raw, ok := d.Properties().Raw()[DeviceVarsProperty].(map[string]any)
	if !ok {
		return nil
	}
	out := make(map[string]any, len(raw))
	for k, v := range raw {
		if redact.SecretName(k) {
			continue
		}
		if s, isText := v.(string); isText && redact.SecretShaped(s) {
			continue
		}
		out[k] = v
	}
	return out
}

// varsNeedDevice reports whether task reads vars and the variables it would
// read can differ by device: some task sets facts, or a device it runs on
// brings variables of its own. Such a task is decided and rendered on each
// device, as one reading the device root is.
func (r *run) varsNeedDevice(nodeID string, task *Task) bool {
	reads := false
	if r.dag.Conditions[nodeID] != nil {
		if uses, err := ConditionUsesRoot(task.Conditional, TemplateRootVars); err == nil && uses {
			reads = true
		}
	}
	if !reads && r.x.renderer != nil {
		reads = ParamsReadRoot(r.x.renderer, task.Params, TemplateRootVars)
	}
	if !reads {
		return false
	}
	if r.dag.UsesSetFact {
		return true
	}
	target := TaskTarget(r.dag, task)
	if target == "" || HasTemplate(target) {
		return false
	}
	for _, d := range r.x.resolver.Resolve(target) {
		if len(deviceVars(d)) > 0 {
			return true
		}
	}
	return false
}

// mergedVars returns the variables task sees on the device roots name (or on
// none), every tier in the order the package documents, the authored ones
// rendered. roots is the evaluation's extra roots; its internal host-vars
// entry, when there is one, is consumed.
func (r *run) mergedVars(task *Task, roots map[string]any) (map[string]any, error) {
	extra := r.x.extraVars
	var host map[string]any
	if h, ok := roots[hostVarsRoot].(map[string]any); ok {
		host = h
	}
	deviceID := ""
	if dev, ok := roots[TemplateRootDevice].(map[string]any); ok {
		deviceID, _ = dev["id"].(string)
	}
	globalFacts, deviceFacts := r.facts.layers(deviceID)

	// The common case: nothing authored and nothing set, so the run's extra
	// variables are the whole map, as they were before vars: existed.
	if len(host) == 0 && len(r.dag.Vars) == 0 && len(task.Vars) == 0 && len(globalFacts) == 0 && len(deviceFacts) == 0 {
		if extra == nil {
			return map[string]any{}, nil
		}
		return extra, nil
	}

	// The tiers, widest scope first; the last to name a variable wins, and
	// whether that tier is authored (a template) decides if it is rendered.
	type tier struct {
		vars     map[string]any
		authored bool
	}
	tiers := []tier{
		{r.dag.Vars, true}, {globalFacts, false}, {host, true},
		{deviceFacts, false}, {task.Vars, true}, {extra, false},
	}
	authored := map[string]any{}
	values := map[string]any{}
	for _, t := range tiers {
		for k, v := range t.vars {
			if t.authored {
				authored[k] = v
				delete(values, k)
			} else {
				values[k] = v
				delete(authored, k)
			}
		}
	}

	out := make(map[string]any, len(authored)+len(values))
	for k, v := range values {
		out[k] = v
	}
	pending := map[string]any{}
	for k, v := range authored {
		if HasTemplate(v) {
			pending[k] = v
			continue
		}
		out[k] = v
	}
	if len(pending) == 0 {
		return out, nil
	}
	if r.x.renderer == nil {
		return nil, fmt.Errorf("this run has no template renderer, so a variable holding {{ cannot be rendered")
	}
	base, err := r.templateContext()
	if err != nil {
		return nil, err
	}
	for k, v := range roots {
		if k != hostVarsRoot {
			base[k] = v
		}
	}
	// Each pass renders what can be rendered with what is known; a variable
	// reading one not rendered yet fails that pass and is tried on the next.
	// A pass that renders nothing ends it, naming the first that failed.
	for pass := 0; pass < maxVarPasses && len(pending) > 0; pass++ {
		names := make([]string, 0, len(pending))
		for k := range pending {
			names = append(names, k)
		}
		sort.Strings(names)
		progress := false
		var firstErr error
		firstName := ""
		for _, k := range names {
			base[TemplateRootVars] = out
			rendered, err := renderValue(r.x.renderer, pending[k], base)
			if err != nil {
				if firstErr == nil {
					firstErr, firstName = err, k
				}
				continue
			}
			out[k] = rendered
			delete(pending, k)
			progress = true
		}
		if !progress {
			return nil, fmt.Errorf("variable %s: %w", firstName, firstErr)
		}
	}
	if len(pending) > 0 {
		names := make([]string, 0, len(pending))
		for k := range pending {
			names = append(names, k)
		}
		sort.Strings(names)
		return nil, fmt.Errorf("variables %v read each other in a chain longer than %d, or in a circle", names, maxVarPasses)
	}
	return out, nil
}

// Package validate: the variable scopes a task reads vars.<name> through,
// worked out from the compiled runbook (Phase 87), shared by VarsRule,
// VarNamesRule and VarShadowWarnings.
package validate

import (
	"fmt"
	"regexp"
	"slices"
	"sort"

	"cel.dev/cel-go/cel"
	celast "cel.dev/cel-go/common/ast"
	"cel.dev/cel-go/common/operators"
	"cel.dev/cel-go/common/types"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/redact"
	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// parentID matches the id of a task inside a block, rescue, always or
// parallel list: the part before the suffix is the id of the group holding it
// (the builder's chain.go gives them these ids).
var parentID = regexp.MustCompile(`^(.*)\.(block|rescue|always|parallel)\[\d+\]$`)

// parentTask returns the group task id sits directly inside, or nil.
func parentTask(dag *engine.DAG, id string) *engine.Task {
	m := parentID.FindStringSubmatch(id)
	if m == nil {
		return nil
	}
	return dag.Nodes[m[1]]
}

// isSetFactTask reports whether task is the set_fact action.
func isSetFactTask(task *engine.Task) bool {
	return task.FQCN == engine.SetFactName || task.FQCN == engine.SetFactFQCN
}

// factSite is one set_fact in the runbook: where it is, which variables it
// sets and whether it sets them for every device.
type factSite struct {
	node   string
	names  []string
	global bool
}

// varScopes answers, for a runbook, which variables each task can read from
// which scope. Build one per rule run with newVarScopes.
type varScopes struct {
	world WorldView
	// facts are the runbook's set_fact tasks, by node id.
	facts []factSite
	// reach caches, per set_fact node, the nodes reachable after it.
	reach map[string]map[string]bool
}

// newVarScopes reads world's set_fact tasks.
func newVarScopes(world WorldView) *varScopes {
	s := &varScopes{world: world, reach: map[string]map[string]bool{}}
	ids := make([]string, 0, len(world.DAG.Nodes))
	for id := range world.DAG.Nodes {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		task := world.DAG.Nodes[id]
		if !isSetFactTask(task) {
			continue
		}
		site := factSite{node: id}
		site.global, _ = task.Params[engine.SetFactGlobal].(bool)
		for k := range task.Params {
			if k != engine.SetFactGlobal && k != engine.SetFactNoOverride {
				site.names = append(site.names, k)
			}
		}
		sort.Strings(site.names)
		s.facts = append(s.facts, site)
	}
	return s
}

// precedes reports whether node to is reached after node from along the
// runbook's chain (Adjacency), so a fact set at from exists when to runs.
// Parallel siblings do not precede one another.
func (s *varScopes) precedes(from, to string) bool {
	seen, ok := s.reach[from]
	if !ok {
		seen = map[string]bool{}
		queue := []string{from}
		for len(queue) > 0 {
			cur := queue[0]
			queue = queue[1:]
			for _, edge := range s.world.DAG.Adjacency[cur] {
				if !seen[edge.To] {
					seen[edge.To] = true
					queue = append(queue, edge.To)
				}
			}
		}
		s.reach[from] = seen
	}
	return from != to && seen[to]
}

// deviceVarNames returns the variable names the devices task reaches bring in
// their vars property, secret-named ones left out as the engine leaves them.
// A target rendered from data could be any device, so every device counts.
func (s *varScopes) deviceVarNames(task *engine.Task) map[string]bool {
	var items []inventory.InventoryItem
	if target := engine.TaskTarget(s.world.DAG, task); engine.HasTemplate(target) {
		items = s.world.Items
	} else {
		_, items = s.world.taskDevices(task)
	}
	names := map[string]bool{}
	for _, item := range items {
		vars, _ := item.Properties().Raw()[inventory.VarsProperty].(map[string]any)
		for k := range vars {
			if !redact.SecretName(k) {
				names[k] = true
			}
		}
	}
	return names
}

// scopeKind orders the scopes from widest to narrowest, the order vars.go
// documents; the last to define a name is the one read.
type scopeKind int

const (
	scopeRunbook scopeKind = iota
	scopeGlobalFact
	scopeDevice
	scopeDeviceFact
	scopeBlock
	scopeTask
)

// definition is one scope that defines a name, with the words a message uses.
type definition struct {
	kind scopeKind
	what string
}

// definitions returns every scope of node's chain that defines name, widest
// first, together with the set_fact sites that set it but do not run before
// node. The run's extra variables are not a scope here: they override by
// design and are checked by the caller.
func (s *varScopes) definitions(node string, name string, devices map[string]bool) (defs []definition, late []factSite) {
	dag := s.world.DAG
	task := dag.Nodes[node]
	if _, ok := dag.Vars[name]; ok {
		defs = append(defs, definition{scopeRunbook, "the runbook's vars:"})
	}
	for _, site := range s.facts {
		if !slices.Contains(site.names, name) || !site.global {
			continue
		}
		if s.precedes(site.node, node) {
			defs = append(defs, definition{scopeGlobalFact, fmt.Sprintf("the global set_fact at %s", site.node)})
		} else {
			late = append(late, site)
		}
	}
	if devices[name] {
		defs = append(defs, definition{scopeDevice, "the device's vars (group_vars or host_vars)"})
	}
	for _, site := range s.facts {
		if !slices.Contains(site.names, name) || site.global {
			continue
		}
		if s.precedes(site.node, node) {
			defs = append(defs, definition{scopeDeviceFact, fmt.Sprintf("the set_fact at %s", site.node)})
		} else {
			late = append(late, site)
		}
	}
	if own, ok := task.Vars[name]; ok {
		parent := parentTask(dag, node)
		inherited, fromBlock := map[string]any(nil), false
		if parent != nil {
			inherited = parent.Vars
			_, fromBlock = parent.Vars[name]
		}
		if fromBlock {
			defs = append(defs, definition{scopeBlock, "the enclosing block's vars:"})
			// The task's own vars: replaced the block's value only when it
			// differs; equal values are indistinguishable once copied down.
			if !equalValues(own, inherited[name]) {
				defs = append(defs, definition{scopeTask, "this task's vars:"})
			}
		} else {
			defs = append(defs, definition{scopeTask, "this task's vars:"})
		}
	}
	return defs, late
}

// equalValues reports whether two authored values are the same.
func equalValues(a, b any) bool { return fmt.Sprintf("%#v", a) == fmt.Sprintf("%#v", b) }

// varRead is one place a task reads vars.<name>.
type varRead struct {
	name string
	// where is "parameter <key>" or "condition".
	where string
	// optional is a read guarded by a default filter or tested for presence.
	optional bool
}

// readsOf returns every vars.<name> the call and, for a node's own call, its
// condition read, sorted by place then name. A template that does not compile
// or a condition that does not parse is another rule's to report.
func readsOf(eng render.Engine, pt paramTask) []varRead {
	var reads []varRead
	keys := make([]string, 0, len(pt.task.Params))
	for k := range pt.task.Params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		for _, source := range engine.TemplateStrings(pt.task.Params[key]) {
			tmpl, err := eng.Compile(source)
			if err != nil {
				continue
			}
			for _, expr := range tmpl.Expressions() {
				if len(expr.Path) < 2 || expr.Path[0] != engine.TemplateRootVars {
					continue
				}
				reads = append(reads, varRead{name: expr.Path[1], where: "parameter " + key, optional: slices.Contains(expr.Filters, defaultFilter)})
			}
		}
	}
	if pt.id == pt.node {
		read, tested := conditionVarReads(pt.task.Conditional)
		for _, name := range read {
			reads = append(reads, varRead{name: name, where: "condition"})
		}
		for _, name := range tested {
			reads = append(reads, varRead{name: name, where: "condition", optional: true})
		}
	}
	return reads
}

// conditionVarReads returns the names c reads as vars.<name> (or
// vars["name"]) and the names it only tests for, with has(vars.<name>) or
// "<name>" in vars, sorted. It parses and never evaluates, as
// engine.ConditionUsesRoot does; a condition that does not parse reads
// nothing here.
func conditionVarReads(c engine.Conditional) (read, tested []string) {
	env, err := cel.NewEnv()
	if err != nil {
		return nil, nil
	}
	isVars := func(e celast.Expr) bool {
		return e.Kind() == celast.IdentKind && e.AsIdent() == engine.TemplateRootVars
	}
	readSet, testedSet := map[string]bool{}, map[string]bool{}
	for _, src := range append(append(append([]string(nil), c.When...), c.WhenOr...), c.WhenCEL) {
		if src == "" {
			continue
		}
		ast, iss := env.Parse(src)
		if iss.Err() != nil {
			continue
		}
		celast.PreOrderVisit(ast.NativeRep().Expr(), celast.NewExprVisitor(func(e celast.Expr) {
			switch e.Kind() {
			case celast.SelectKind:
				sel := e.AsSelect()
				if isVars(sel.Operand()) {
					if sel.IsTestOnly() {
						testedSet[sel.FieldName()] = true
					} else {
						readSet[sel.FieldName()] = true
					}
				}
			case celast.CallKind:
				call := e.AsCall()
				args := call.Args()
				switch call.FunctionName() {
				case operators.Index, operators.OptIndex:
					if len(args) == 2 && isVars(args[0]) && args[1].Kind() == celast.LiteralKind {
						if key, ok := args[1].AsLiteral().(types.String); ok {
							readSet[string(key)] = true
						}
					}
				case operators.In:
					if len(args) == 2 && args[0].Kind() == celast.LiteralKind && isVars(args[1]) {
						if key, ok := args[0].AsLiteral().(types.String); ok {
							testedSet[string(key)] = true
						}
					}
				}
			}
		}))
	}
	for n := range readSet {
		read = append(read, n)
	}
	for n := range testedSet {
		tested = append(tested, n)
	}
	sort.Strings(read)
	sort.Strings(tested)
	return read, tested
}

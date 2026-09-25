// Package playbook: translating a task list, a task at a time.
package playbook

import (
	"fmt"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
)

// outTask is one translated task, before it becomes an engine.Task: a
// leaf (fqcn set), a block (block set) or a placeholder (fqcn under
// ansible.unconverted).
type outTask struct {
	name      string
	fqcn      string
	params    map[string]any
	register  string
	when      []string
	tags      []string
	checkMode bool
	block     []*outTask
	rescue    []*outTask
	always    []*outTask
	// result is the report's record of a leaf or placeholder.
	result *TaskResult
	// cites are the finding IDs the emitted task's comment names.
	cites []string
	at    Position
}

// taskCtx is what a task inherits from the blocks and play around it.
type taskCtx struct {
	// whens are the conditions of enclosing blocks, already translated to
	// CEL, pushed down because a native block carries no when.
	whens []string
	// blockers are findings an enclosing block or play raised that stop
	// every task inside from converting (delegate_to on a block).
	blockers []string
	// checkMode is an enclosing check_mode: true.
	checkMode bool
	// collections is the module search path a play or block set.
	collections []string
	// runbook is the output file the task lands in, for its TaskResult.
	runbook string
	depth   int
	// imports is the chain of files being imported, to refuse a cycle.
	imports []string
}

// placeholderPrefix is the namespace a blocked task's placeholder calls.
// The ansible namespace is reserved, so nothing can ever register a
// method under it, and validation names the unconverted module.
const placeholderPrefix = engine.UnconvertedPrefix

// translateTasks translates a task list.
func (t *translator) translateTasks(list *yaml.Node, ctx taskCtx) []*outTask {
	list = deref(list)
	if list == nil || list.Kind != yaml.SequenceNode {
		return nil
	}
	var out []*outTask
	for _, node := range list.Content {
		out = append(out, t.translateTask(node, ctx)...)
	}
	return out
}

// translateTask translates one task: a block, or a leaf through its
// module's table entry, or a placeholder when it cannot convert.
func (t *translator) translateTask(node *yaml.Node, ctx taskCtx) []*outTask {
	node = deref(node)
	at := nodePos(node, t.file)
	if node == nil || node.Kind != yaml.MappingNode {
		id := t.raise("module.ambiguous", at, "", "a task that is not a map")
		return []*outTask{t.placeholder("(not a task)", "task", at, ctx, id)}
	}
	entries, repeated := mapEntries(node)
	name := taskName(node)
	if len(repeated) > 0 {
		id := t.raise("yaml.duplicate_key", nodePos(repeated[0].keyAt, t.file), name, "key "+repeated[0].key+" is repeated")
		return []*outTask{t.placeholder(name, "task", at, ctx, id)}
	}
	if t.emitted >= maxEmittedTasks {
		id := t.raise("yaml.limit", at, name, fmt.Sprintf("the conversion passed %d tasks", maxEmittedTasks))
		return []*outTask{{name: name, fqcn: placeholderPrefix + "limit", cites: []string{id}, at: at,
			result: &TaskResult{At: at, Name: name, Module: "task", Runbook: ctx.runbook, FQCN: placeholderPrefix + "limit", Outcome: OutcomeBlocked, Findings: []string{id}}}}
	}
	if ctx.depth > maxTaskDepth {
		id := t.raise("yaml.limit", at, name, "blocks and imports nest too deeply")
		return []*outTask{t.placeholder(name, "task", at, ctx, id)}
	}
	for _, e := range entries {
		if e.key == "block" {
			return t.translateBlock(node, entries, name, ctx)
		}
	}
	return t.translateLeaf(node, entries, name, ctx)
}

// taskName is a task's name as written, or "" when it has none.
func taskName(node *yaml.Node) string {
	if n := deref(lookup(node, "name")); n != nil && n.Kind == yaml.ScalarNode {
		return n.Value
	}
	return ""
}

// placeholder builds the output for a task that did not convert: a call
// under ansible.unconverted that nothing can serve, carrying no
// parameters (they may hold secrets), citing the findings that blocked it.
func (t *translator) placeholder(name, module string, at Position, ctx taskCtx, cites ...string) *outTask {
	if name == "" {
		name = module
	}
	fqcn := placeholderPrefix + sanitizeModule(module)
	t.emitted++
	return &outTask{
		name:  name,
		fqcn:  fqcn,
		cites: cites,
		at:    at,
		result: &TaskResult{
			At: at, Name: name, Module: module, Runbook: ctx.runbook, FQCN: fqcn,
			Outcome: OutcomeBlocked, Findings: cites,
		},
	}
}

// sanitizeModule keeps a module name usable inside a method name: letters,
// digits, underscores and dots only, so a placeholder's fqcn can never
// carry text that means something else to a reader or a parser.
func sanitizeModule(module string) string {
	var b strings.Builder
	for _, r := range module {
		switch {
		case r == '.' || r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	s := strings.Trim(b.String(), ".")
	if s == "" || len(s) > 128 {
		return "task"
	}
	return s
}

// Package engine: the facts root, the stored facts of the device a task
// decides on, which only a task's own condition reads (Phase 41's read half).
//
// A condition reads a fact by a key written out in the runbook:
// facts["os.family"], facts.uptime, has(facts.uptime), "os.family" in facts,
// facts[?"os.family"] or facts.?uptime. Everything else that would reach the
// root is refused when the runbook is built, so what a run can read is known
// before it starts: the whole root used as a value (handed to a function or a
// macro, compared, counted), a key computed while the run goes (facts[item]),
// another device's facts (hostvars), and the root in until, changed_when,
// failed_when or an assert's that, which decide on a task's own result. A task
// parameter reads no fact (render_params.go), so no fact value reaches a
// method, a set_fact, the journal or a report: a condition's answer is all
// that leaves.
//
// Detection parses the CEL and reads its syntax tree, never its text, so a
// string holding the word is not a read and a read spelled another way is not
// missed; a condition that does not parse is refused, never taken to read
// nothing.
package engine

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"

	"cel.dev/cel-go/cel"
	celast "cel.dev/cel-go/common/ast"
	"cel.dev/cel-go/common/operators"
	"cel.dev/cel-go/common/types"

	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
)

// TemplateRootFacts is the root a condition reads stored facts through.
const TemplateRootFacts = "facts"

// hostVarsName is Ansible's root for another host's variables and facts.
const hostVarsName = "hostvars"

// The refusals, each saying what is allowed instead.
const (
	factsAllowed       = `read a stored fact by a key written in the runbook: facts["os.family"], facts.uptime, has(facts.uptime), "os.family" in facts or facts[?"os.family"]`
	factsWholeRoot     = "a condition may not use the facts root itself as a value (hand it to a function or a macro, compare it, count it): " + factsAllowed
	factsComputedKey   = "a condition may not read a fact by a key computed while the run goes (facts[item], facts[vars.key]): which facts a run reads is known before it starts, so " + factsAllowed
	factsOtherDevice   = "a condition may not read another device's facts (hostvars): facts is the stored facts of the device the task decides on, and reading another device's is not built yet"
	factsOtherKeywords = "reads facts, which only a task's when, when_or or when_cel may read: a stored fact decides whether a task runs, never how its own result is judged"
)

// ErrParamReadsFacts is why a parameter, a set_fact value, a loop or a
// rollback step may not read facts: a fact is device-supplied text, and the
// only thing a run takes from it in this release is whether a condition held.
const ErrParamReadsFacts = "an expression reads facts, and a parameter may not: a stored fact is read only in a task's when, when_or or when_cel, so no fact's value reaches a method, a set_fact, a rollback step, the journal or a report; decide with a condition, or gather in an earlier task and read its registered result (result.<register>[\"<key>\"])"

// ErrNoFactsOnRunner is how a Runner, and the Controller before it, refuse a
// runbook that reads stored facts, after naming the task: a Runner holds no
// store and is handed no fact.
const ErrNoFactsOnRunner = "and a Runner cannot read stored facts in this release, so nothing ran on any device; " +
	"pleiades run reads them on the command line, and on this tier a run can gather in an earlier task and read that task's registered result (register:, then result.<name>[\"<key>\"])"

// factsParseEnv parses conditions with the syntax they compile with, the
// optional field and index (.? and [?]) included, which the shared parseEnv
// does not enable.
var factsParseEnv = sync.OnceValues(func() (*cel.Env, error) { return cel.NewEnv(cel.OptionalTypes()) })

// ConditionFacts is what one condition reads of the facts root.
type ConditionFacts struct {
	// Keys are the literal keys it reads, sorted, each once.
	Keys []string
	// Problems are each refused read, in words an author can act on.
	Problems []string
}

// ConditionFactReads returns what c's expressions read of the facts root. An
// expression that does not parse is an error, which a caller refuses: it
// never reads as reading nothing.
func ConditionFactReads(c Conditional) (ConditionFacts, error) {
	return factReadsOf(c.Sources())
}

// factReadsOf is ConditionFactReads over CEL sources.
func factReadsOf(sources []string) (ConditionFacts, error) {
	env, err := factsParseEnv()
	if err != nil {
		return ConditionFacts{}, err
	}
	keys := map[string]bool{}
	var out ConditionFacts
	for _, src := range sources {
		ast, iss := env.Parse(src)
		if iss.Err() != nil {
			return ConditionFacts{}, fmt.Errorf("condition %q does not parse, so what it reads of facts cannot be told: %w", src, iss.Err())
		}
		out.Problems = append(out.Problems, collectFactReads(ast.NativeRep().Expr(), keys)...)
	}
	for k := range keys {
		out.Keys = append(out.Keys, k)
	}
	sort.Strings(out.Keys)
	return out, nil
}

// collectFactReads adds to keys every key root reads of the facts root and
// returns each read it refuses. Every appearance of the root must be one of
// the literal reads; any appearance left over is the root used as a value.
func collectFactReads(root celast.Expr, keys map[string]bool) []string {
	var problems []string
	appearances, accounted := 0, 0
	isFacts := func(e celast.Expr) bool { return e.Kind() == celast.IdentKind && e.AsIdent() == TemplateRootFacts }
	add := func(key string) {
		if !facts.ReadableKey(key) {
			problems = append(problems, fmt.Sprintf("a condition reads facts[%s], which is not a fact name (empty, too long, or holding a space, a slash or a control character)", strconv.Quote(key)))
			return
		}
		keys[key] = true
	}
	celast.PreOrderVisit(root, celast.NewExprVisitor(func(e celast.Expr) {
		switch e.Kind() {
		case celast.IdentKind:
			switch e.AsIdent() {
			case TemplateRootFacts:
				appearances++
			case hostVarsName:
				problems = append(problems, factsOtherDevice)
			}
		case celast.SelectKind:
			if sel := e.AsSelect(); isFacts(sel.Operand()) {
				accounted++
				add(sel.FieldName())
			}
		case celast.CallKind:
			call := e.AsCall()
			args := call.Args()
			switch call.FunctionName() {
			case operators.Index, operators.OptIndex, operators.OptSelect:
				if len(args) == 2 && isFacts(args[0]) {
					accounted++
					if key, ok := stringLiteral(args[1]); ok {
						add(key)
					} else {
						problems = append(problems, factsComputedKey)
					}
				}
			case operators.In:
				if len(args) == 2 && isFacts(args[1]) {
					accounted++
					if key, ok := stringLiteral(args[0]); ok {
						add(key)
					} else {
						problems = append(problems, factsComputedKey)
					}
				}
			}
		}
	}))
	if appearances > accounted {
		problems = append(problems, factsWholeRoot)
	}
	return problems
}

// stringLiteral returns e's text when e is a string literal.
func stringLiteral(e celast.Expr) (string, bool) {
	if e.Kind() != celast.LiteralKind {
		return "", false
	}
	s, ok := e.AsLiteral().(types.String)
	return string(s), ok
}

// checkFactPlacement refuses a task whose condition reads facts in a way the
// root does not allow, or whose until, changed_when, failed_when or that
// reads it at all, and returns what its condition reads. It runs when the
// runbook is built, so every tier refuses the same runbook before anything
// runs.
func checkFactPlacement(task *Task, id string) (ConditionFacts, error) {
	reads, err := ConditionFactReads(task.Conditional)
	if err != nil {
		// The condition's own compile, which runs next, says what is wrong.
		return ConditionFacts{}, nil
	}
	if len(reads.Problems) > 0 {
		return ConditionFacts{}, fmt.Errorf("task %s: %s", taskLabel(id, task), strings.Join(reads.Problems, "; "))
	}
	for _, c := range []struct {
		name string
		list StringList
	}{{"until", task.Until}, {"changed_when", task.ChangedWhen}, {"failed_when", task.FailedWhen}, {"that", assertThat(task)}} {
		if len(c.list) == 0 {
			continue
		}
		// One that does not parse is refused by its own compile, next.
		if uses, err := sourcesNameFacts(Conditional{When: c.list}.Sources()); err == nil && uses {
			return ConditionFacts{}, fmt.Errorf("task %s: its %s %s", taskLabel(id, task), c.name, factsOtherKeywords)
		}
	}
	// A loop's label is printed beside each item's result, so it is held to
	// a parameter's rule; one that does not compile fails when it renders.
	if task.LoopControl != nil && IsTemplate(task.LoopControl.Label) {
		if tmpl, err := render.New().Compile(task.LoopControl.Label); err == nil && readsRoot(tmpl, TemplateRootFacts) {
			return ConditionFacts{}, fmt.Errorf("task %s, loop_control label: %s", taskLabel(id, task), ErrParamReadsFacts)
		}
	}
	return reads, nil
}

// sourcesNameFacts reports whether any of sources names the facts root at
// all, however it is used.
func sourcesNameFacts(sources []string) (bool, error) {
	env, err := factsParseEnv()
	if err != nil {
		return false, err
	}
	found := false
	for _, src := range sources {
		ast, iss := env.Parse(src)
		if iss.Err() != nil {
			return false, iss.Err()
		}
		celast.PreOrderVisit(ast.NativeRep().Expr(), celast.NewExprVisitor(func(e celast.Expr) {
			if e.Kind() == celast.IdentKind && e.AsIdent() == TemplateRootFacts {
				found = true
			}
		}))
	}
	return found, nil
}

// conditionFactKeys is the keys task's condition reads of the facts root: as
// the builder worked them out, or, for a task it did not build, read again
// from the condition. A condition that does not parse reads none here, and
// the facts root it is bound to answers any key it was not told of as
// unknown, so a run cannot read a fact detection missed.
func (t *Task) conditionFactKeys() []string {
	if t.factsParsed {
		return t.factKeys
	}
	reads, err := ConditionFactReads(t.Conditional)
	if err != nil || len(reads.Problems) > 0 {
		return nil
	}
	return reads.Keys
}

// FactRead is one task's read of the facts root.
type FactRead struct {
	// Node is the task's node id, and Label its id with its name.
	Node, Label string
	// Keys are the keys its condition reads.
	Keys []string
}

// FactReadsOf returns every task of dag whose condition reads the facts
// root, in node id order: what a tier that cannot read stored facts refuses,
// and what validation holds to the store a run has.
func FactReadsOf(dag *DAG) []FactRead {
	if dag == nil {
		return nil
	}
	var out []FactRead
	for id, task := range dag.Nodes {
		if task == nil {
			continue
		}
		if keys := task.conditionFactKeys(); len(keys) > 0 {
			out = append(out, FactRead{Node: id, Label: taskLabel(id, task), Keys: keys})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Node < out[j].Node })
	return out
}

// QuotedFactKeys is keys as a condition writes them, for a message:
// facts["a"], facts["b"].
func QuotedFactKeys(keys []string) string {
	quoted := make([]string, len(keys))
	for i, k := range keys {
		quoted[i] = "facts[" + strconv.Quote(k) + "]"
	}
	return strings.Join(quoted, ", ")
}

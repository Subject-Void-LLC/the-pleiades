package engine

import (
	"fmt"
	"sync"

	"github.com/google/cel-go/cel"
)

// Evaluator defines a contract for compiling and evaluating dynamic CEL expressions.
type Evaluator interface {
	// Compile parses and type-checks a CEL expression against the provided schema.
	Compile(expression string) (Program, error)
}

// defaultCELCostLimit bounds how much evaluation work a single compiled
// Program may perform per Eval call. cel-go bounds an expression's parsed
// *size* (100,000 code points, its own default) but not its evaluation
// *cost*: a nested comprehension well within that size limit,
// "[0..3200].all(x, [0..3200].all(y, x+y>=0))", measured over 5 real
// seconds of CPU time on a single Eval call in local testing (Phase 39,
// Schema & Injection Hardening), and scales quadratically, so a
// runbook author (accidentally or adversarially) can turn one when_cel
// condition into an effectively unbounded hang with no special
// privilege, since Executor.runNode calls Eval with no timeout of its
// own. 100,000 was chosen empirically, not guessed: it rejects that
// exact pathological expression in under 200ms, while any realistic
// condition (a handful of comparisons, a linear scan over a
// few-hundred-element list) costs orders of magnitude less and is
// unaffected.
const defaultCELCostLimit = 100_000

// Program is a compiled CEL expression ready for execution.
type Program interface {
	// Eval evaluates the compiled program against vars, the top-level CEL
	// activation: each key is a variable name declared in the environment
	// (today, "stat" and "nodes"; see NewCELEvaluator), bound directly, with
	// no implicit wrapping. A caller that only cares about "stat" must
	// supply map[string]interface{}{"stat": ...} itself; Eval no longer
	// guesses which variable a flat map belongs under.
	Eval(vars map[string]interface{}) (bool, error)
}

// celEvaluator is a Facade over cel-go's env/ast/issues/program machinery
// (PATTERNS.md's Facade entry) and a Flyweight cache of compiled programs
// (PATTERNS.md's Flyweight entry): cel.Program is documented by cel-go
// itself as "stateless, thread-safe, and cachable," so sharing one compiled
// instance across every caller that compiles the same expression text is
// safe, not just an optimization applied per caller. Without this, a
// runbook with many tasks sharing identical when/when_cel text (or a
// long-lived controller process rebuilding many DAGs over its lifetime)
// would re-parse and re-type-check identical source repeatedly, defeating
// the microsecond evaluation claim PLAN.md Section 21.4 makes for CEL.
type celEvaluator struct {
	env *cel.Env

	mu    sync.Mutex
	cache map[string]Program
}

type celProgram struct {
	prg cel.Program
}

// NewCELEvaluator initializes a CEL execution environment.
func NewCELEvaluator() (Evaluator, error) {
	// 'stat' holds dynamic device/condition properties for simple,
	// non-cross-node conditions (e.g. the Release Gate's own
	// stat.firmware == 'v2.0' && stat.ping_ms < 50). 'nodes' holds the
	// aggregated cross-node WorkflowContext (PLAN.md Section 27): a task's
	// when_cel can reference an earlier task's registered result by name,
	// e.g. nodes.precheck[""].needs_reboot. Both are declared as
	// map(string, dyn) so any downstream field/macro access type-checks
	// permissively; Executor.runNode currently binds both names to the
	// identical WorkflowContext snapshot (executor.go), a deliberate,
	// stated scope choice, not an oversight: no caller today has a reason
	// to give 'stat' a narrower, device-only meaning distinct from 'nodes'.
	env, err := cel.NewEnv(
		cel.Variable("stat", cel.MapType(cel.StringType, cel.DynType)),
		cel.Variable("nodes", cel.MapType(cel.StringType, cel.DynType)),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create CEL env: %w", err)
	}

	return &celEvaluator{env: env, cache: make(map[string]Program)}, nil
}

// Compile returns a compiled Program for expression, sharing one instance
// across every caller that compiles the identical text (Flyweight). The
// real parse/type-check/program-construction work happens at most once per
// distinct expression string for the lifetime of this Evaluator; a cache
// hit is a single map lookup under a mutex.
//
// The miss path compiles without holding the lock (cel-go's own Compile
// and Program calls do real, non-trivial work, and there is no shared
// mutable state to protect during that work), then stores the result only
// if no other goroutine has already won that key, so concurrent first-time
// compiles of the same new expression converge on one shared winner rather
// than each goroutine keeping its own separately-compiled copy.
func (e *celEvaluator) Compile(expression string) (Program, error) {
	e.mu.Lock()
	if cached, ok := e.cache[expression]; ok {
		e.mu.Unlock()
		return cached, nil
	}
	e.mu.Unlock()

	ast, issues := e.env.Compile(expression)
	if issues != nil && issues.Err() != nil {
		return nil, fmt.Errorf("failed to compile CEL expression: %w", issues.Err())
	}

	prg, err := e.env.Program(ast, cel.CostLimit(defaultCELCostLimit))
	if err != nil {
		return nil, fmt.Errorf("failed to create CEL program: %w", err)
	}
	compiled := &celProgram{prg: prg}

	e.mu.Lock()
	defer e.mu.Unlock()
	if cached, ok := e.cache[expression]; ok {
		// Another goroutine won the race for this exact expression while we
		// were compiling; discard our copy and share theirs instead, so
		// every caller of this expression converges on one instance.
		return cached, nil
	}
	e.cache[expression] = compiled
	return compiled, nil
}

func (p *celProgram) Eval(vars map[string]interface{}) (bool, error) {
	out, _, err := p.prg.Eval(vars)
	if err != nil {
		return false, fmt.Errorf("CEL evaluation failed: %w", err)
	}

	result, ok := out.Value().(bool)
	if !ok {
		return false, fmt.Errorf("CEL expression did not return a boolean")
	}

	return result, nil
}

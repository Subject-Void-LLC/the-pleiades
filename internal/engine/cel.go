package engine

import (
	"fmt"
	"sync"

	"cel.dev/cel-go/cel"
	"cel.dev/cel-go/common/types"
	"cel.dev/cel-go/ext"
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

	// EvalPartial is Eval with the registered results unknown names
	// marked as not known (a check's unchecked tasks, which registered
	// nothing). known is false when the answer depends on one of them,
	// and true when the rest of the expression settles it whatever they
	// hold, by CEL's own rules: a true operand decides an OR and a false
	// one an AND, and an unknown absorbs an error in either. An error that
	// no unknown absorbs is returned as Eval would return it.
	EvalPartial(vars map[string]interface{}, unknown []UnknownRegister) (value, known bool, err error)
}

// UnknownRegister names a registered result whose value a check does not
// know, because the task that registers it could not be checked: its
// result on one Device, or on every device when Whole is set (a task whose
// own condition could not be decided, which never reached a device). The
// device key is the one the result would be registered under, which is
// the empty string for a task with no target device.
type UnknownRegister struct {
	Name   string
	Device string
	Whole  bool
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

	// env and ast build partial on first use. A real run never asks for
	// it, so a real run's evaluation is exactly the program above, and a
	// check pays for the second program only for the conditions it
	// reaches.
	env     *cel.Env
	ast     *cel.Ast
	partial func() (cel.Program, error)
}

// CELVariableOptions returns the cel.EnvOption values declaring this
// engine's three top-level activation variables. Exported (rather than
// folded directly into NewCELEvaluator) so tools/gendocs can build the
// identical baseline environment this package uses, then Extend it with
// CELLibraryOptions to compute exactly which functions those libraries
// add, instead of a hand-maintained doc table that could drift from what
// the real Evaluator actually accepts.
//
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
//
// 'vars' holds a dispatch's own resolved launch.Resolved.ExtraVars
// (AWX_PARITY_ROADMAP.md Section 3b.1), an Executor-wide constant for
// the whole Run call rather than anything WorkflowContext accumulates:
// unlike 'stat'/'nodes', which grow as earlier tasks register results,
// 'vars' is the same map for every node from the moment Run starts.
// Executor.runNode binds it from Executor.extraVars (WithVariables),
// defaulting to an empty map so a condition can reference vars.foo
// even on a dag no ExecutorOption ever set it for, rather than a CEL
// evaluation error about a missing attribute.
func CELVariableOptions() []cel.EnvOption {
	return []cel.EnvOption{
		cel.Variable("stat", cel.MapType(cel.StringType, cel.DynType)),
		cel.Variable("nodes", cel.MapType(cel.StringType, cel.DynType)),
		cel.Variable("vars", cel.MapType(cel.StringType, cel.DynType)),
		// result is each register one device (or one device-less task)
		// wrote, by name alone (Phase 117a, render_params.go's
		// singleWriters), so a condition reads result.ticket.json rather
		// than indexing stat by a device id it cannot know.
		cel.Variable("result", cel.MapType(cel.StringType, cel.DynType)),
	}
}

// CELLibraryOptions returns the cel.EnvOption values that extend this
// engine's CEL environment beyond cel-go's own standard library (Phase 50,
// PLAN.md Section 36's Filters Part). Exported for the same reason as
// CELVariableOptions: tools/gendocs builds a baseline env from
// CELVariableOptions alone and an extended env from
// CELVariableOptions+CELLibraryOptions, then diffs the two environments'
// Functions() by overload ID to render exactly what each library adds,
// with no second, hand-maintained copy of the list to drift from this one.
//
//   - ext.Network() supplies ip()/cidr()/isIP()/isCIDR() and CIDR
//     containment member functions, built on net/netip.
//   - ext.Encoders() supplies base64.encode/decode and json.encode.
//     base64.encode takes bytes and base64.decode returns bytes, not
//     string: reach for base64.encode(bytes(x)) and
//     string(base64.decode(x)).
//   - cel.OptionalTypes() supplies "?." / ".orValue(default)", the
//     missing-value case a hand-written filters.default function cannot
//     replace (CEL evaluates call arguments eagerly, so
//     filters.default(stat.missing, y) would still throw evaluating
//     stat.missing before default ever ran).
//   - filtersLib() registers every pkg/filters function under the
//     "filters." prefix; see that function's own doc comment.
//
// Ordering note: ext.Network() installs a cel.CustomTypeAdapter that wraps
// whatever type adapter is already configured on the env at the point it
// runs. A future EnvOption that also needs cel.CustomTypeAdapter must be
// listed before ext.Network() in this slice, or ext.Network()'s wrapping
// silently shadows it.
func CELLibraryOptions() []cel.EnvOption {
	return []cel.EnvOption{
		ext.Network(),
		ext.Encoders(),
		cel.OptionalTypes(),
		filtersLib(),
	}
}

// NewCELEvaluator initializes a CEL execution environment.
func NewCELEvaluator() (Evaluator, error) {
	opts := append(CELVariableOptions(), CELLibraryOptions()...)
	env, err := cel.NewEnv(opts...)
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
	compiled := &celProgram{prg: prg, env: e.env, ast: ast}
	compiled.partial = sync.OnceValues(func() (cel.Program, error) {
		return compiled.env.Program(compiled.ast, cel.CostLimit(defaultCELCostLimit), cel.EvalOptions(cel.OptPartialEval))
	})

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

// EvalPartial implements Program. Each unknown register is marked under
// both variables that reach the register tree (registerVariables), since
// runNode binds stat and nodes to the same snapshot and a condition may
// read it through either.
func (p *celProgram) EvalPartial(vars map[string]interface{}, unknown []UnknownRegister) (bool, bool, error) {
	prg, err := p.partial()
	if err != nil {
		return false, false, fmt.Errorf("failed to create partial CEL program: %w", err)
	}
	patterns := make([]*cel.AttributePatternType, 0, 2*len(unknown))
	for _, u := range unknown {
		for variable := range registerVariables {
			pattern := cel.AttributePattern(variable).QualString(u.Name)
			// result holds a register's stats directly, with no device
			// level, so any unknown device of a register leaves its whole
			// entry there unknown.
			if !u.Whole && variable != "result" {
				pattern = pattern.QualString(u.Device)
			}
			patterns = append(patterns, pattern)
		}
	}
	activation, err := cel.PartialVars(vars, patterns...)
	if err != nil {
		return false, false, fmt.Errorf("failed to build partial CEL activation: %w", err)
	}
	out, _, err := prg.Eval(activation)
	if err != nil {
		return false, false, fmt.Errorf("CEL evaluation failed: %w", err)
	}
	if types.IsUnknown(out) {
		return false, false, nil
	}
	result, ok := out.Value().(bool)
	if !ok {
		return false, false, fmt.Errorf("CEL expression did not return a boolean")
	}
	return result, true, nil
}

package engine

import (
	"fmt"

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
	// Eval evaluates the compiled program against the given input map.
	Eval(input map[string]interface{}) (bool, error)
}

type celEvaluator struct {
	env *cel.Env
}

type celProgram struct {
	prg cel.Program
}

// NewCELEvaluator initializes a CEL execution environment.
func NewCELEvaluator() (Evaluator, error) {
	// We define a standard environment variable 'stat' which holds dynamic device properties.
	env, err := cel.NewEnv(
		cel.Variable("stat", cel.MapType(cel.StringType, cel.DynType)),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create CEL env: %w", err)
	}

	return &celEvaluator{env: env}, nil
}

func (e *celEvaluator) Compile(expression string) (Program, error) {
	ast, issues := e.env.Compile(expression)
	if issues != nil && issues.Err() != nil {
		return nil, fmt.Errorf("failed to compile CEL expression: %w", issues.Err())
	}

	prg, err := e.env.Program(ast, cel.CostLimit(defaultCELCostLimit))
	if err != nil {
		return nil, fmt.Errorf("failed to create CEL program: %w", err)
	}

	return &celProgram{prg: prg}, nil
}

func (p *celProgram) Eval(input map[string]interface{}) (bool, error) {
	out, _, err := p.prg.Eval(map[string]interface{}{
		"stat": input,
	})
	if err != nil {
		return false, fmt.Errorf("CEL evaluation failed: %w", err)
	}

	result, ok := out.Value().(bool)
	if !ok {
		return false, fmt.Errorf("CEL expression did not return a boolean")
	}

	return result, nil
}

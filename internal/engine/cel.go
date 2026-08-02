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

	prg, err := e.env.Program(ast)
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

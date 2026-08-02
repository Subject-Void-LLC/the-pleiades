package engine_test

import (
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/internal/engine"
)

func TestCELEngine_ReleaseGate(t *testing.T) {
	eval, err := engine.NewCELEvaluator()
	if err != nil {
		t.Fatalf("failed to init CEL engine: %v", err)
	}

	// 1. Compile the Release Gate expression
	expr := `stat.firmware == 'v2.0' && stat.ping_ms < 50`
	prg, err := eval.Compile(expr)
	if err != nil {
		t.Fatalf("failed to compile expression: %v", err)
	}

	// 2. Evaluate with a matching payload
	matchPayload := map[string]interface{}{
		"firmware": "v2.0",
		"ping_ms":  42,
	}
	result, err := prg.Eval(matchPayload)
	if err != nil {
		t.Fatalf("eval failed: %v", err)
	}
	if !result {
		t.Errorf("expected matching payload to evaluate to true, got false")
	}

	// 3. Evaluate with a non-matching payload
	failPayload := map[string]interface{}{
		"firmware": "v2.0",
		"ping_ms":  100, // too slow
	}
	result, err = prg.Eval(failPayload)
	if err != nil {
		t.Fatalf("eval failed: %v", err)
	}
	if result {
		t.Errorf("expected failing payload to evaluate to false, got true")
	}
}

func TestCELEngine_InvalidExpression(t *testing.T) {
	eval, _ := engine.NewCELEvaluator()
	
	// Syntax error
	_, err := eval.Compile(`stat.firmware === 'v2.0'`) // === is invalid in CEL
	if err == nil {
		t.Errorf("expected compile to fail on invalid syntax")
	}
}

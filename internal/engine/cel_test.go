package engine_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

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

// TestCELEngine_RejectsExpensiveComprehension is Phase 39's (Schema &
// Injection Hardening) regression test for a real finding: cel-go bounds
// an expression's parsed *size* (100,000 code points, its own default)
// but not its evaluation *cost*, so a nested comprehension well within
// that size limit can still take real, unbounded CPU time to evaluate,
// scaling quadratically in list length. A single when_cel string in a
// runbook is enough to trigger this, no special privilege required. This
// asserts the fix (defaultCELCostLimit, cel.go) actually rejects a
// pathological expression instead of hanging: without the fix, this exact
// expression measured multiple real seconds of CPU time in local testing.
func TestCELEngine_RejectsExpensiveComprehension(t *testing.T) {
	eval, err := engine.NewCELEvaluator()
	if err != nil {
		t.Fatalf("failed to init CEL engine: %v", err)
	}

	const n = 3200
	items := make([]string, n)
	for i := range items {
		items[i] = fmt.Sprintf("%d", i)
	}
	list := "[" + strings.Join(items, ",") + "]"
	expr := fmt.Sprintf("%s.all(x, %s.all(y, x+y>=0))", list, list)

	prg, err := eval.Compile(expr)
	if err != nil {
		t.Fatalf("expected this expression to compile (it is well within cel-go's own size limit), got: %v", err)
	}

	start := time.Now()
	_, err = prg.Eval(map[string]interface{}{})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatalf("expected the cost limit to reject this expression, got success in %v", elapsed)
	}
	// A generous bound, not a tight performance assertion: this only
	// needs to prove the cost limit terminates the evaluation instead of
	// letting it run unbounded (which measured multiple real seconds
	// even at this size with no limit at all, and scales quadratically
	// from there). The race detector alone can add a large constant
	// factor, so this leaves wide headroom rather than asserting on raw
	// speed.
	if elapsed > 15*time.Second {
		t.Fatalf("expected the cost limit to reject well before this, took %v: %v", elapsed, err)
	}
}

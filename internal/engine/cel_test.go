package engine_test

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
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
	result, err := prg.Eval(map[string]interface{}{"stat": matchPayload})
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
	result, err = prg.Eval(map[string]interface{}{"stat": failPayload})
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

	// The same limit holds on the partial evaluation a check uses (Phase
	// 46's hardening audit): a check's conditions are the same runbook
	// author's text, evaluated by a second program.
	start := time.Now()
	if _, _, err := prg.EvalPartial(map[string]interface{}{}, []engine.UnknownRegister{{Name: "a", Whole: true}}); err == nil {
		t.Fatalf("expected the cost limit to reject this expression in a check, got success in %v", time.Since(start))
	}

	start = time.Now()
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

// TestCELEngine_NodesVariable is Phase 9's own regression test for the
// checklist item it closes: before this phase, "nodes" was not declared at
// all, so any expression referencing it failed to compile. This proves it
// compiles and evaluates, using the real WorkflowContext shape (a map
// keyed by device ID, not a list) and the map-key-iteration idiom that
// PLAN.md Sections 21.4/27.3 were dated-corrected to (see PLAN.md and
// this test's sibling in executor_test.go for the same proof through the
// real Executor).
func TestCELEngine_NodesVariable(t *testing.T) {
	eval, err := engine.NewCELEvaluator()
	if err != nil {
		t.Fatalf("failed to init CEL engine: %v", err)
	}

	prg, err := eval.Compile(`nodes.precheck.exists(d, nodes.precheck[d].needs_reboot == true)`)
	if err != nil {
		t.Fatalf("failed to compile nodes-rooted expression: %v", err)
	}

	nodes := map[string]interface{}{
		"precheck": map[string]interface{}{
			"switch1": map[string]interface{}{"needs_reboot": false},
			"switch2": map[string]interface{}{"needs_reboot": true},
		},
	}
	result, err := prg.Eval(map[string]interface{}{"nodes": nodes})
	if err != nil {
		t.Fatalf("eval failed: %v", err)
	}
	if !result {
		t.Errorf("expected exists() to find switch2's needs_reboot == true, got false")
	}

	// A tree with no device needing reboot must evaluate false, not error:
	// proves this is a real, data-driven check, not one that only ever
	// returns true.
	allFine := map[string]interface{}{
		"precheck": map[string]interface{}{
			"switch1": map[string]interface{}{"needs_reboot": false},
		},
	}
	result, err = prg.Eval(map[string]interface{}{"nodes": allFine})
	if err != nil {
		t.Fatalf("eval failed: %v", err)
	}
	if result {
		t.Errorf("expected exists() to find no device needing reboot, got true")
	}
}

// TestCELEngine_CompileSharesProgramForIdenticalExpressions proves the
// Flyweight cache actually shares one compiled Program across every caller
// that compiles identical text, rather than each call independently
// recompiling (defeating the "cache compiled programs" checklist item) or
// each call independently memoizing its own private copy (which would
// waste memory without the sharing Flyweight itself promises).
func TestCELEngine_CompileSharesProgramForIdenticalExpressions(t *testing.T) {
	eval, err := engine.NewCELEvaluator()
	if err != nil {
		t.Fatalf("failed to init CEL engine: %v", err)
	}

	p1, err := eval.Compile(`stat.a == true`)
	if err != nil {
		t.Fatalf("compile 1 failed: %v", err)
	}
	p2, err := eval.Compile(`stat.a == true`)
	if err != nil {
		t.Fatalf("compile 2 failed: %v", err)
	}
	if p1 != p2 {
		t.Errorf("expected identical expression text to return the same shared Program, got two distinct instances")
	}

	p3, err := eval.Compile(`stat.a == false`)
	if err != nil {
		t.Fatalf("compile 3 failed: %v", err)
	}
	if p1 == p3 {
		t.Errorf("expected different expression text to return a distinct Program, got the same instance")
	}
}

// TestCELEngine_ConcurrentCompileConvergesOnOneSharedProgram races many
// goroutines compiling the exact same new expression text for the first
// time (the case the cache's own store-if-absent logic exists to handle:
// see cel.go's Compile doc comment). Every goroutine must observe success,
// no data race (run with -race), and every returned Program must be the
// same shared instance, proving concurrent first-time compiles converge on
// one winner rather than each goroutine keeping a separately-compiled copy.
// cel-go's own documentation states a compiled Program is safe for
// concurrent Eval, so sharing one instance here is also safe at Eval time,
// exercised by evaluating that shared Program concurrently afterward.
func TestCELEngine_ConcurrentCompileConvergesOnOneSharedProgram(t *testing.T) {
	eval, err := engine.NewCELEvaluator()
	if err != nil {
		t.Fatalf("failed to init CEL engine: %v", err)
	}

	const goroutines = 32
	programs := make([]engine.Program, goroutines)
	errs := make([]error, goroutines)

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func(i int) {
			defer wg.Done()
			programs[i], errs[i] = eval.Compile(`stat.concurrent == true`)
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("goroutine %d: compile failed: %v", i, err)
		}
		if programs[i] != programs[0] {
			t.Errorf("goroutine %d: expected the same shared Program as goroutine 0, got a distinct instance", i)
		}
	}

	// Evaluate the shared Program concurrently too, proving the sharing
	// this test just proved is actually safe to use, not just structurally
	// identical.
	var evalWG sync.WaitGroup
	evalWG.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer evalWG.Done()
			result, err := programs[0].Eval(map[string]interface{}{"stat": map[string]interface{}{"concurrent": true}})
			if err != nil {
				t.Errorf("concurrent eval failed: %v", err)
			}
			if !result {
				t.Errorf("expected concurrent eval to return true")
			}
		}()
	}
	evalWG.Wait()
}

package engine_test

import (
	"fmt"
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/internal/engine"
)

func BenchmarkCELEval(b *testing.B) {
	eval, _ := engine.NewCELEvaluator()
	prg, _ := eval.Compile(`stat.firmware == 'v2.0' && stat.ping_ms < 50`)

	vars := map[string]interface{}{
		"stat": map[string]interface{}{
			"firmware": "v2.0",
			"ping_ms":  42,
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := prg.Eval(vars)
		if err != nil {
			b.Fatalf("eval failed: %v", err)
		}
	}
}

// BenchmarkCELCompile_Cached measures a Compile call that always hits the
// Flyweight cache (cel.go): after the first call, every subsequent call to
// the identical expression text should cost roughly one map lookup under a
// mutex, not a real parse/type-check/program-construction pass.
func BenchmarkCELCompile_Cached(b *testing.B) {
	eval, _ := engine.NewCELEvaluator()
	const expr = `stat.firmware == 'v2.0' && stat.ping_ms < 50`
	if _, err := eval.Compile(expr); err != nil {
		b.Fatalf("warmup compile failed: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := eval.Compile(expr); err != nil {
			b.Fatalf("compile failed: %v", err)
		}
	}
}

// BenchmarkCELCompile_Uncached measures the cold path: a distinct,
// never-before-seen expression on every iteration, so every call is a real
// cache miss doing the full parse/type-check/program-construction work.
// The gap between this and BenchmarkCELCompile_Cached is the direct,
// measured evidence that the Flyweight cache does what PLAN.md Section
// 21.4's "microseconds" claim requires: without it, N tasks sharing
// identical when/when_cel text would each pay this cold cost instead of
// paying it once. No credible existing published figure for "CEL compile
// cost, cached vs. uncached" exists to cite as an industry-alternative
// comparison (AGENTS.md's benchmarking rule); stated plainly here rather
// than fabricated, matching this project's own established precedent
// (HANDOFF_DOCUMENT.md, the Phase 7 session's keyset-pagination benchmark
// note).
func BenchmarkCELCompile_Uncached(b *testing.B) {
	eval, _ := engine.NewCELEvaluator()

	exprs := make([]string, b.N)
	for i := range exprs {
		exprs[i] = fmt.Sprintf("stat.uncached_%d == true", i)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := eval.Compile(exprs[i]); err != nil {
			b.Fatalf("compile failed: %v", err)
		}
	}
}

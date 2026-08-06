package engine_test

import (
	"fmt"
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/internal/engine"
)

// BenchmarkDAGBuilder exercises a representative tasks-shaped runbook: a
// handful of leaf tasks, one gated behind a "when" condition, so the
// benchmark still measures real CEL condition compilation rather than a
// build path that never invokes it. Each iteration's condition text is
// unique (interpolating i into "when"), deliberately defeating cel.go's
// own Flyweight compile cache (Phase 9): reusing identical text across
// iterations would turn this into a cache-hit benchmark after the first
// call, silently changing what "real CEL condition compilation" means
// without any test failing to flag it. BenchmarkCELCompile_Cached
// (cel_bench_test.go) is the benchmark that measures the cache-hit path;
// this one stays honest about measuring the cold path.
func BenchmarkDAGBuilder(b *testing.B) {
	eval, _ := engine.NewCELEvaluator()
	builder := engine.NewBuilder(eval)

	payloads := make([][]byte, b.N)
	for i := range payloads {
		payloads[i] = []byte(fmt.Sprintf(`{
			"id": "bench-runbook",
			"tasks": [
				{"name": "start", "fqcn": "noop"},
				{"name": "gather facts", "fqcn": "ssh_exec", "when": "stat.a > %d"},
				{"name": "apply config", "fqcn": "ios_backup"},
				{"name": "verify", "fqcn": "ssh_exec", "when": "stat.b == %d"},
				{"name": "finish", "fqcn": "noop"}
			]
		}`, i, i))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := builder.Build(payloads[i])
		if err != nil {
			b.Fatalf("build failed: %v", err)
		}
	}
}

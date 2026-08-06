package engine_test

import (
	"fmt"
	"strings"
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

// BenchmarkDAGBuilder_LargeFlatTaskList measures Build() over a large flat
// tasks: list (10,000 tasks, no nesting), the shape that drove the old
// recursive hasCycle's stack depth linearly with input size (Phase 10's
// Schema/Injection Hardening finding, FAILURE_PATTERNS.md #62). The same
// payload is reused across iterations (unlike BenchmarkDAGBuilder above,
// which deliberately varies its condition text to defeat the CEL compile
// cache): this benchmark's own subject is graph size, not CEL compilation,
// and every task here is unconditional. No credible existing published
// AWX/Tower/raw-topological-sort figure exists for this specific
// comparison (AGENTS.md's benchmarking rule); stated plainly rather than
// fabricated.
func BenchmarkDAGBuilder_LargeFlatTaskList(b *testing.B) {
	const n = 10000
	var sb strings.Builder
	sb.WriteString(`{"id":"bench-large","tasks":[`)
	for i := 0; i < n; i++ {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(`{"name":"t","fqcn":"noop"}`)
	}
	sb.WriteString(`]}`)
	payload := []byte(sb.String())

	eval, _ := engine.NewCELEvaluator()
	builder := engine.NewBuilder(eval)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := builder.Build(payload); err != nil {
			b.Fatalf("build failed: %v", err)
		}
	}
}

// BenchmarkDAGBuilder_Parallel measures Build() over a runbook whose whole
// body is one parallel task with 100 children, the new Phase 10 construct
// synthesizeParallel (tasktree.go) adds.
func BenchmarkDAGBuilder_Parallel(b *testing.B) {
	const n = 100
	var sb strings.Builder
	sb.WriteString(`{"id":"bench-parallel","tasks":[{"name":"fanout","parallel":[`)
	for i := 0; i < n; i++ {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(`{"name":"p","fqcn":"noop"}`)
	}
	sb.WriteString(`]}]}`)
	payload := []byte(sb.String())

	eval, _ := engine.NewCELEvaluator()
	builder := engine.NewBuilder(eval)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := builder.Build(payload); err != nil {
			b.Fatalf("build failed: %v", err)
		}
	}
}

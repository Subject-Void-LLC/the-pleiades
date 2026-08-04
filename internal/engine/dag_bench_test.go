package engine_test

import (
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/internal/engine"
)

// BenchmarkDAGBuilder exercises a representative tasks-shaped runbook: a
// handful of leaf tasks, one gated behind a "when" condition, so the
// benchmark still measures real CEL condition compilation rather than a
// build path that never invokes it.
func BenchmarkDAGBuilder(b *testing.B) {
	eval, _ := engine.NewCELEvaluator()
	builder := engine.NewBuilder(eval)

	payload := []byte(`{
		"id": "bench-runbook",
		"tasks": [
			{"name": "start", "fqcn": "noop"},
			{"name": "gather facts", "fqcn": "ssh_exec", "when": "stat.a > 1"},
			{"name": "apply config", "fqcn": "ios_backup"},
			{"name": "verify", "fqcn": "ssh_exec", "when": "stat.b == true"},
			{"name": "finish", "fqcn": "noop"}
		]
	}`)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := builder.Build(payload)
		if err != nil {
			b.Fatalf("build failed: %v", err)
		}
	}
}

package engine_test

import (
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/internal/engine"
)

func BenchmarkDAGBuilder(b *testing.B) {
	eval, _ := engine.NewCELEvaluator()
	builder := engine.NewBuilder(eval)

	payload := []byte(`{
		"id": "bench-playbook",
		"nodes": [
			{"id": "N1"}, {"id": "N2"}, {"id": "N3"}, {"id": "N4"}, {"id": "N5"}
		],
		"edges": [
			{"from": "N1", "to": "N2", "condition": "stat.a > 1"},
			{"from": "N2", "to": "N3"},
			{"from": "N3", "to": "N4", "condition": "stat.b == true"},
			{"from": "N4", "to": "N5"}
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

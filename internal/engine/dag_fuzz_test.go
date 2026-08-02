package engine_test

import (
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/internal/engine"
)

func FuzzDAGBuilder(f *testing.F) {
	eval, _ := engine.NewCELEvaluator()
	builder := engine.NewBuilder(eval)

	f.Add([]byte(`{}`))
	f.Add([]byte(`{"nodes": [{"id":"A"}], "edges": [{"from":"A", "to":"A"}]}`))
	f.Add([]byte(`malformed-json`))

	f.Fuzz(func(t *testing.T, payload []byte) {
		// Just ensure it doesn't panic on arbitrary byte slices
		builder.Build(payload)
	})
}

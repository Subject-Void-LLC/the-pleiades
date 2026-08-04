package engine_test

import (
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/internal/engine"
)

func BenchmarkCELEval(b *testing.B) {
	eval, _ := engine.NewCELEvaluator()
	prg, _ := eval.Compile(`stat.firmware == 'v2.0' && stat.ping_ms < 50`)

	payload := map[string]interface{}{
		"firmware": "v2.0",
		"ping_ms":  42,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := prg.Eval(payload)
		if err != nil {
			b.Fatalf("eval failed: %v", err)
		}
	}
}

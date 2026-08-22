package engine_test

import (
	"fmt"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
)

// BenchmarkInProcessWorkflowContextMerge measures the cost of the
// operation Executor calls once per successful device execution that
// carries a Register name: the direct, in-memory Crawl-tier equivalent of
// PLAN.md Section 27's NATS KV stats aggregation.
func BenchmarkInProcessWorkflowContextMerge(b *testing.B) {
	wc := engine.NewInProcessWorkflowContext()
	stats := map[string]interface{}{"needs_reboot": true, "firmware": "v2.0"}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = wc.Merge("precheck", fmt.Sprintf("host%d", i%64), stats)
	}
}

// BenchmarkInProcessWorkflowContextRead measures the cost of the
// deep-copy snapshot Executor takes before every conditional node's CEL
// evaluation, against a context already populated with 64 devices' worth
// of stats.
func BenchmarkInProcessWorkflowContextRead(b *testing.B) {
	wc := engine.NewInProcessWorkflowContext()
	for i := 0; i < 64; i++ {
		_ = wc.Merge("precheck", fmt.Sprintf("host%d", i), map[string]interface{}{"needs_reboot": true, "firmware": "v2.0"})
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := wc.Read(); err != nil {
			b.Fatalf("Read failed: %v", err)
		}
	}
}

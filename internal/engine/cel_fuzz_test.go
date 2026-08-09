package engine_test

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
)

func FuzzCELCompile(f *testing.F) {
	eval, _ := engine.NewCELEvaluator()

	f.Add(`stat.firmware == 'v2.0'`)
	f.Add(`stat.ping_ms < 50`)
	f.Add(`invalid.syntax()`)
	f.Add(`stat['key'] == true`)
	f.Add(``)
	// Phase 9: the "nodes" root (PLAN.md Section 27's cross-node
	// aggregation) and its map-key-iteration idiom, the surface this phase
	// newly declares.
	f.Add(`nodes.precheck["switch1"].needs_reboot == true`)
	f.Add(`nodes.precheck.exists(d, nodes.precheck[d].needs_reboot == true)`)
	f.Add(`nodes.precheck.all(d, nodes.precheck[d].online == true)`)

	f.Fuzz(func(t *testing.T, expr string) {
		prg, err := eval.Compile(expr)
		if err == nil && prg != nil {
			// If it compiled, it shouldn't panic on eval
			_, _ = prg.Eval(map[string]interface{}{})
		}
	})
}

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

// FuzzCELEvalPartial covers the partial evaluation a check uses for its
// conditions: any expression that compiles, against a register tree with
// an arbitrary register marked unknown, never panics, and with nothing
// marked unknown always gives the answer Eval gives. The second half is
// what keeps a check's conditions from drifting from a real run's where
// no task went unchecked.
func FuzzCELEvalPartial(f *testing.F) {
	eval, _ := engine.NewCELEvaluator()
	for _, seed := range []struct{ expr, unknown string }{
		{`stat.a[""].changed`, "a"},
		{`stat.a[""].changed || vars.force`, "a"},
		{`has(stat.a) && stat.b[""].seen == 'x'`, "a"},
		{`nodes.a.exists(d, nodes.a[d].changed)`, "a"},
		{`stat.reslt[""].x == 1 || stat.a[""].changed`, "a"},
		{`size(stat) > 1`, "b"},
		{`stat.b[""].seen == 'x'`, ""},
	} {
		f.Add(seed.expr, seed.unknown, "")
	}
	f.Fuzz(func(t *testing.T, expr, unknown, device string) {
		prg, err := eval.Compile(expr)
		if err != nil {
			return
		}
		vars := func() map[string]interface{} {
			tree := map[string]interface{}{
				"b": map[string]interface{}{"": map[string]interface{}{"seen": "x", "changed": true}},
			}
			return map[string]interface{}{"stat": tree, "nodes": tree, "vars": map[string]interface{}{"force": true}}
		}
		_, _, _ = prg.EvalPartial(vars(), []engine.UnknownRegister{{Name: unknown, Device: device}, {Name: unknown, Whole: true}})

		want, wantErr := prg.Eval(vars())
		got, known, gotErr := prg.EvalPartial(vars(), nil)
		if (wantErr == nil) != (gotErr == nil) || (wantErr == nil && (!known || got != want)) {
			t.Fatalf("%q: EvalPartial with nothing unknown = (%v, known %v, %v), Eval = (%v, %v)", expr, got, known, gotErr, want, wantErr)
		}
	})
}

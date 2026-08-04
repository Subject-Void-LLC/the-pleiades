package engine_test

import (
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/internal/engine"
)

func FuzzCELCompile(f *testing.F) {
	eval, _ := engine.NewCELEvaluator()

	f.Add(`stat.firmware == 'v2.0'`)
	f.Add(`stat.ping_ms < 50`)
	f.Add(`invalid.syntax()`)
	f.Add(`stat['key'] == true`)
	f.Add(``)

	f.Fuzz(func(t *testing.T, expr string) {
		prg, err := eval.Compile(expr)
		if err == nil && prg != nil {
			// If it compiled, it shouldn't panic on eval
			_, _ = prg.Eval(map[string]interface{}{})
		}
	})
}

// Package engine: the environment a condition is parsed in for its
// references is the compile environment's own syntax, library by library and
// macro by macro, so the two cannot drift apart again (FAILURE_PATTERNS 1351).
package engine

import (
	"slices"
	"testing"

	"cel.dev/cel-go/cel"
)

// TestParseEnv_HasTheCompileEnvironmentsSyntax: parseEnv holds every library
// and every macro the compile environment does. The control is a bare
// environment, the parser this package once used, which lacks the optional
// syntax's library and macros, so the comparison can tell the two apart.
func TestParseEnv_HasTheCompileEnvironmentsSyntax(t *testing.T) {
	compile, err := cel.NewEnv(conditionEnvOptions()...)
	if err != nil {
		t.Fatal(err)
	}
	parse, err := parseEnv()
	if err != nil {
		t.Fatal(err)
	}
	bare, err := cel.NewEnv()
	if err != nil {
		t.Fatal(err)
	}
	macros := func(env *cel.Env) []string {
		var keys []string
		for _, m := range env.Macros() {
			keys = append(keys, m.MacroKey())
		}
		slices.Sort(keys)
		return keys
	}
	libraries := func(env *cel.Env) []string {
		names := env.Libraries()
		slices.Sort(names)
		return names
	}
	if got, want := libraries(parse), libraries(compile); !slices.Equal(got, want) {
		t.Errorf("parse environment's libraries %v, compile environment's %v", got, want)
	}
	if got, want := macros(parse), macros(compile); !slices.Equal(got, want) {
		t.Errorf("parse environment's macros %v, compile environment's %v", got, want)
	}
	if slices.Equal(macros(bare), macros(compile)) || slices.Equal(libraries(bare), libraries(compile)) {
		t.Errorf("control: a bare environment has the compile environment's syntax, so this test cannot tell them apart")
	}
}

// Tests for EnvParam.
package sdk

import (
	"strings"
	"testing"
)

func TestEnvParam(t *testing.T) {
	env, err := EnvParam(map[string]any{"env": map[string]any{"NAME": "x", "N": 3, "B": true, "F": 1.5}}, "env")
	if err != nil {
		t.Fatalf("EnvParam: %v", err)
	}
	want := map[string]string{"PLEIADES_NAME": "x", "PLEIADES_N": "3", "PLEIADES_B": "true", "PLEIADES_F": "1.5"}
	if len(env) != len(want) {
		t.Fatalf("env = %v", env)
	}
	for k, v := range want {
		if env[k] != v {
			t.Errorf("env[%s] = %q, want %q", k, env[k], v)
		}
	}
	if env, err := EnvParam(map[string]any{}, "env"); env != nil || err != nil {
		t.Errorf("absent: %v, %v", env, err)
	}
	if env, err := EnvParam(map[string]any{"env": nil}, "env"); env != nil || err != nil {
		t.Errorf("nil: %v, %v", env, err)
	}
	if _, err := EnvParam(map[string]any{"env": "A=1"}, "env"); err == nil || !strings.Contains(err.Error(), "must be a map") {
		t.Errorf("not a map: %v", err)
	}
	if _, err := EnvParam(map[string]any{"env": map[string]any{"A": []any{1}}}, "env"); err == nil || !strings.Contains(err.Error(), "A must be") {
		t.Errorf("nested: %v", err)
	}
}

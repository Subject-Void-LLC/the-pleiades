// Tests for ParamTypeRule against the real registered catalog, so the
// declared types it reads are the ones the binary ships, and through the
// real YAML builder, so a value is the type a runbook file decodes to.
package validate_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/validate"
)

// TestParamTypeRule covers what the rule refuses and what it lets through.
func TestParamTypeRule(t *testing.T) {
	for _, tc := range []struct {
		name   string
		fqcn   string
		params map[string]any
		want   []string // substrings of the one finding; nil means no finding
	}{
		{"text for a string", "exec.shell", map[string]any{"cmd": "cat", "stdin": "4"}, nil},
		{
			"a number for a string", "exec.shell", map[string]any{"cmd": "cat", "stdin": 4},
			[]string{`task tasks[0] (name "step") passes parameter "stdin" a number`, "exec.shell declares it string", "quote it"},
		},
		{
			"text for an int", "wait.connection", map[string]any{"timeout": "600"},
			[]string{`parameter "timeout" a string`, "declares it int", "unquoted"},
		},
		{"a whole float for an int, as JSON decodes it", "wait.connection", map[string]any{"timeout": float64(600)}, nil},
		{
			"text for a bool", "win.firewall.rule", map[string]any{"name": "r", "enabled": "true"},
			[]string{`parameter "enabled" a string`, "declares it bool"},
		},
		{"a list key", "file.json", map[string]any{"path": "/x", "key": []any{"features", "legacy.mode"}, "state": "absent"}, nil},
		{"a port number", "win.firewall.rule", map[string]any{"name": "r", "local_port": 22}, nil},
		{"ports as one string", "win.firewall.rule", map[string]any{"name": "r", "local_port": "22,443"}, nil},
		{"one status code or several", "http.request", map[string]any{"url": "https://x.example/", "status_code": []any{200, 204}}, nil},
		{"a template is the engine's to judge", "exec.shell", map[string]any{"cmd": "cat", "stdin": "{{ vars.n }}"}, nil},
		{"an undeclared parameter is ParamsRule's", "exec.shell", map[string]any{"cmd": "cat", "stdn": 4}, nil},
		{"null is absent", "exec.shell", map[string]any{"cmd": "cat", "stdin": nil}, nil},
		{"engine action", "noop", map[string]any{"anything": 1}, nil},
		{"declared-only method", "file.template", map[string]any{"src": 1}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			findings := validate.ParamTypeRule(validate.WorldView{DAG: dagWithParams(tc.fqcn, tc.params)})
			if tc.want == nil {
				if len(findings) != 0 {
					t.Fatalf("want no finding, got %v", findings)
				}
				return
			}
			if len(findings) != 1 || findings[0].RuleName != "param-type" {
				t.Fatalf("want one param-type finding, got %v", findings)
			}
			for _, w := range tc.want {
				if !strings.Contains(findings[0].Message, w) {
					t.Errorf("finding %q lacks %q", findings[0].Message, w)
				}
			}
		})
	}
}

// TestParamTypeRule_WhatAYAMLFileDecodesTo: the values YAML makes easy to
// write by mistake, decoded by the builder a runbook file goes through. An
// unquoted mode decodes as a number, not the text file.directory's mode
// takes; a quoted one is text and passes.
func TestParamTypeRule_WhatAYAMLFileDecodesTo(t *testing.T) {
	eval, err := engine.NewCELEvaluator()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		task string
		want string
	}{
		{"file.directory:\n      path: /srv/app\n      mode: 0755", `parameter "mode" a number`},
		{"file.directory:\n      path: /srv/app\n      mode: \"0755\"", ""},
		{"exec.shell:\n      cmd: cat\n      stdin: 4", `parameter "stdin" a number`},
		{"wait.connection:\n      timeout: \"600\"", `parameter "timeout" a string`},
	} {
		dag, err := engine.NewBuilder(eval).BuildFromYAML([]byte("id: y\nhosts: web1\ntasks:\n  - name: step\n    " + tc.task + "\n"))
		if err != nil {
			t.Fatalf("%s: %v", tc.task, err)
		}
		findings := validate.ParamTypeRule(validate.WorldView{DAG: dag})
		switch {
		case tc.want == "" && len(findings) != 0:
			t.Errorf("%s: want no finding, got %v", tc.task, findings)
		case tc.want != "" && (len(findings) != 1 || !strings.Contains(findings[0].Message, tc.want)):
			t.Errorf("%s: want a finding saying %q, got %v", tc.task, tc.want, findings)
		}
	}
}

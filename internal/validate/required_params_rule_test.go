// Tests for RequiredParamsRule against the real registered catalog, so the
// required parameters it reads are the ones the binary ships.
package validate_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/validate"
)

// TestRequiredParamsRule covers what the rule refuses and what it lets
// through: every required parameter given passes, one left out or given
// as null is refused by name, a fragment's required parameters count, a
// template counts as given, a task with no parameters at all is checked,
// and an engine action or an unknown or declared-only method is left to
// the rules that own it.
func TestRequiredParamsRule(t *testing.T) {
	for _, tc := range []struct {
		name   string
		fqcn   string
		params map[string]any
		want   []string // substrings of the one finding; nil means no finding
	}{
		{"every required parameter given", "net.ios.config", map[string]any{"lines": []any{"hostname sw1"}, "backup": true}, nil},
		{
			"a required parameter left out", "net.ios.config", map[string]any{"backup": true},
			[]string{`task tasks[0] (name "step") calls net.ios.config without "lines", which it requires`, "pleiades doc net.ios.config describes it"},
		},
		{"no parameters at all", "net.cli.command", nil, []string{`without "command"`}},
		{"given as null", "net.cli.command", map[string]any{"command": nil}, []string{`without "command"`}},
		{"several left out, sorted", "archive.create", map[string]any{}, []string{`without "path", "src"`, "describes them"}},
		{"a fragment's required parameter", "container.run", map[string]any{"name": "web"}, []string{`without "image"`}},
		{"a template counts as given", "net.cli.command", map[string]any{"command": "{{ vars.line | cli_token }}"}, nil},
		{"an alias gives its parameter", "file.symlink", map[string]any{"src": "/opt/tool", "dest": "/usr/local/bin/tool"}, nil},
		{"neither the parameter nor its alias", "file.symlink", map[string]any{"src": "/opt/tool"}, []string{`without "path"`}},
		{"engine action", "noop", nil, nil},
		{"engine keyword spelled with dots", "pleiades.builtin.set_metadata", nil, nil},
		{"unregistered method", "totally.fake.name", nil, nil},
		{"declared-only method", "file.template", nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			findings := validate.RequiredParamsRule(validate.WorldView{DAG: dagWithParams(tc.fqcn, tc.params)})
			if tc.want == nil {
				if len(findings) != 0 {
					t.Errorf("findings = %v, want none", findings)
				}
				return
			}
			if len(findings) != 1 || findings[0].RuleName != "param-required" || findings[0].Node != "tasks[0]" {
				t.Fatalf("findings = %+v, want one param-required finding on tasks[0]", findings)
			}
			for _, w := range tc.want {
				if !strings.Contains(findings[0].Message, w) {
					t.Errorf("message %q does not contain %q", findings[0].Message, w)
				}
			}
		})
	}
}

// TestRequiredParamsRule_RunsInValidateAndOverRollbackSteps: the rule is
// one Validate runs, and a task's rollback: step is held to it too, since
// an undo missing a parameter would fail at the rollback that needs it.
func TestRequiredParamsRule_RunsInValidateAndOverRollbackSteps(t *testing.T) {
	dag := dagWithParams("net.ios.config", map[string]any{"lines": []any{"hostname sw1"}})
	dag.Nodes["tasks[0]"].Rollback = []engine.Task{{FQCN: "net.ios.config", Params: map[string]any{"backup": true}}}
	report := validate.Validate(validate.WorldView{DAG: dag})
	var rollback []validate.Finding
	for _, f := range report.Findings {
		if f.RuleName == "rollback" && strings.Contains(f.Message, `without "lines"`) {
			rollback = append(rollback, f)
		}
		if f.RuleName == "param-required" {
			t.Errorf("the forward task gives lines, and was refused: %+v", f)
		}
	}
	if len(rollback) != 1 || rollback[0].Node != "tasks[0].rollback[0]" {
		t.Fatalf("findings %+v, want the rollback step refused for leaving out lines", report.Findings)
	}

	// By rule name: the task has no device, so CapabilityRule refuses it too,
	// and HasErrors alone would pass with this rule gone.
	missing := validate.Validate(validate.WorldView{DAG: dagWithParams("net.ios.config", map[string]any{"backup": true})})
	if !slices.ContainsFunc(missing.Findings, func(f validate.Finding) bool { return f.RuleName == "param-required" }) {
		t.Fatalf("findings %+v, want a param-required one for the task leaving out lines", missing.Findings)
	}
}

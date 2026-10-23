// Tests of VarsRule: a parameter reading a run variable nobody supplied is
// found before anything runs, and only when the caller knows the run's
// variables.
package validate_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/validate"
)

// varsRunbook reads vars.candidate plainly, vars.region through a default,
// and vars.candidate a second time in a list; a task with no template and
// one whose template does not compile (TemplateRule's to report) give
// VarsRule nothing to say.
const varsRunbook = `id: vars
tasks:
  - name: apply
    noop:
      src: "{{ vars.candidate }}"
      region: "{{ vars.region | default('us-east-1') }}"
      items:
        - "{{ vars.candidate }}"
        - "{{ vars.other }}"
  - name: plain
    noop:
      msg: no template here
  - name: broken
    noop:
      msg: "{{ vars.broken | no_such_filter }}"
  - name: only when a candidate is given
    when: vars.candidate != ''
    noop:
      src: "{{ vars.candidate }}"
`

// TestVarsRule covers each answer: every missing name once per parameter,
// with its task; nothing for a supplied name or a defaulted one, or for a
// task whose own when: may skip it; and nothing at all when the run's
// variables are not known to the caller.
func TestVarsRule(t *testing.T) {
	dag := yamlDAG(t, varsRunbook)
	for _, tc := range []struct {
		name  string
		world validate.WorldView
		want  []string
	}{
		{name: "none supplied", world: validate.WorldView{DAG: dag, VariablesKnown: true},
			want: []string{"parameter items: no value is supplied for vars.candidate", "parameter items: no value is supplied for vars.other", "parameter src: no value is supplied for vars.candidate"}},
		{name: "candidate supplied", world: validate.WorldView{DAG: dag, VariablesKnown: true, Variables: map[string]any{"candidate": "c.conf"}},
			want: []string{"parameter items: no value is supplied for vars.other"}},
		{name: "every one supplied", world: validate.WorldView{DAG: dag, VariablesKnown: true, Variables: map[string]any{"candidate": "c.conf", "other": 1}}},
		{name: "variables not known", world: validate.WorldView{DAG: dag}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			findings := validate.VarsRule(tc.world)
			if len(findings) != len(tc.want) {
				t.Fatalf("findings = %+v, want %d", findings, len(tc.want))
			}
			for i, f := range findings {
				if f.RuleName != "vars" || f.Node != "tasks[0]" || !strings.Contains(f.Message, tc.want[i]) || !strings.Contains(f.Message, `task tasks[0] (name "apply")`) {
					t.Errorf("finding %d = %+v, want %q on tasks[0]", i, f, tc.want[i])
				}
				if strings.Contains(f.Message, "region") {
					t.Errorf("a defaulted variable was refused: %s", f.Message)
				}
			}
		})
	}
	// Validate runs the rule: a run given no variables is refused.
	if report := validate.Validate(validate.WorldView{DAG: dag, VariablesKnown: true}); !strings.Contains(report.String(), "[vars] node \"tasks[0]\"") {
		t.Errorf("Validate does not run VarsRule:\n%s", report.String())
	}
}

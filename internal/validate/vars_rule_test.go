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
			// Only the first task's parameters are in question here; the
			// condition on the last task is read by its own test below.
			var findings []validate.Finding
			for _, f := range validate.VarsRule(tc.world) {
				if f.Node == "tasks[0]" {
					findings = append(findings, f)
				}
			}
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

// varsRollbackRunbook undoes a change with a step reading vars.undo_to, and
// a conditional task (testing for vars.flag) with one reading vars.never, which
// is left to the engine with its task.
const varsRollbackRunbook = `id: vars-rollback
tasks:
  - name: change
    noop:
      msg: plain
    rollback:
      - noop:
          to: "{{ vars.undo_to }}"
  - name: maybe
    when: has(vars.flag) && vars.flag == 'x'
    noop:
      msg: plain
    rollback:
      - noop:
          to: "{{ vars.never }}"
`

// TestVarsRule_ReadsRollbackStepsAndAdvisesOnlyAFlagTheCommandHas: a
// rollback: step reading a variable nobody supplied is found, under the
// step's own name, since it renders with the run's variables when the task
// is undone; a step of a conditional task is not. The advice names
// --extra-vars only for a command that takes it (run, validate) and tells a
// command with none (adhoc, rollback) to write the value or a default.
func TestVarsRule_ReadsRollbackStepsAndAdvisesOnlyAFlagTheCommandHas(t *testing.T) {
	dag := yamlDAG(t, varsRollbackRunbook)
	withFlag := validate.VarsRule(validate.WorldView{DAG: dag, VariablesKnown: true, VariablesFlag: "--extra-vars"})
	if len(withFlag) != 1 || withFlag[0].Node != "tasks[0].rollback[0]" || !strings.Contains(withFlag[0].Message, "no value is supplied for vars.undo_to; give it with --extra-vars undo_to=<value>") {
		t.Fatalf("with --extra-vars: findings %+v, want one on tasks[0].rollback[0] naming the flag", withFlag)
	}
	without := validate.VarsRule(validate.WorldView{DAG: dag, VariablesKnown: true})
	if len(without) != 1 || !strings.Contains(without[0].Message, "this command takes no variables") || strings.Contains(without[0].Message, "--extra-vars") {
		t.Fatalf("with no flag: findings %+v, want advice that names no flag", without)
	}
	if got := validate.VarsRule(validate.WorldView{DAG: dag, VariablesKnown: true, Variables: map[string]any{"undo_to": "a"}}); len(got) != 0 {
		t.Errorf("a supplied step variable was refused: %+v", got)
	}

	// The same advice for a task's own parameter, as adhoc would meet it.
	adhoc := validate.VarsRule(validate.WorldView{DAG: yamlDAG(t, varsRunbook), VariablesKnown: true})
	for _, f := range adhoc {
		if strings.Contains(f.Message, "--extra-vars") {
			t.Errorf("a command with no --extra-vars was told to use it: %s", f.Message)
		}
	}
	if len(adhoc) == 0 {
		t.Error("no finding for a command with no variables")
	}
}

// TestVarsRule_ReadsAConditionOfItsOwn: a task's when: reading a name nobody
// supplies is found on the task, though its parameters are left to the
// engine, and is quiet once the name is supplied.
func TestVarsRule_ReadsAConditionOfItsOwn(t *testing.T) {
	dag := yamlDAG(t, varsRunbook)
	var onCondition []validate.Finding
	for _, f := range validate.VarsRule(validate.WorldView{DAG: dag, VariablesKnown: true}) {
		if f.Node == "tasks[3]" {
			onCondition = append(onCondition, f)
		}
	}
	if len(onCondition) != 1 || !strings.Contains(onCondition[0].Message, "condition: no value is supplied for vars.candidate") {
		t.Fatalf("findings on the conditional task = %+v", onCondition)
	}
	if got := validate.VarsRule(validate.WorldView{DAG: dag, VariablesKnown: true, Variables: map[string]any{"candidate": "c", "other": 1}}); len(got) != 0 {
		t.Errorf("findings with everything supplied: %+v", got)
	}
}

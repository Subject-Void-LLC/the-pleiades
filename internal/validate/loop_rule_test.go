// Tests of what validate says about loops and retries: a loop's item is
// readable inside the loop and nowhere else, a loop's own expression may not
// read the item, and an until over a method that changes a device warns.
package validate_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/validate"
)

func templateFindings(t *testing.T, runbook string) []validate.Finding {
	t.Helper()
	return validate.TemplateRule(validate.WorldView{DAG: yamlDAG(t, runbook)})
}

func TestTemplateRule_ReadsALoopsItemOnlyInsideTheLoop(t *testing.T) {
	if got := templateFindings(t, `id: ok
tasks:
  - noop:
      note: "{{ item.name }} {{ loop.index }} {{ row.name }}"
    loop: [{name: a}]
    loop_control: {loop_var: row}
`); len(got) != 0 {
		t.Errorf("a loop's own variables were refused: %+v", got)
	}
	got := templateFindings(t, `id: bad
tasks:
  - noop:
      note: "{{ item }}"
`)
	if len(got) != 1 || !strings.Contains(got[0].Message, `reads "item"`) {
		t.Errorf("item outside a loop: %+v", got)
	}
	got = templateFindings(t, `id: expr
tasks:
  - noop: {}
    loop: "{{ item.names }}"
`)
	if len(got) != 1 || !strings.Contains(got[0].Message, "loop:") {
		t.Errorf("a loop reading its own item: %+v", got)
	}
}

func TestTemplateRule_LoopItemsInCommandTextMustBeQuoted(t *testing.T) {
	got := templateFindings(t, `id: cmd
tasks:
  - exec.shell:
      cmd: "echo {{ item }}"
    loop: [a]
`)
	if len(got) == 0 || !strings.Contains(got[0].Message, "quote") {
		t.Errorf("an unquoted item in command text: %+v", got)
	}
}

func TestUntilWarnings_NameAMethodThatChangesADevice(t *testing.T) {
	dag := yamlDAG(t, `id: u
tasks:
  - exec.shell:
      cmd: touch /tmp/x
    register: r
    until: ["result.r.exit_code == 0"]
  - noop: {}
    register: n
    until: ["true"]
`)
	got := validate.UntilWarnings(validate.WorldView{DAG: dag})
	if len(got) != 1 || got[0].Node != "tasks[0]" || !strings.Contains(got[0].Message, "repeats its change") {
		t.Errorf("warnings = %+v", got)
	}
}

func TestVarsRule_ReadsLateFactsTemplatedTargetsAndALoopExpression(t *testing.T) {
	world := func(runbook string) validate.WorldView {
		return validate.WorldView{DAG: yamlDAG(t, runbook), VariablesKnown: true}
	}
	// A global fact set after the task that reads it is said to come too late.
	got := validate.VarsRule(world(`id: late
tasks:
  - noop:
      v: "{{ vars.x }}"
  - set_fact: {x: 1, global: true}
`))
	if len(got) == 0 || !strings.Contains(got[0].Message, "vars.x") {
		t.Errorf("a late global fact: %+v", got)
	}
	// A target rendered from data could be any device, so none is assumed.
	tplWorld := world(`id: tpl
tasks:
  - noop: {}
    register: pick
  - noop:
      target: "{{ result.pick.name }}"
      v: "{{ vars.y }}"
    within: web
`)
	// The builder requires within: beside a rendered target, and a target that
	// names a device by within: is not a template; clearing it here asks what
	// the rule does with a target that is one.
	tplWorld.DAG.Nodes["tasks[1]"].Within = ""
	got = validate.VarsRule(tplWorld)
	if len(got) == 0 {
		t.Errorf("a templated target hid an undefined variable: %+v", got)
	}
	// The index form of a read is a read.
	got = validate.VarsRule(world("id: idx\ntasks:\n  - noop: {}\n    when_cel: 'vars[\"flag\"] == true'\n"))
	if len(got) == 0 || !strings.Contains(got[0].Message, "flag") {
		t.Errorf("an indexed read: %+v", got)
	}
	// "x" in vars guards a read.
	if got := validate.VarsRule(world("id: guard\ntasks:\n  - noop: {}\n    when_cel: '\"flag\" in vars'\n")); len(got) != 0 {
		t.Errorf("a membership test was read as a use: %+v", got)
	}
}

func TestTemplateRule_ALoopExpressionThatDoesNotCompile(t *testing.T) {
	got := templateFindings(t, "id: bad\ntasks:\n  - noop: {}\n    loop: \"{{ vars.a | no_such_filter }}\"\n")
	if len(got) != 1 || !strings.Contains(got[0].Message, "loop:") {
		t.Errorf("findings %+v", got)
	}
}

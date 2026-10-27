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

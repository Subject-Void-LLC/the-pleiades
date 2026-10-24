// A rollback: step's templates are held to TemplateRule's register check, as
// a task's are, so a misspelled register in an undo is found by validate
// rather than by the rollback that needs it (FAILURE_PATTERNS 574).
package validate_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/validate"
)

func TestTemplateRule_AMisspelledRegisterInARollbackStepIsFound(t *testing.T) {
	runbook := func(read string) string {
		return "id: r\nhosts: web\ntasks:\n  - name: read\n    noop:\n      x: 1\n    register: change\n" +
			"  - name: act\n    noop:\n      x: 2\n    rollback:\n      - noop:\n          msg: \"{{ result." + read + ".x }}\"\n"
	}
	if findings := validate.TemplateRule(validate.WorldView{DAG: yamlDAG(t, runbook("change"))}); len(findings) != 0 {
		t.Fatalf("a step reading a register the runbook writes was refused: %v", findings)
	}
	findings := validate.TemplateRule(validate.WorldView{DAG: yamlDAG(t, runbook("chnage"))})
	if len(findings) != 1 || findings[0].Node != "tasks[1].rollback[0]" || !strings.Contains(findings[0].Message, "chnage") {
		t.Errorf("a misspelled register in a rollback step: findings %+v, want one on tasks[1].rollback[0] naming it", findings)
	}
}

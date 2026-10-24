// Package validate_test: a project path the run writes is the author's to
// choose, never a device's, in a task and in its rollback: steps alike.
package validate_test

import (
	"strings"
	"testing"

	// The real method, whose path parameter declares the project path
	// format, rather than a fixture standing in for it.
	_ "github.com/Subject-Void-LLC/the-pleiades/internal/catalog/pleiades/builtin/project"
	"github.com/Subject-Void-LLC/the-pleiades/internal/validate"
)

// projectPathHead reads a value off a device and registers it, as the
// method's own example does.
const projectPathHead = "id: keep\ntasks:\n  - name: read r3\n    noop:\n      running_config: x\n    register: r3\n"

// TestTemplateRule_AProjectPathReadsOnlyVars: pleiades.builtin.project.write's
// path may render from vars, and is refused when it reads result, nodes or
// error, which hold what a device returned, so device output cannot choose
// which project file (the inventory, a runbook) is replaced. content may
// read a result as it always could. A rollback: step's path is held to the
// same rule.
func TestTemplateRule_AProjectPathReadsOnlyVars(t *testing.T) {
	for _, tc := range []struct {
		name string
		task string
		want string
	}{
		{"a literal path", "path: configs/r3.conf\n      content: \"{{ result.r3.running_config }}\"", ""},
		{"a path from vars", "path: \"configs/{{ vars.site }}/r3.conf\"\n      content: \"{{ result.r3.running_config }}\"", ""},
		{"a path from a result", "path: \"{{ result.r3.running_config }}\"\n      content: x", "may read only vars, never result"},
		{"a path from nodes", "path: \"configs/{{ nodes.r3 | to_json }}\"\n      content: x", "may read only vars, never nodes"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dag := yamlDAG(t, projectPathHead+"  - name: keep it\n    pleiades.builtin.project.write:\n      "+tc.task+"\n")
			findings := validate.TemplateRule(validate.WorldView{DAG: dag})
			switch {
			case tc.want == "" && len(findings) > 0:
				t.Fatalf("refused a path the author chose: %v", findings)
			case tc.want != "" && (len(findings) != 1 || !strings.Contains(findings[0].Message, tc.want) || !strings.Contains(findings[0].Message, "parameter path")):
				t.Fatalf("findings %v, want one on parameter path saying %q", findings, tc.want)
			}
		})
	}

	// A rollback: step renders when the task is undone, inside the run or by
	// pleiades rollback, and is held to the rule under its own name.
	step := yamlDAG(t, projectPathHead+"  - name: change\n    noop:\n      x: 1\n    rollback:\n      - pleiades.builtin.project.write:\n          path: \"{{ result.r3.running_config }}\"\n          content: x\n")
	findings := validate.TemplateRule(validate.WorldView{DAG: step})
	if len(findings) != 1 || findings[0].Node != "tasks[1].rollback[0]" || !strings.Contains(findings[0].Message, "may read only vars") {
		t.Errorf("a rollback step's path from a result: findings %v", findings)
	}
}

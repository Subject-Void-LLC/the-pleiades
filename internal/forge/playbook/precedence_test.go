// Tests that the converter raises vars.precedence exactly where Ansible and
// the runbook read a different value for one variable set at two scopes: a
// set_fact beside a block's or a task's vars. Every other mix of the scopes it
// carries (play, block, task vars, and a set_fact over play vars) reads the
// same value in both and is not reported.
package playbook_test

import (
	"slices"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/playbook"
	"github.com/Subject-Void-LLC/the-pleiades/internal/validate"
)

// TestPrecedence_OnlyWhereTheWinnerDiffers converts one playbook per mix of
// scopes and checks whether vars.precedence is raised, and that what it wrote
// builds and validates. A mix without a set_fact has no colliding form: in
// both orders the narrower of play, block and task wins.
func TestPrecedence_OnlyWhereTheWinnerDiffers(t *testing.T) {
	read := "{name: use, command: \"echo {{ mode }}\""
	cases := []struct {
		name  string
		play  string
		tasks string
		// differs says Ansible and the runbook read different values, so the
		// finding must be raised.
		differs bool
	}{
		{name: "play and task", play: "  vars: {mode: a}", tasks: "- " + read + ", vars: {mode: c}}"},
		{name: "play and block", play: "  vars: {mode: a}", tasks: "- name: group\n  vars: {mode: b}\n  block:\n    - " + read + "}"},
		{name: "block and task", tasks: "- name: group\n  vars: {mode: b}\n  block:\n    - " + read + ", vars: {mode: c}}"},
		{name: "play, block and task", play: "  vars: {mode: a}", tasks: "- name: group\n  vars: {mode: b}\n  block:\n    - " + read + ", vars: {mode: c}}"},
		{name: "set_fact and play", play: "  vars: {mode: a}", tasks: "- {name: set, set_fact: {mode: f}}\n- " + read + "}"},
		{name: "set_fact and task", differs: true, tasks: "- {name: set, set_fact: {mode: f}}\n- " + read + ", vars: {mode: c}}"},
		{name: "set_fact and block", differs: true, tasks: "- {name: set, set_fact: {mode: f}}\n- name: group\n  vars: {mode: b}\n  block:\n    - " + read + "}"},
		{name: "set_fact, play and task", differs: true, play: "  vars: {mode: a}", tasks: "- {name: set, set_fact: {mode: f}}\n- " + read + ", vars: {mode: c}}"},
		{name: "set_fact, play and block", differs: true, play: "  vars: {mode: a}", tasks: "- {name: set, set_fact: {mode: f}}\n- name: group\n  vars: {mode: b}\n  block:\n    - " + read + "}"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := convert(t, nativeCase{play: tc.play, tasks: tc.tasks}.wholePlaybook())
			codes := codesOf(res.Report)
			if got := slices.Contains(codes, playbook.Code("vars.precedence")); got != tc.differs {
				t.Errorf("vars.precedence raised = %v, want %v; findings %v\n%s", got, tc.differs, codes, res.Runbooks[0].YAML)
			}
			if res.Report.Counts.Blocked != 0 {
				t.Fatalf("blocked %d; findings %v", res.Report.Counts.Blocked, codes)
			}
			if report := validate.Validate(validate.WorldView{DAG: build(t, res.Runbooks[0].YAML)}); report.HasErrors() {
				t.Errorf("the converted runbook fails validation:\n%s\n%s", report.String(), res.Runbooks[0].YAML)
			}
		})
	}
}

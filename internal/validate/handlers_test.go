// Tests for HandlersRule through the real YAML builder, so the scopes and
// guards it reads are the ones a run gives.
package validate_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/validate"
)

// buildYAML builds a runbook through the real Builder.
func buildYAML(t *testing.T, src string) *engine.DAG {
	t.Helper()
	eval, err := engine.NewCELEvaluator()
	if err != nil {
		t.Fatal(err)
	}
	dag, err := engine.NewBuilder(eval).BuildFromYAML([]byte(src))
	if err != nil {
		t.Fatalf("building: %v\n%s", err, src)
	}
	return dag
}

// guardedRunbook is a block over web whose first task registers probe, with
// a rescue and a task after the block; rescue and after are the YAML each
// case puts there.
func guardedRunbook(rescue, after string) string {
	return `id: h
hosts: web
tasks:
  - name: guarded
    block:
      - name: probe it
        noop: {}
        register: probe
    rescue:
` + rescue + `
  - name: after
` + after
}

const plainRescue = "      - name: note it\n        noop: {}\n"

// TestHandlersRule covers what the rule refuses and what it lets through.
func TestHandlersRule(t *testing.T) {
	for _, tc := range []struct {
		name, runbook string
		want          string // a substring of the one finding; "" means no finding
	}{
		{"error in a rescue", guardedRunbook("      - name: tell\n        noop:\n          said: \"{{ error.message }}\"\n", "    noop: {}\n"), ""},
		{"error outside a rescue", guardedRunbook(plainRescue, "    noop:\n      said: \"{{ error.message }}\"\n"), "reads error"},
		{"error in a condition outside a rescue", guardedRunbook(plainRescue, "    noop: {}\n    when_cel: \"error.task != ''\"\n"), "condition reads error"},
		{"error in a rescue's condition", guardedRunbook("      - name: tell\n        noop: {}\n        when_cel: \"error.unreached\"\n", "    noop: {}\n"), ""},
		{"a register only the block writes, read after it", guardedRunbook(plainRescue, "    noop:\n      saw: \"{{ result.probe.ok }}\"\n"), `reads the register "probe"`},
		{"read in a condition", guardedRunbook(plainRescue, "    noop: {}\n    when_cel: \"result.probe.ok\"\n"), `reads the register "probe"`},
		{"the rescue registers it too", guardedRunbook("      - name: note it\n        noop: {}\n        register: probe\n", "    noop:\n      saw: \"{{ result.probe.ok }}\"\n"), ""},
		{"read with default", guardedRunbook(plainRescue, "    noop:\n      saw: \"{{ result.probe.ok | default(false) }}\"\n"), ""},
		{"tested with has", guardedRunbook(plainRescue, "    noop: {}\n    when_cel: \"has(result.probe) && result.probe.ok\"\n"), ""},
		{"read after a block that also has always", guardedRunbook(plainRescue+"    always:\n      - name: clean\n        noop: {}\n", "    noop:\n      saw: \"{{ result.probe.ok }}\"\n"), `reads the register "probe"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			findings := validate.HandlersRule(validate.WorldView{DAG: buildYAML(t, tc.runbook)})
			switch {
			case tc.want == "" && len(findings) != 0:
				t.Errorf("want no finding, got %v", findings)
			case tc.want != "" && (len(findings) != 1 || findings[0].RuleName != validate.HandlerRule || !strings.Contains(findings[0].Message, tc.want)):
				t.Errorf("want one finding saying %q, got %v", tc.want, findings)
			}
		})
	}

	// A block with no rescue: a failure ends the run before the task after
	// the block, so its read is never at risk.
	noRescue := strings.Replace(guardedRunbook(plainRescue, "    noop:\n      saw: \"{{ result.probe.ok }}\"\n"), "    rescue:\n"+plainRescue, "    always:\n"+plainRescue, 1)
	if findings := validate.HandlersRule(validate.WorldView{DAG: buildYAML(t, noRescue)}); len(findings) != 0 {
		t.Errorf("a block with no rescue: %v", findings)
	}
}

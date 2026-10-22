// Package validate_test: tests of the condition rule (FAILURE_PATTERNS 561).
package validate_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/validate"
)

// TestConditionRule: a condition naming a register no task writes is refused
// before the run, in every keyword and through every root and spelling a
// condition reads one by; a register some task writes, one written by the
// same name elsewhere, and a read no name can be read off all pass.
func TestConditionRule(t *testing.T) {
	const head = "id: t\ntasks:\n  - name: read the kernel\n    noop: {}\n    register: kernel\n"
	for _, tc := range []struct {
		name string
		task string
		want string
	}{
		{"result, written", "noop: {}\n    when: result.kernel.rc == 0", ""},
		{"result, misspelled", "noop: {}\n    when: result.kernal.rc == 0", `reads the register "kernal", and no task`},
		{"stat, misspelled", "noop: {}\n    when: stat.kernal.rc == 0", `"kernal"`},
		{"nodes indexed by a literal", "noop: {}\n    when: nodes['kernal'].rc == 0", `"kernal"`},
		{"when_or", "noop: {}\n    when_or:\n      - result.kernel.rc == 0\n      - result.kernal.rc == 0", `"kernal"`},
		{"when_cel", "noop: {}\n    when_cel: result.kernal.rc == 0", `"kernal"`},
		{"has() on a register nobody writes", "noop: {}\n    when: has(result.kernal)", ""},
		{"in on a register nobody writes", "noop: {}\n    when: \"\\\"kernal\\\" in stat\"", ""},
		{"has() beside a misspelled read", "noop: {}\n    when: has(result.kernel) && result.kernal.rc == 0", `"kernal"`},
		{"its own register", "noop: {}\n    register: mine\n    when: result.mine.rc == 0", "this task's own"},
		{"its own name, written earlier too", "noop: {}\n    register: kernel\n    when: result.kernel.rc == 0", ""},
		{"a computed index", "noop: {}\n    when: nodes[vars.which].rc == 0", ""},
		{"no condition", "noop: {}", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dag := yamlDAG(t, head+"  - name: the task\n    "+tc.task+"\n")
			findings := validate.ConditionRule(validate.WorldView{DAG: dag})
			switch {
			case tc.want == "" && len(findings) > 0:
				t.Fatalf("refused a condition that reads a written register: %v", findings)
			case tc.want != "" && (len(findings) != 1 || !strings.Contains(findings[0].Message, tc.want)):
				t.Fatalf("findings %v, want one saying %q", findings, tc.want)
			case tc.want != "" && (findings[0].RuleName != validate.ConditionRuleName || !strings.Contains(findings[0].Message, `"the task"`)):
				t.Fatalf("finding %+v does not name its rule and the task", findings[0])
			}
		})
	}
}

// TestConditionRule_AWriterTagSelectionLeftOut: a register whose only writer
// is tagged never, read behind has(), runs today and is not refused, though
// the selected DAG holds no writer (FAILURE_PATTERNS 561's review).
func TestConditionRule_AWriterTagSelectionLeftOut(t *testing.T) {
	dag := yamlDAG(t, "id: t\ntasks:\n  - name: debug dump\n    noop: {}\n    register: dump\n    tags: [never]\n  - name: report\n    noop: {}\n    when: has(stat.dump)\n")
	if findings := validate.ConditionRule(validate.WorldView{DAG: dag}); len(findings) != 0 {
		t.Fatalf("findings %v for a read written for absence", findings)
	}
}

// TestConditionRule_LeavesAnUnparsedConditionToTheBuilder: a DAG assembled
// by hand with a condition that does not parse gets no finding from this
// rule; parsing is the builder's refusal.
func TestConditionRule_LeavesAnUnparsedConditionToTheBuilder(t *testing.T) {
	dag := &engine.DAG{Nodes: map[string]*engine.Task{"a": {FQCN: "noop", Conditional: engine.Conditional{When: []string{"x =="}}}}}
	if findings := validate.ConditionRule(validate.WorldView{DAG: dag}); len(findings) != 0 {
		t.Fatalf("findings %v for a condition that does not parse", findings)
	}
}

// TestConditionRule_RunsInValidate: the rule is registered, so pleiades
// validate's own pass reports the misspelled register (FAILURE_PATTERNS
// 561's reproduction: the run failed, validation said nothing).
func TestConditionRule_RunsInValidate(t *testing.T) {
	dag := yamlDAG(t, "id: t\ntasks:\n  - name: read the kernel\n    noop: {}\n    register: kernel\n  - name: the task\n    noop: {}\n    when: result.kernal.rc == 0\n")
	for _, f := range validate.Validate(validate.WorldView{DAG: dag}).Findings {
		if f.RuleName == validate.ConditionRuleName {
			return
		}
	}
	t.Fatal("validate did not run the condition rule")
}

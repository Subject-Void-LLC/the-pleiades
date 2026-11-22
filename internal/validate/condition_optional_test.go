// Package validate_test: the plan-time rules that read a condition's
// references decide a condition written with CEL's optional syntax (.? and
// [?]) as they decide one written plainly, and a read written that way is a
// read written for absence (FAILURE_PATTERNS 1351).
package validate_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/validate"
)

// wantFinding fails t unless findings holds exactly one finding containing
// want, or none when want is empty.
func wantFinding(t *testing.T, findings []validate.Finding, want string) {
	t.Helper()
	switch {
	case want == "" && len(findings) != 0:
		t.Errorf("unexpected findings: %+v", findings)
	case want != "" && (len(findings) != 1 || !strings.Contains(findings[0].Message, want)):
		t.Errorf("findings = %+v, want one containing %q", findings, want)
	}
}

// TestConditionOptional_ConditionRule: an optional read of a register nobody
// writes is written for its absence, as has() is, and a plain misspelled read
// beside it is still refused.
func TestConditionOptional_ConditionRule(t *testing.T) {
	const head = "id: t\ntasks:\n  - name: read the kernel\n    noop: {}\n    register: kernel\n  - name: the task\n    noop: {}\n"
	for _, tc := range []struct{ name, cond, want string }{
		{"optional field", `when_cel: "result.?kernal.hasValue()"`, ""},
		{"optional index", `when_cel: "nodes[?'kernal'].hasValue()"`, ""},
		{"beside a misspelled read", `when_cel: "result.?kernel.hasValue() && result.kernal.rc == 0"`, `reads the register "kernal", and no task`},
		{"in a when item", `when: ["vars.?x.orValue(true) && stat.kernal.rc == 0"]`, `"kernal"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wantFinding(t, validate.ConditionRule(validate.WorldView{DAG: yamlDAG(t, head+"    "+tc.cond+"\n")}), tc.want)
		})
	}
}

// TestConditionOptional_HandlersRule: error read through the optional field
// outside a rescue is refused, an optional read of a register a rescued block
// may not have written is written for its absence, and a plain one beside an
// optional read of something else is still refused.
func TestConditionOptional_HandlersRule(t *testing.T) {
	for _, tc := range []struct{ name, after, want string }{
		{"error outside a rescue", "    noop: {}\n    when_cel: \"error.?task.hasValue()\"\n", "condition reads error"},
		{"an optional read of the register", "    noop: {}\n    when_cel: \"result.?probe.?ok.orValue(false)\"\n", ""},
		{"a plain read beside an optional one", "    noop: {}\n    when_cel: \"vars.?x.orValue(true) && result.probe.ok\"\n", `reads the register "probe"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wantFinding(t, validate.HandlersRule(validate.WorldView{DAG: buildYAML(t, guardedRunbook(plainRescue, tc.after))}), tc.want)
		})
	}
}

// TestConditionOptional_CheckModeRule: a real task reading a checked task's
// result through the optional syntax is refused, and one reading only a
// variable that way is not refused at all.
func TestConditionOptional_CheckModeRule(t *testing.T) {
	registerMethod(t, "ruleoptional.can", true)
	const probe = "id: r\ntasks:\n  - name: probe\n    fqcn: ruleoptional.can\n    register: drift\n    check_mode: true\n  - name: act\n    fqcn: noop\n"
	for _, tc := range []struct{ name, cond, want string }{
		{"reads the checked result", `when_cel: "result.?drift.hasValue()"`, `reads the result of a task that is only checked: "drift"`},
		{"by an optional index", `when_cel: "stat[?'drift'].hasValue()"`, `"drift"`},
		{"reads only a variable", `when_cel: "vars.?go.orValue(false)"`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wantFinding(t, validate.CheckModeRule(validate.WorldView{DAG: yamlDAG(t, probe+"    "+tc.cond+"\n")}), tc.want)
		})
	}
}

// TestConditionOptional_TemplateRuleLockAll: a task taking every device's
// lock together whose condition reads device through the optional syntax is
// refused, as the engine refuses it when it runs.
func TestConditionOptional_TemplateRuleLockAll(t *testing.T) {
	for _, tc := range []struct{ name, cond, want string }{
		{"optional field", `when_cel: "device.?name.hasValue()"`, "cannot take lock_acquisition: all_at_plan_time"},
		{"optional index in a when item", `when: ["device[?'name'].orValue('') == 'a'"]`, "cannot take lock_acquisition: all_at_plan_time"},
		{"control: reads no device", `when_cel: "vars.?x.orValue(true)"`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dag := yamlDAG(t, "id: l\nhosts: web\ntasks:\n  - name: all\n    noop: {}\n    lock_acquisition: all_at_plan_time\n    "+tc.cond+"\n")
			wantFinding(t, validate.TemplateRule(validate.WorldView{DAG: dag}), tc.want)
		})
	}
}

// TestConditionOptional_VarsRule: a variable read through the optional
// syntax is written for its absence and needs no value, and a plain read of
// a missing variable beside it is still refused.
func TestConditionOptional_VarsRule(t *testing.T) {
	for _, tc := range []struct{ name, cond, want string }{
		{"optional field", `when_cel: "vars.?region.orValue('x') == 'x'"`, ""},
		{"optional index", `when_cel: "vars[?'region'].hasValue()"`, ""},
		{"beside a missing read", `when_cel: "vars.?region.orValue('x') == 'x' && vars.typo == 1"`, "no value is supplied for vars.typo"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dag := yamlDAG(t, "id: v\ntasks:\n  - name: the task\n    noop: {}\n    "+tc.cond+"\n")
			wantFinding(t, validate.VarsRule(validate.WorldView{DAG: dag, VariablesKnown: true}), tc.want)
		})
	}
}

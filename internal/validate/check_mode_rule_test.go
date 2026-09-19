// Package validate_test: tests of the check_mode rule.
package validate_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/validate"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// yamlDAG compiles a YAML runbook through the real Builder.
func yamlDAG(t *testing.T, payload string) *engine.DAG {
	t.Helper()
	eval, err := engine.NewCELEvaluator()
	if err != nil {
		t.Fatal(err)
	}
	dag, err := engine.NewBuilder(eval).BuildFromYAML([]byte(payload))
	if err != nil {
		t.Fatalf("building the runbook: %v", err)
	}
	return dag
}

// registerMethod registers a fixture Collection method, with or without
// check support, for the rest of the test.
func registerMethod(t *testing.T, name string, supportsCheck bool) {
	t.Helper()
	t.Cleanup(collection.SnapshotForTest())
	run := func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (collection.Result, error) {
		return collection.Result{}, nil
	}
	d := collection.Descriptor{
		Name: name,
		Manifest: collection.Manifest{
			Status:        collection.StatusImplemented,
			Reversibility: collection.Reversibility{Notes: "a test fixture"},
			SupportsCheck: supportsCheck,
		},
		Invoke: run,
	}
	if supportsCheck {
		d.Check = run
	}
	if err := collection.Register(d); err != nil {
		t.Fatal(err)
	}
}

// TestCheckModeRule_RefusesACheckThatCannotHappen covers the first half:
// check_mode on a task whose action has no check is refused, naming the
// task, unless the whole runbook is a check.
func TestCheckModeRule_RefusesACheckThatCannotHappen(t *testing.T) {
	registerMethod(t, "rulecheck.can", true)
	registerMethod(t, "rulecheck.cannot", false)

	cases := []struct {
		name    string
		runbook string
		refused string
	}{
		{name: "checkable", runbook: "id: r\ntasks:\n  - name: a\n    fqcn: rulecheck.can\n    check_mode: true\n"},
		{name: "built-in noop", runbook: "id: r\ntasks:\n  - name: a\n    fqcn: noop\n    check_mode: true\n"},
		{name: "method without check", runbook: "id: r\ntasks:\n  - name: a\n    fqcn: rulecheck.cannot\n    check_mode: true\n", refused: "rulecheck.cannot declares no check support"},
		{name: "through a block", runbook: "id: r\ntasks:\n  - name: g\n    check_mode: true\n    block:\n      - name: a\n        fqcn: rulecheck.cannot\n", refused: "declares no check support"},
		{name: "a transport action", runbook: "id: r\ntasks:\n  - name: a\n    fqcn: ssh_exec\n    check_mode: true\n    params:\n      cmd: uptime\n", refused: "ssh_exec is neither"},
		{name: "runbook level is a whole check", runbook: "id: r\ncheck_mode: true\ntasks:\n  - name: a\n    fqcn: rulecheck.cannot\n"},
		{name: "no key", runbook: "id: r\ntasks:\n  - name: a\n    fqcn: rulecheck.cannot\n"},

		// Methods whose check covers only some calls, the real ones: the
		// call's own parameters decide, and the method's own reason is
		// what the author reads.
		{name: "a guarded command", runbook: "id: r\ntasks:\n  - name: a\n    fqcn: exec.command\n    check_mode: true\n    params:\n      cmd: /bin/true\n      creates: /etc/done\n"},
		{name: "a guarded shell line", runbook: "id: r\ntasks:\n  - name: a\n    fqcn: exec.shell\n    check_mode: true\n    params:\n      cmd: rm -f /tmp/x\n      removes: /tmp/x\n"},
		{name: "an unguarded command", runbook: "id: r\ntasks:\n  - name: a\n    fqcn: exec.command\n    check_mode: true\n    params:\n      cmd: /bin/true\n", refused: "exec.command cannot check this call: what a command changes cannot be known without running it"},
		{name: "an unguarded shell line", runbook: "id: r\ntasks:\n  - name: a\n    fqcn: exec.shell\n    check_mode: true\n    params:\n      cmd: date\n", refused: "exec.shell cannot check this call"},
		{name: "a GET by default", runbook: "id: r\ntasks:\n  - name: a\n    fqcn: http.request\n    check_mode: true\n    params:\n      url: https://example.invalid/\n"},
		{name: "a POST", runbook: "id: r\ntasks:\n  - name: a\n    fqcn: http.request\n    check_mode: true\n    params:\n      url: https://example.invalid/\n      method: post\n", refused: "a POST request may change something on the server"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			findings := validate.CheckModeRule(validate.WorldView{DAG: yamlDAG(t, tc.runbook)})
			if tc.refused == "" {
				if len(findings) != 0 {
					t.Errorf("unexpected findings: %+v", findings)
				}
				return
			}
			if len(findings) != 1 || !strings.Contains(findings[0].Message, tc.refused) {
				t.Errorf("findings = %+v, want one containing %q", findings, tc.refused)
			}
		})
	}
}

// TestCheckModeRule_AWholeCheckStillNamesWhatItCannotCheck is the
// regression test for the rule first refusing every uncheckable task of a
// `--mode check` run: only a task carrying check_mode itself promised a
// check, so a whole check keeps reporting the others as unchecked at run
// time, while a task whose own key cannot be honored is still refused.
func TestCheckModeRule_AWholeCheckStillNamesWhatItCannotCheck(t *testing.T) {
	registerMethod(t, "rulewhole.cannot", false)
	plain := yamlDAG(t, "id: r\ntasks:\n  - name: a\n    fqcn: rulewhole.cannot\n")
	if findings := validate.CheckModeRule(validate.WorldView{DAG: plain, Mode: collection.ModeCheck}); len(findings) != 0 {
		t.Errorf("a --mode check run of an uncheckable task was refused: %+v", findings)
	}
	keyed := yamlDAG(t, "id: r\ntasks:\n  - name: a\n    fqcn: rulewhole.cannot\n    check_mode: true\n")
	if findings := validate.CheckModeRule(validate.WorldView{DAG: keyed, Mode: collection.ModeCheck}); len(findings) != 1 {
		t.Errorf("a task's own impossible check_mode was not refused in a --mode check run: %+v", findings)
	}
}

// TestCheckModeRule_ARealChangeIsNeverDecidedByAPrediction covers the
// second half: in a real run, a task that runs for real may not read a
// checked task's registered result, by name or by any access a name
// cannot be read from. The controls are the same runbooks with the reader
// checked too, or run as a whole check, or reading an unchecked task.
func TestCheckModeRule_ARealChangeIsNeverDecidedByAPrediction(t *testing.T) {
	registerMethod(t, "rulepredict.can", true)
	const probe = "id: r\ntasks:\n  - name: probe\n    fqcn: rulepredict.can\n    register: drift\n    check_mode: true\n"

	cases := []struct {
		name    string
		runbook string
		mode    collection.Mode
		refused string
	}{
		{name: "reads the checked result", runbook: probe + "  - name: act\n    fqcn: noop\n    when: stat.drift[\"\"].changed\n", refused: `"drift" (from tasks[0] (name "probe"))`},
		{name: "through nodes", runbook: probe + "  - name: act\n    fqcn: noop\n    when_cel: nodes.drift[\"\"].changed\n", refused: `"drift"`},
		{name: "a computed index", runbook: probe + "  - name: act\n    fqcn: noop\n    when_cel: stat[vars.which][\"\"].changed\n", refused: "no register name can be read"},
		{name: "the reader is checked too", runbook: probe + "  - name: act\n    fqcn: noop\n    check_mode: true\n    when: stat.drift[\"\"].changed\n"},
		{name: "the run is a check", runbook: probe + "  - name: act\n    fqcn: noop\n    when: stat.drift[\"\"].changed\n", mode: collection.ModeCheck},
		{name: "reads an executed result", runbook: "id: r\ntasks:\n  - name: probe\n    fqcn: noop\n    register: drift\n  - name: act\n    fqcn: noop\n    when: stat.drift[\"\"].changed\n"},
		{name: "reads something else", runbook: probe + "  - name: act\n    fqcn: noop\n    when: vars.go == true\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			findings := validate.CheckModeRule(validate.WorldView{DAG: yamlDAG(t, tc.runbook), Mode: tc.mode})
			if tc.refused == "" {
				if len(findings) != 0 {
					t.Errorf("unexpected findings: %+v", findings)
				}
				return
			}
			if len(findings) != 1 || !strings.Contains(findings[0].Message, tc.refused) {
				t.Errorf("findings = %+v, want one containing %q", findings, tc.refused)
			}
		})
	}
}

// TestLifecycleRule_ACheckModeTaskMayCheckASimulateLockedDevice proves the
// lifecycle rule reads each task's own mode: in a real run, a task
// carrying check_mode may target a simulate-locked device, and the same
// task without it may not.
func TestLifecycleRule_ACheckModeTaskMayCheckASimulateLockedDevice(t *testing.T) {
	dev := &inventorytest.Stub{StubName: "web1", StubState: inventory.StateSimulateLocked}
	for _, tc := range []struct {
		key     string
		refused bool
	}{
		{key: "    check_mode: true\n", refused: false},
		{key: "", refused: true},
	} {
		dag := yamlDAG(t, "id: r\ntasks:\n  - name: a\n    fqcn: noop\n"+tc.key+"    params:\n      target: web1\n")
		findings := validate.LifecycleRule(validate.WorldView{Items: []inventory.InventoryItem{dev}, DAG: dag, Mode: collection.ModeExecute})
		if got := len(findings) > 0; got != tc.refused {
			t.Errorf("with key %q: refused = %v, want %v (%+v)", tc.key, got, tc.refused, findings)
		}
	}
}

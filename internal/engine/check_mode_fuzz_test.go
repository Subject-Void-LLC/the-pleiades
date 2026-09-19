// Package engine_test: a fuzz of the check_mode runbook key.
package engine_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
)

// FuzzCheckModeKey feeds the real YAML builder runbooks carrying
// check_mode at odd depths and with odd values. Whatever it is given, the
// builder must not panic, must never mark a task checked unless the
// runbook said check_mode somewhere, and when the runbook-level key is
// set must mark every task. A false value, in any spelling, must never
// build.
func FuzzCheckModeKey(f *testing.F) {
	for _, seed := range []string{
		"id: a\ntasks:\n  - name: t\n    fqcn: noop\n    check_mode: true\n",
		"id: a\ncheck_mode: yes\ntasks:\n  - name: t\n    fqcn: noop\n",
		"id: a\ntasks:\n  - name: b\n    check_mode: on\n    block:\n      - name: t\n        fqcn: noop\n    rescue:\n      - name: r\n        fqcn: noop\n",
		"id: a\ntasks:\n  - name: p\n    check_mode: true\n    parallel:\n      - name: t\n        fqcn: noop\n",
		"id: a\ntasks:\n  - name: t\n    fqcn: noop\n    check_mode: false\n",
		"id: a\ntasks:\n  - name: t\n    fqcn: noop\n    check_mode: [true]\n",
		"id: a\ntasks:\n  - name: t\n    fqcn: noop\n    check_mode: \"{{ x }}\"\n",
		"id: a\ntasks:\n  - name: t\n    fqcn: noop\n    check_mode: &x true\n  - name: u\n    fqcn: noop\n    check_mode: *x\n",
		"id: a\ncheck_mode: NO\ntasks:\n  - name: t\n    fqcn: noop\n",
		"id: a\ntasks:\n  - name: t\n    noop: {}\n    check_mode: t\n",
	} {
		f.Add(seed)
	}
	eval, err := engine.NewCELEvaluator()
	if err != nil {
		f.Fatal(err)
	}
	b := engine.NewBuilder(eval)
	f.Fuzz(func(t *testing.T, runbook string) {
		dag, err := b.BuildFromYAML([]byte(runbook))
		if err != nil {
			return
		}
		mentioned := strings.Contains(runbook, "check_mode")
		for id, task := range dag.Nodes {
			if bool(task.CheckMode) && !mentioned {
				t.Fatalf("%s is checked, but the runbook never says check_mode:\n%s", id, runbook)
			}
			if dag.CheckMode && task.FQCN != "" && !bool(task.CheckMode) {
				t.Fatalf("the runbook-level check_mode did not reach %s:\n%s", id, runbook)
			}
		}
		if dag.CheckMode && !mentioned {
			t.Fatalf("the runbook is a check without saying check_mode:\n%s", runbook)
		}
	})
}

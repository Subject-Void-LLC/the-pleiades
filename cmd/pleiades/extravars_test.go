// Tests for run's --extra-vars, through the real binary: a runbook's
// condition reads what the run was started with.
package main_test

import (
	"strings"
	"testing"
)

// extraVarsRunbook runs one task only when vars.ticket names INC1, and has
// no hosts, so it needs no device.
const extraVarsRunbook = "id: ev\ntasks:\n  - name: act on the ticket\n    noop:\n      changed: true\n    when_cel: \"has(vars.ticket) && vars.ticket == 'INC1'\"\n"

// TestCLI_ExtraVarsReachConditions: before Phase 117a, `pleiades run` took
// no variables at all and vars was always empty on the Crawl tier. Each way
// of giving one reaches the condition; a name given twice, a file that is
// not a mapping and a bad name are refused before anything runs.
func TestCLI_ExtraVarsReachConditions(t *testing.T) {
	dir := t.TempDir()
	if out, err := runPleiades(t, dir, "init"); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	writeFile(t, dir, "runbooks/ev.yaml", extraVarsRunbook)
	writeFile(t, dir, "ticket.yaml", "ticket: INC1\n")
	writeFile(t, dir, "list.yaml", "- not a mapping\n")

	statusOf := func(args ...string) string {
		t.Helper()
		rep, out, _ := runPleiadesJSON(t, dir, append([]string{"run", "runbooks/ev.yaml"}, args...)...)
		if len(rep.Tasks) != 1 {
			t.Fatalf("%v: %d tasks reported\n%s", args, len(rep.Tasks), out)
		}
		return rep.Tasks[0].Status
	}
	for _, args := range [][]string{{"--extra-vars", "ticket=INC1"}, {"-e", "ticket=INC1"}, {"-e", "@ticket.yaml"}, {"-e", "ticket:=INC1"}} {
		if got := statusOf(args...); got != "changed" {
			t.Errorf("%v: the task is %s, want changed: the condition did not read the variable", args, got)
		}
	}
	for _, args := range [][]string{{"-e", "ticket=INC2"}, nil} {
		if got := statusOf(args...); got != "skipped" {
			t.Errorf("%v: the task is %s, want skipped", args, got)
		}
	}
	for _, args := range [][]string{
		{"-e", "ticket=INC1", "-e", "@ticket.yaml"},
		{"-e", "@list.yaml"},
		{"-e", "bad-name=x"},
		{"-e", "@missing.yaml"},
	} {
		out, err := runPleiades(t, dir, append([]string{"run", "runbooks/ev.yaml"}, args...)...)
		if err == nil || !strings.Contains(out, "extra-vars") {
			t.Errorf("%v: err = %v, want a refusal naming --extra-vars:\n%s", args, err, out)
		}
	}
}

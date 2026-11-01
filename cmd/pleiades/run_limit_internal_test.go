// Tests of --limit on the CLI: which devices a limited run reaches, and what
// is refused.
package main

import (
	"context"
	"flag"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/lock"
)

// limitedRun builds runbook over serialWorld, narrows it to limit and runs
// it, returning what ran.
func limitedRun(t *testing.T, runbook, limit string) ([]string, error) {
	t.Helper()
	eval, err := engine.NewCELEvaluator()
	if err != nil {
		t.Fatal(err)
	}
	dag, err := engine.NewBuilder(eval).BuildFromYAML([]byte(runbook))
	if err != nil {
		t.Fatal(err)
	}
	l, err := engine.ParseLimit(limit)
	if err != nil {
		t.Fatal(err)
	}
	world, err := applyLimit(serialWorld(), dag, l, limit)
	if err != nil {
		return nil, err
	}
	log := &batchLog{}
	x := engine.NewExecutor(world, log, lock.NewInProcessManager(), event.NewInProcessBus(), engine.NewInProcessWorkflowContext(), 0)
	if result, err := x.Run(context.Background(), dag); err != nil || result.HasErrors() {
		t.Fatalf("run: %v, %+v", err, result.Nodes)
	}
	return sortedLevels(log.order), nil
}

// TestLimit_NarrowsWhatARunReaches: a name list, a glob with an exclusion,
// and a tag intersection each reach only what they say among the hosts, and a
// task naming a device outside the hosts still reaches it, as Ansible's limit
// leaves delegate_to alone.
func TestLimit_NarrowsWhatARunReaches(t *testing.T) {
	const runbook = "id: l\nhosts: web\ntasks:\n  - name: a\n    noop: {}\n"
	for limit, want := range map[string]string{
		"w1,w3":      "a@w1,a@w3",
		"w*,!w2,!w5": "a@w1,a@w3,a@w4",
		"web,&w4":    "a@w4",
	} {
		got, err := limitedRun(t, runbook, limit)
		if err != nil || strings.Join(got, ",") != want {
			t.Errorf("--limit %q ran %v (%v), want %s", limit, got, err, want)
		}
	}
	got, err := limitedRun(t, "id: l\nhosts: web\ntasks:\n  - name: lab\n    noop:\n      target: lab\n  - name: a\n    noop: {}\n", "w1")
	if err != nil || strings.Join(got, ",") != "lab@lab,a@w1" {
		t.Errorf("--limit w1 with a lab task ran %v (%v), want lab@lab,a@w1", got, err)
	}
}

// TestLimit_RefusesALimitThatMatchesNothing and a flag that does not parse,
// and rollback refuses the flag outright.
func TestLimit_RefusesALimitThatMatchesNothing(t *testing.T) {
	if _, err := limitedRun(t, "id: l\nhosts: web\ntasks:\n  - name: a\n    noop: {}\n", "db-*"); err == nil || !strings.Contains(err.Error(), `--limit "db-*" matches none of the runbook's hosts`) {
		t.Errorf("a limit matching nothing: %v", err)
	}
	f := addRunFlags(newFlagSetForTest())
	*f.limit = "~web"
	if _, err := f.options(); err == nil || !strings.Contains(err.Error(), "--limit") {
		t.Errorf("a limit that does not parse: %v", err)
	}
	if err := runRollback([]string{"00000000-0000-0000-0000-000000000000", "--limit", "w1", "--dir", t.TempDir()}); err == nil || !strings.Contains(err.Error(), "--limit is not taken") {
		t.Errorf("rollback with --limit: %v", err)
	}
}

// newFlagSetForTest is a fresh flag set that reports instead of exiting.
func newFlagSetForTest() *flag.FlagSet { return flag.NewFlagSet("test", flag.ContinueOnError) }

// Tests of the facts root in a check (facts_check.go): a checked task reading
// a fact an earlier checked task would emit is unchecked, a read-only gather's
// check is an observation later checked tasks read, and a real task reads the
// store whatever was checked before it.
package engine_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// installThenRead installs a package, then probes where its fact says so.
const installThenRead = `
id: install-then-read
hosts: fleet
tasks:
  - name: install
    tfx.install: {name: nginx}
  - name: configure
    when: facts["tfx.pkg.nginx"] is defined
    tfx.probe: {}
`

// storedNginx is a store holding the package on both devices, under a value
// no ledger may carry.
func storedNginx() *fakeReader {
	a := engine.FactAnswer{State: engine.FactPresent, Value: map[string]any{"version": storedNeedle}}
	return &fakeReader{answers: map[string]map[string]engine.FactAnswer{"dev-1": {"tfx.pkg.nginx": a}, "dev-2": {"tfx.pkg.nginx": a}}}
}

// TestFactsCheck_ACheckedEmitterPredictingNoChangeStillTaints: the install
// predicts no change, yet a real run would store its fact, so the task
// reading it is unchecked by its family's declaration: never checked, never
// skipped as decided, and its reason names the tasks, the method and the
// family, never the stored value.
func TestFactsCheck_ACheckedEmitterPredictingNoChangeStillTaints(t *testing.T) {
	m := registerFactsRead(t)
	result, err := newCheckExecutor(factsFleet(), engine.WithFactReader(storedNginx()), engine.WithMode(collection.ModeCheck)).Run(
		context.Background(), factsDAG(t, installThenRead))
	if err != nil {
		t.Fatal(err)
	}
	runs := resultsOf(result, "tasks[1]")
	if len(runs) != 2 {
		t.Fatalf("results %v, want one per device", runs)
	}
	for device, n := range runs {
		if !n.Unchecked || !n.Skipped || n.Err != nil {
			t.Errorf("on %s: %s, want it unchecked", device, describe(n))
		}
		for _, want := range []string{`facts["tfx.pkg.nginx"]`, `tasks[0] (name "install")`, "tfx.install", `"tfx.pkg."*`, "a check stores no fact"} {
			if !strings.Contains(n.SkipReason, want) {
				t.Errorf("on %s the reason %q does not say %q", device, n.SkipReason, want)
			}
		}
		if strings.Contains(n.SkipReason, storedNeedle) {
			t.Errorf("the reason carries the stored value: %q", n.SkipReason)
		}
	}
	if m.installChecks.Load() != 2 || m.probeChecks.Load() != 0 || m.probeRuns.Load() != 0 {
		t.Errorf("install checked %d times, probe checked %d and run %d: want the install checked on each device and the reader neither",
			m.installChecks.Load(), m.probeChecks.Load(), m.probeRuns.Load())
	}
}

// TestFactsCheck_ARealTaskAfterACheckedOneReadsTheStore: in a real run whose
// install is only checked, the real task reading the package's fact reads
// the store as it is and runs; nothing a check did taints it.
func TestFactsCheck_ARealTaskAfterACheckedOneReadsTheStore(t *testing.T) {
	m := registerFactsRead(t)
	runbook := strings.Replace(installThenRead, "    tfx.install: {name: nginx}", "    check_mode: true\n    tfx.install: {name: nginx}", 1)
	result, err := newCheckExecutor(factsFleet(), engine.WithFactReader(storedNginx())).Run(context.Background(), factsDAG(t, runbook))
	if err != nil {
		t.Fatal(err)
	}
	for device, n := range resultsOf(result, "tasks[1]") {
		if n.Unchecked || n.Skipped || n.Err != nil || n.Checked {
			t.Errorf("the real reader on %s: %s, want it run for real", device, describe(n))
		}
	}
	if m.probeRuns.Load() != 2 {
		t.Errorf("the real reader ran %d times, want once on each device", m.probeRuns.Load())
	}
}

// TestFactsCheck_AnObservedGatherIsReadableByCheckedTasksOnly: a read-only
// gather's check reads the device, so what it emits is read by a later
// checked task, in a check and in a real run's check_mode task, and is never
// stored; a real task reads only the store, so on a device no run gathered it
// fails as unknown rather than reading the observation.
func TestFactsCheck_AnObservedGatherIsReadableByCheckedTasksOnly(t *testing.T) {
	m := registerFactsRead(t)
	sink := &factSink{got: map[string][]facts.Entry{}}
	check, err := newCheckExecutor(factsFleet(), engine.WithFactReader(&fakeReader{}), engine.WithFactRecorder(sink), engine.WithMode(collection.ModeCheck)).Run(
		context.Background(), factsDAG(t, gatherThenRead))
	if err != nil {
		t.Fatal(err)
	}
	for device, n := range resultsOf(check, "tasks[1]") {
		if n.Unchecked || n.Skipped || n.Err != nil || !n.Checked {
			t.Errorf("the checked reader of the observed 6.1 on %s: %s, want it checked", device, describe(n))
		}
	}
	for device, n := range resultsOf(check, "tasks[2]") {
		if !n.Skipped || n.Unchecked || n.Err != nil {
			t.Errorf("the checked reader of 5.0 on %s: %s, want it skipped as decided", device, describe(n))
		}
	}
	if m.probeChecks.Load() != 2 {
		t.Errorf("the probe was checked %d times, want once on each device", m.probeChecks.Load())
	}

	checkedGather := strings.Replace(gatherThenRead, "    tfx.gather: {}", "    check_mode: true\n    tfx.gather: {}", 1)
	real, err := newCheckExecutor(factsFleet(), engine.WithFactReader(&fakeReader{}), engine.WithFactRecorder(sink)).Run(context.Background(), factsDAG(t, checkedGather))
	if err != nil {
		t.Fatal(err)
	}
	for device, n := range resultsOf(real, "tasks[1]") {
		if n.Err == nil || !strings.Contains(n.Err.Error(), "says whether the device has it") {
			t.Errorf("the real reader after a checked gather on %s: %s, want it failed as unknown", device, describe(n))
		}
	}
	// The same run's checked task reads the observation.
	before := m.probeChecks.Load()
	checkedRead := strings.Replace(checkedGather, "  - name: on six\n", "  - name: on six\n    check_mode: true\n", 1)
	mixed, err := newCheckExecutor(factsFleet(), engine.WithFactReader(&fakeReader{}), engine.WithFactRecorder(sink)).Run(context.Background(), factsDAG(t, checkedRead))
	if err != nil {
		t.Fatal(err)
	}
	for device, n := range resultsOf(mixed, "tasks[1]") {
		if n.Unchecked || n.Skipped || n.Err != nil || !n.Checked {
			t.Errorf("a check_mode reader after a checked gather on %s: %s, want it checked against the observation", device, describe(n))
		}
	}
	if got := m.probeChecks.Load() - before; got != 2 {
		t.Errorf("the check_mode reader was checked %d times, want once on each device", got)
	}
	if len(sink.got) != 0 {
		t.Errorf("an observation was stored: %v", sink.got)
	}
}

// TestFactsCheck_AnObserverWhoseCheckDidNotRunTaints: a read-only gather
// whose own condition the check could not decide observed nothing, so a
// checked task reading what it would observe is unchecked on that device.
func TestFactsCheck_AnObserverWhoseCheckDidNotRunTaints(t *testing.T) {
	registerFactsRead(t)
	uncheckable := registerCheckedMethod(t, "factsnocheck", false, false, false)
	result, err := newCheckExecutor(factsFleet(), engine.WithFactReader(&fakeReader{}), engine.WithMode(collection.ModeCheck)).Run(context.Background(), factsDAG(t, `
id: observer-missed
hosts: fleet
tasks:
  - name: before
    register: u
    `+uncheckable.name+`: {}
  - name: gather
    when: nodes.u["dev-1"].seen == "x"
    tfx.gather: {}
  - name: read
    when: facts["tfx.kernel"] == "6.1"
    tfx.probe: {}
`))
	if err != nil {
		t.Fatal(err)
	}
	for device, n := range resultsOf(result, "tasks[2]") {
		if !n.Unchecked || !strings.Contains(n.SkipReason, "observes through tfx.gather, and that task's check did not run on this device") {
			t.Errorf("the reader on %s: %s, want it unchecked for the observer that did not run", device, describe(n))
		}
	}
}

// TestFactsCheck_AnUnknownFactFailsACheckAsItWouldARealRun: a check reading
// a key no task of it emits, on a device never gathered, fails the task: it
// is not a gap in the check but the real run's own failure.
func TestFactsCheck_AnUnknownFactFailsACheckAsItWouldARealRun(t *testing.T) {
	registerFactsRead(t)
	result, err := newCheckExecutor(factsFleet(), engine.WithFactReader(&fakeReader{}), engine.WithMode(collection.ModeCheck)).Run(context.Background(), factsDAG(t, `
id: check-unknown
hosts: fleet
tasks:
  - name: read
    when: facts["tfx.kernel"] is not defined
    tfx.probe: {}
`))
	if err != nil {
		t.Fatal(err)
	}
	for device, n := range resultsOf(result, "tasks[0]") {
		if n.Err == nil || n.Unchecked {
			t.Errorf("on %s: %s, want the task failed", device, describe(n))
		}
	}
}

// TestCheckedFactReads_TheRuleValidationAndTheExecutorShare: the plan-time
// function names, for each checked reader, every earlier checked emitter and
// whether it observes; a real reader, and an emitter in the reader's own
// level or after it, are not named.
func TestCheckedFactReads_TheRuleValidationAndTheExecutorShare(t *testing.T) {
	registerFactsRead(t)
	dag := factsDAG(t, `
id: plan
hosts: fleet
tasks:
  - name: gather
    tfx.gather: {}
  - name: install
    tfx.install: {name: nginx}
  - name: read
    when_cel: 'facts["tfx.pkg.nginx"].version == "1" && facts["tfx.kernel"] == "6.1"'
    tfx.probe: {}
  - name: later
    tfx.install: {name: nginx}
`)
	if got := engine.CheckedFactReads(collection.ModeExecute, dag); len(got) != 0 {
		t.Errorf("a real run's taints = %v, want none", got)
	}
	taints := engine.CheckedFactReads(collection.ModeCheck, dag)["tasks[2]"]
	if len(taints) != 2 {
		t.Fatalf("taints = %+v, want the gather's kernel and the install's package", taints)
	}
	for _, ft := range taints {
		switch ft.Emitter {
		case "tasks[0]":
			if ft.Key != "tfx.kernel" || !ft.Observes {
				t.Errorf("the gather's taint %+v, want the kernel, observed", ft)
			}
		case "tasks[1]":
			if ft.Key != "tfx.pkg.nginx" || ft.Observes || ft.Declared != `"tfx.pkg."*` {
				t.Errorf("the install's taint %+v, want the package by its family, not observed", ft)
			}
		default:
			t.Errorf("a taint from %s, which does not come before the reader", ft.Emitter)
		}
	}
}

// TestFactsCheck_AKeyAnObservingGatherDidNotFindIsAbsent: a checked task
// reading a key the read-only gather declares and did not find on the device
// reads it as absent, as a real run would after that gather.
func TestFactsCheck_AKeyAnObservingGatherDidNotFindIsAbsent(t *testing.T) {
	registerFactsRead(t)
	result, err := newCheckExecutor(factsFleet(), engine.WithFactReader(&fakeReader{}), engine.WithMode(collection.ModeCheck)).Run(context.Background(), factsDAG(t, `
id: observed-absent
hosts: fleet
tasks:
  - name: gather
    tfx.gather: {}
  - name: no codename
    when: facts["tfx.codename"] is not defined
    tfx.probe: {}
`))
	if err != nil {
		t.Fatal(err)
	}
	for device, n := range resultsOf(result, "tasks[1]") {
		if n.Skipped || n.Unchecked || n.Err != nil {
			t.Errorf("on %s: %s, want it checked, the codename absent", device, describe(n))
		}
	}
}

// TestFactsCheck_AReadOnlyMethodWhoseCheckIsNotItsRunDoesNotObserve: a
// read-only method whose Check is another function than its Invoke predicts
// rather than reads, so what it emits during a check is no observation, and
// a checked task reading it is unchecked.
func TestFactsCheck_AReadOnlyMethodWhoseCheckIsNotItsRunDoesNotObserve(t *testing.T) {
	registerFactsRead(t)
	emit := func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (collection.Result, error) {
		return collection.Result{}, nil
	}
	predict := func(_ context.Context, rc sdk.RunbookContext, _ inventory.InventoryItem, _ map[string]any) (collection.Result, error) {
		return collection.Result{}, rc.EmitFact("tfx.kernel", "6.1")
	}
	if err := collection.Register(collection.Descriptor{Name: "tfx.peek", Invoke: emit, Check: predict,
		Manifest: collection.Manifest{Status: collection.StatusImplemented, SupportsCheck: true, Reversibility: collection.Reversibility{ReadOnly: true, Notes: "a fixture"},
			Facts: []collection.FactSpec{{Key: "tfx.kernel", Kind: collection.FactStable, Type: collection.FactString, Description: "the kernel"}}}}); err != nil {
		t.Fatal(err)
	}
	if engine.ObservesInCheck("tfx.peek") || !engine.ObservesInCheck("tfx.gather") {
		t.Fatal("ObservesInCheck does not tell a Check that is its run from one that is not")
	}
	result, err := newCheckExecutor(factsFleet(), engine.WithFactReader(&fakeReader{}), engine.WithMode(collection.ModeCheck)).Run(context.Background(), factsDAG(t, `
id: peek
hosts: fleet
tasks:
  - name: peek
    tfx.peek: {}
  - name: read
    when: facts["tfx.kernel"] == "6.1"
    tfx.probe: {}
`))
	if err != nil {
		t.Fatal(err)
	}
	for device, n := range resultsOf(result, "tasks[1]") {
		if !n.Unchecked {
			t.Errorf("on %s: %s, want it unchecked", device, describe(n))
		}
	}
}

// TestFactsCheck_AResultDecidedWithoutATaintedKeyIsChecked: a key an earlier
// checked task would emit is unknown to a check by the same three-valued
// rule, so a reader whose result CEL decides without it is checked or skipped
// as decided, as the real run would decide it; only a result that depends on
// it is a gap.
func TestFactsCheck_AResultDecidedWithoutATaintedKeyIsChecked(t *testing.T) {
	for name, tc := range map[string]struct {
		cond               string
		unchecked, skipped bool
	}{
		"depends on the tainted key": {`when: facts["tfx.pkg.nginx"] is defined`, true, true},
		"a false item decides":       {"when:\n      - \"false\"\n      - facts[\"tfx.pkg.nginx\"] is defined", false, true},
		"a true alternative decides": {`when_cel: 'true || facts[?"tfx.pkg.nginx"].hasValue()'`, false, false},
	} {
		t.Run(name, func(t *testing.T) {
			m := registerFactsRead(t)
			runbook := strings.Replace(installThenRead, `    when: facts["tfx.pkg.nginx"] is defined`, "    "+tc.cond, 1)
			result, err := newCheckExecutor(factsFleet(), engine.WithFactReader(storedNginx()), engine.WithMode(collection.ModeCheck)).Run(context.Background(), factsDAG(t, runbook))
			if err != nil {
				t.Fatal(err)
			}
			for device, n := range resultsOf(result, "tasks[1]") {
				if n.Unchecked != tc.unchecked || n.Skipped != tc.skipped || n.Err != nil {
					t.Errorf("on %s: %s, want unchecked=%v skipped=%v", device, describe(n), tc.unchecked, tc.skipped)
				}
			}
			if want := int32(0); !tc.skipped {
				if m.probeChecks.Load() != 2 {
					t.Errorf("the reader was checked %d times, want once on each device", m.probeChecks.Load())
				}
			} else if m.probeChecks.Load() != want {
				t.Errorf("the reader was checked %d times, want none", m.probeChecks.Load())
			}
		})
	}
}

// Tests of the facts root in a real run: a condition reads the device's
// stored facts under what this run emitted (facts_overlay.go), an unknown key
// fails the task whatever the condition says (facts_view.go), and an absent
// key on a gathered device is honestly absent.
package engine_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
)

// gatherThenRead is a runbook gathering on the fleet and then probing where
// the gathered kernel is 6.1 and where it is 5.0.
const gatherThenRead = `
id: gather-then-read
hosts: fleet
tasks:
  - name: gather
    register: g
    tfx.gather: {}
  - name: on six
    when: facts["tfx.kernel"] == "6.1"
    tfx.probe: {}
  - name: on five
    when: facts["tfx.kernel"] == "5.0"
    tfx.probe: {}
`

// TestFactsRoot_AGatherThenAConditionSeesTheGatheredValue: a device no run
// has gathered before is gathered and read in one run, and the condition
// reads the gathered value on every path: plainly, with a secret_mask in the
// runbook (which holds the run's facts until it ends), and with a store that
// refuses every write.
func TestFactsRoot_AGatherThenAConditionSeesTheGatheredValue(t *testing.T) {
	for name, tc := range map[string]struct {
		runbook string
		sink    *factSink
	}{
		"plainly":                 {gatherThenRead, &factSink{got: map[string][]facts.Entry{}}},
		"with a secret_mask":      {gatherThenRead + "  - name: mask\n    secret_mask: {register: g, fields: [tfx.note]}\n    tfx.probe: {}\n", &factSink{got: map[string][]facts.Entry{}}},
		"with a failing store":    {gatherThenRead, &factSink{got: map[string][]facts.Entry{}, fail: errors.New("disk full")}},
		"with no recorder at all": {gatherThenRead, nil},
	} {
		t.Run(name, func(t *testing.T) {
			m := registerFactsRead(t)
			opts := []engine.ExecutorOption{engine.WithFactReader(&fakeReader{})}
			if tc.sink != nil {
				opts = append(opts, engine.WithFactRecorder(tc.sink))
			}
			result, err := newCheckExecutor(factsFleet(), opts...).Run(context.Background(), factsDAG(t, tc.runbook))
			if err != nil {
				t.Fatal(err)
			}
			for device, n := range resultsOf(result, "tasks[1]") {
				if n.Skipped || n.Err != nil {
					t.Errorf("on %s the task reading the gathered 6.1: %s", device, describe(n))
				}
			}
			for device, n := range resultsOf(result, "tasks[2]") {
				if !n.Skipped || n.Err != nil {
					t.Errorf("on %s the task reading 5.0: %s, want skipped", device, describe(n))
				}
			}
			if got := m.probeRuns.Load(); got < 2 {
				t.Errorf("the probe ran %d times, want once on each device", got)
			}
		})
	}
}

// TestFactsRoot_AnEarlierReadDoesNotFreezeALaterGather: a condition reading
// the stored 5.0 takes the snapshot, a gather then finds 6.1, and a later
// condition reads 6.1. The store is read once per device.
func TestFactsRoot_AnEarlierReadDoesNotFreezeALaterGather(t *testing.T) {
	registerFactsRead(t)
	reader := &fakeReader{answers: map[string]map[string]engine.FactAnswer{
		"dev-1": {"tfx.kernel": {State: engine.FactPresent, Value: "5.0"}},
		"dev-2": {"tfx.kernel": {State: engine.FactPresent, Value: "5.0"}},
	}}
	result, err := newCheckExecutor(factsFleet(), engine.WithFactReader(reader)).Run(context.Background(), factsDAG(t, `
id: freeze
hosts: fleet
tasks:
  - name: before
    when: facts["tfx.kernel"] == "5.0"
    tfx.probe: {}
  - name: gather
    tfx.gather: {}
  - name: after
    when: facts["tfx.kernel"] == "6.1"
    tfx.probe: {}
`))
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range []string{"tasks[0]", "tasks[2]"} {
		for device, n := range resultsOf(result, node) {
			if n.Skipped || n.Err != nil {
				t.Errorf("%s on %s: %s, want it run", node, device, describe(n))
			}
		}
	}
	if len(reader.asked) != 2 {
		t.Errorf("the store was read %d times (%v), want once per device", len(reader.asked), reader.asked)
	}
}

// TestFactsRoot_TheOverlayValueEqualsTheStoredValueOnTheNextRun: a number a
// gather emits compares with > the same in the run that gathered it, read
// from the overlay, and in the next run, read from a real sealed store: both
// hold the store's own form of it.
func TestFactsRoot_TheOverlayValueEqualsTheStoredValueOnTheNextRun(t *testing.T) {
	registerFactsRead(t)
	store := sealedFactStore(t)
	// A number compares as a number, and a value of the wrong type for its
	// declaration (an integer note, declared a string) is not there, as
	// the store would not keep it.
	compare := `
  - name: big
    when:
      - facts["tfx.mem_mb"] > 4000
      - facts["tfx.mem_mb"] == 4096
      - facts["tfx.load"] > 0.5
      - '!("tfx.note" in facts)'
    tfx.probe: {}
`
	x := func() *engine.Executor {
		return newCheckExecutor(factsFleet(), engine.WithFactReader(storeReader{store}), engine.WithFactRecorder(store))
	}
	// The secret_mask holds the first run's facts until it ends, so its
	// condition reads them from the overlay alone, never from the store.
	first, err := x().Run(context.Background(), factsDAG(t, `id: first
hosts: fleet
tasks:
  - name: gather
    register: g
    tfx.gather: {note: 42, codename: bookworm}
`+compare+`  - name: mask
    secret_mask: {register: g, fields: [tfx.codename]}
    tfx.probe: {}
`))
	if err != nil {
		t.Fatal(err)
	}
	if first.HasErrors() {
		t.Fatalf("the gathering run failed: %+v", first.Nodes)
	}
	for device, n := range resultsOf(first, "tasks[1]") {
		if n.Skipped || n.Err != nil {
			t.Errorf("the gathering run on %s: %s", device, describe(n))
		}
	}
	second, err := x().Run(context.Background(), factsDAG(t, "id: second\nhosts: fleet\ntasks:"+compare))
	if err != nil {
		t.Fatal(err)
	}
	for device, n := range resultsOf(second, "tasks[0]") {
		if n.Skipped || n.Err != nil {
			t.Errorf("the next run on %s: %s", device, describe(n))
		}
	}
}

// TestFactsRoot_ANeverGatheredDeviceIsUnknownAndFailsTheTask: a key on a
// device no method that emits it has observed is unknown, which fails the
// task naming it and saying to gather first, whatever the condition would
// have answered: no test, default or short circuit launders it.
func TestFactsRoot_ANeverGatheredDeviceIsUnknownAndFailsTheTask(t *testing.T) {
	for name, cond := range map[string]string{
		"a comparison":           `when: facts["tfx.kernel"] == "6.1"`,
		"is defined":             `when: facts["tfx.kernel"] is defined`,
		"is not defined":         `when: facts["tfx.kernel"] is not defined`,
		"in":                     `when_cel: '!("tfx.kernel" in facts)'`,
		"has":                    `when_cel: '!has(facts.uptime)'`,
		"an optional default":    `when_cel: 'facts[?"tfx.kernel"].orValue("none") == "none"'`,
		"an optional field":      `when_cel: '!facts.?uptime.hasValue()'`,
		"a short circuit or":     `when_cel: 'true || facts["tfx.kernel"] == "x"'`,
		"a short circuit and":    `when_cel: 'false && facts["tfx.kernel"] == "x"'`,
		"a when_or":              "when_or:\n      - \"true\"\n      - facts[\"tfx.kernel\"] == \"x\"",
		"a later item of a when": "when:\n      - facts[\"tfx.kernel\"] is not defined\n      - \"true\"",
	} {
		t.Run(name, func(t *testing.T) {
			m := registerFactsRead(t)
			result, err := newCheckExecutor(factsFleet(), engine.WithFactReader(&fakeReader{})).Run(context.Background(),
				factsDAG(t, "id: unknown\nhosts: fleet\ntasks:\n  - name: read\n    "+cond+"\n    tfx.probe: {}\n"))
			if err != nil {
				t.Fatal(err)
			}
			runs := resultsOf(result, "tasks[0]")
			if len(runs) != 2 {
				t.Fatalf("results %v, want one per device", runs)
			}
			for device, n := range runs {
				if n.Err == nil || n.Skipped {
					t.Fatalf("on %s: %s, want the task failed", device, describe(n))
				}
				msg := n.Err.Error()
				if !strings.Contains(msg, `tasks[0] (name "read")`) || !strings.Contains(msg, "gather it") || !strings.Contains(msg, "the task did not run") {
					t.Errorf("on %s the failure says %q, want it to name the task, say to gather and that nothing ran", device, msg)
				}
				if !strings.Contains(msg, `facts["tfx.kernel"]`) && !strings.Contains(msg, `facts["uptime"]`) {
					t.Errorf("on %s the failure says %q, want it to name the key", device, msg)
				}
			}
			if got := m.probeRuns.Load(); got != 0 {
				t.Errorf("the probe ran %d times on an unknown fact", got)
			}
		})
	}
}

// TestFactsRoot_AnAbsentKeyOnAGatheredDeviceIsHonestlyAbsent: a key a method
// that emits it looked for and did not find reads as absent, from the store
// (another run gathered) and from this run's own gather, so a test for it
// answers; reading it without a test fails naming it, never quoting a value.
func TestFactsRoot_AnAbsentKeyOnAGatheredDeviceIsHonestlyAbsent(t *testing.T) {
	registerFactsRead(t)
	stored := &fakeReader{answers: map[string]map[string]engine.FactAnswer{
		"dev-1": {"tfx.codename": {State: engine.FactAbsent}}, "dev-2": {"tfx.codename": {State: engine.FactAbsent}},
	}}
	runbook := func(gather string) string {
		return "id: absent\nhosts: fleet\ntasks:\n" + gather + `  - name: missing
    when: facts["tfx.codename"] is not defined
    tfx.probe: {}
  - name: present
    when: facts["tfx.codename"] is defined
    tfx.probe: {}
`
	}
	for name, tc := range map[string]struct {
		reader engine.FactReader
		gather string
	}{
		"stored absent":        {stored, ""},
		"absent from this run": {&fakeReader{}, "  - name: gather\n    tfx.gather: {}\n"},
	} {
		t.Run(name, func(t *testing.T) {
			result, err := newCheckExecutor(factsFleet(), engine.WithFactReader(tc.reader)).Run(context.Background(), factsDAG(t, runbook(tc.gather)))
			if err != nil {
				t.Fatal(err)
			}
			first := 0
			if tc.gather != "" {
				first = 1
			}
			missing, present := resultsOf(result, nodeAt(first)), resultsOf(result, nodeAt(first+1))
			for device, n := range missing {
				if n.Skipped || n.Err != nil {
					t.Errorf("is not defined on %s: %s, want it run", device, describe(n))
				}
			}
			for device, n := range present {
				if !n.Skipped || n.Err != nil {
					t.Errorf("is defined on %s: %s, want it skipped", device, describe(n))
				}
			}
		})
	}
	result, err := newCheckExecutor(factsFleet(), engine.WithFactReader(stored)).Run(context.Background(), factsDAG(t, `
id: untested
hosts: fleet
tasks:
  - name: read
    when: facts["tfx.codename"] == "bookworm"
    tfx.probe: {}
`))
	if err != nil {
		t.Fatal(err)
	}
	for device, n := range resultsOf(result, "tasks[0]") {
		if n.Err == nil || !strings.Contains(n.Err.Error(), `it reads facts["tfx.codename"], which the device does not have`) {
			t.Errorf("an untested read of an absent key on %s: %s", device, describe(n))
		}
	}
}

// nodeAt is the node id of the top-level task at i.
func nodeAt(i int) string { return "tasks[" + string(rune('0'+i)) + "]" }

// TestFactsRoot_NoReaderWiredFailsTheTask: an executor no store was wired to
// cannot read a fact, and a task reading one fails saying so rather than
// reading undefined, so is not defined does not run it.
func TestFactsRoot_NoReaderWiredFailsTheTask(t *testing.T) {
	m := registerFactsRead(t)
	result, err := newCheckExecutor(factsFleet()).Run(context.Background(), factsDAG(t, `
id: unwired
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
		if n.Err == nil || !strings.Contains(n.Err.Error(), "this run has no store of facts to read") {
			t.Errorf("on %s: %s, want the task failed for want of a store", device, describe(n))
		}
	}
	if m.probeRuns.Load() != 0 {
		t.Error("the probe ran with no store to read")
	}
}

// TestFactsRoot_AStoreThatCannotBeReadFailsOnlyWhatItCannotAnswer: a store
// that will not open fails a task reading a key this run did not emit,
// naming why, and a key this run gathered still reads.
func TestFactsRoot_AStoreThatCannotBeReadFailsOnlyWhatItCannotAnswer(t *testing.T) {
	registerFactsRead(t)
	reader := &fakeReader{err: errors.New("the project's key is gone")}
	result, err := newCheckExecutor(factsFleet(), engine.WithFactReader(reader)).Run(context.Background(), factsDAG(t, `
id: unavailable
hosts: fleet
tasks:
  - name: stored only
    when: facts["tfx.kernel"] == "6.1"
    tfx.probe: {}
`))
	if err != nil {
		t.Fatal(err)
	}
	for device, n := range resultsOf(result, "tasks[0]") {
		if n.Err == nil || !strings.Contains(n.Err.Error(), "could not be read: the project's key is gone") {
			t.Errorf("on %s: %s, want the store's failure named", device, describe(n))
		}
	}
	gathered, err := newCheckExecutor(factsFleet(), engine.WithFactReader(reader)).Run(context.Background(), factsDAG(t, gatherThenRead))
	if err != nil {
		t.Fatal(err)
	}
	for device, n := range resultsOf(gathered, "tasks[1]") {
		if n.Skipped || n.Err != nil {
			t.Errorf("a key this run gathered, on %s with its store unreadable: %s", device, describe(n))
		}
	}
}

// TestFactsRoot_EveryPathThatDecidesATaskReadsWhatTheRunGathered: a task
// reading a gathered fact inside a block with a rescue, in a loop and in an
// until retry is decided on the gathered value, as a top-level task is.
func TestFactsRoot_EveryPathThatDecidesATaskReadsWhatTheRunGathered(t *testing.T) {
	for name, reader := range map[string]string{
		"in a block with a rescue": "  - name: guarded\n    block:\n      - name: read\n        when: facts[\"tfx.kernel\"] == \"6.1\"\n        tfx.probe: {}\n    rescue:\n      - name: answer\n        noop: {}\n",
		"in a loop":                "  - name: read\n    loop: [a, b]\n    when: facts[\"tfx.kernel\"] == \"6.1\"\n    tfx.probe: {}\n",
		"in an until retry":        "  - name: read\n    register: r\n    until: \"true\"\n    retries: 1\n    delay: 0\n    when: facts[\"tfx.kernel\"] == \"6.1\"\n    tfx.probe: {}\n",
	} {
		t.Run(name, func(t *testing.T) {
			m := registerFactsRead(t)
			result, err := newCheckExecutor(factsFleet(), engine.WithFactReader(&fakeReader{})).Run(context.Background(),
				factsDAG(t, "id: paths\nhosts: fleet\ntasks:\n  - name: gather\n    tfx.gather: {}\n"+reader))
			if err != nil {
				t.Fatal(err)
			}
			if result.HasErrors() {
				t.Fatalf("the run failed: %+v", result.Nodes)
			}
			if m.probeRuns.Load() < 2 {
				t.Errorf("the reader ran %d times, want at least once on each device", m.probeRuns.Load())
			}
		})
	}
}

// TestFactsRoot_TheRunsSecretsGuardWhatAConditionReads: a value holding one
// of the run's known secrets is never read, whether this run gathered it
// (it is not overlaid, as it would not be stored) or the store held it (it
// is withheld, failing the task that reads it).
func TestFactsRoot_TheRunsSecretsGuardWhatAConditionReads(t *testing.T) {
	m := registerFactsRead(t)
	const secret = "Pelican-Vault-Forty"
	gathered, err := newCheckExecutor(factsFleet(), engine.WithFactReader(&fakeReader{}), engine.WithKnownSecrets([]string{secret})).Run(context.Background(), factsDAG(t, `
id: secret-overlay
hosts: fleet
tasks:
  - name: gather
    tfx.gather: {note: `+secret+`}
  - name: read
    when: facts["tfx.note"] is defined
    tfx.probe: {}
`))
	if err != nil {
		t.Fatal(err)
	}
	for device, n := range resultsOf(gathered, "tasks[1]") {
		if !n.Skipped || n.Err != nil {
			t.Errorf("a gathered secret on %s: %s, want it not there to read", device, describe(n))
		}
	}
	stored := &fakeReader{answers: map[string]map[string]engine.FactAnswer{
		"dev-1": {"tfx.note": {State: engine.FactPresent, Value: secret}}, "dev-2": {"tfx.note": {State: engine.FactPresent, Value: secret}},
	}}
	held, err := newCheckExecutor(factsFleet(), engine.WithFactReader(stored), engine.WithKnownSecrets([]string{secret})).Run(context.Background(), factsDAG(t, `
id: secret-stored
hosts: fleet
tasks:
  - name: read
    when: facts["tfx.note"] is defined
    tfx.probe: {}
`))
	if err != nil {
		t.Fatal(err)
	}
	for device, n := range resultsOf(held, "tasks[0]") {
		if n.Err == nil || !strings.Contains(n.Err.Error(), "holds a secret this run knows") || strings.Contains(n.Err.Error(), secret) {
			t.Errorf("a stored secret on %s: %s, want it withheld, naming why and not the value", device, describe(n))
		}
	}
	if m.probeRuns.Load() != 0 {
		t.Errorf("a task reading a secret ran %d times", m.probeRuns.Load())
	}
}

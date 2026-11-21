// Tests that a key reads absent only where an observation is known to have
// looked for it: a key a method declares exactly, from a call this run made
// with nothing narrowing it. A family's key, a key of a narrowed call and a
// key the store alone could speak for read unknown, and fail the task.
package engine_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
)

// TestFactsRoot_AKeyNoObservationLookedForIsNeverAbsent: after a gather of
// the defaults, a service the gather's family would name was never looked
// for; after an install of one package, every other package was never
// looked for; after a gather narrowed to one key, every other key was never
// looked for. Each reads unknown, so is not defined fails the task rather
// than running it on a device that may well have what it asks about.
func TestFactsRoot_AKeyNoObservationLookedForIsNeverAbsent(t *testing.T) {
	for name, tc := range map[string]struct{ first, cond string }{
		"a family key after a gather of the defaults": {"tfx.gather: {}", `facts["tfx.svc.nginx"] is not defined`},
		"another package after an install":            {"tfx.install: {name: curl}", `facts["tfx.pkg.nginx"] is not defined`},
		"another key after a narrowed gather":         {"tfx.gather: {only: [tfx.kernel]}", `facts["tfx.codename"] is not defined`},
	} {
		t.Run(name, func(t *testing.T) {
			m := registerFactsRead(t)
			result, err := newCheckExecutor(factsFleet(), engine.WithFactReader(&fakeReader{})).Run(context.Background(), factsDAG(t,
				"id: never-asked\nhosts: fleet\ntasks:\n  - name: first\n    "+tc.first+"\n  - name: read\n    when: "+tc.cond+"\n    tfx.probe: {}\n"))
			if err != nil {
				t.Fatal(err)
			}
			for device, n := range resultsOf(result, "tasks[1]") {
				if n.Err == nil || !strings.Contains(n.Err.Error(), "no stored answer") {
					t.Errorf("on %s: %s, want the task failed with the key unknown", device, describe(n))
				}
			}
			if m.probeRuns.Load() != 0 {
				t.Errorf("the task ran %d times on a key nothing looked for", m.probeRuns.Load())
			}
		})
	}
}

// TestFactsRoot_AStoredObservationAloneMakesNoKeyAbsent: the store keeps what
// each observation emitted, not what it looked for, so a key a stored
// observation's method declares exactly reads unknown in a later run, never
// absent: nothing recorded says the call that made it was not narrowed.
func TestFactsRoot_AStoredObservationAloneMakesNoKeyAbsent(t *testing.T) {
	m := registerFactsRead(t)
	store := sealedFactStore(t)
	x := func() *engine.Executor {
		return newCheckExecutor(factsFleet(), engine.WithFactReader(storeReader{store}), engine.WithFactRecorder(store))
	}
	if _, err := x().Run(context.Background(), factsDAG(t, "id: gather\nhosts: fleet\ntasks:\n  - name: gather\n    tfx.gather: {}\n")); err != nil {
		t.Fatal(err)
	}
	result, err := x().Run(context.Background(), factsDAG(t, "id: later\nhosts: fleet\ntasks:\n  - name: read\n    when: facts[\"tfx.codename\"] is not defined\n    tfx.probe: {}\n"))
	if err != nil {
		t.Fatal(err)
	}
	for device, n := range resultsOf(result, "tasks[0]") {
		if n.Err == nil {
			t.Errorf("on %s: %s, want the key unknown", device, describe(n))
		}
	}
	if m.probeRuns.Load() != 0 {
		t.Error("a later run read a key the store never said was looked for as absent")
	}
}

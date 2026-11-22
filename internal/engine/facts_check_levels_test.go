// Tests that a block's always and rescue tasks and a handler, each reading a
// fact an earlier task would emit, are placed after it on the walk, by the
// check's taint and by what the run could gather first.
package engine_test

import (
	"context"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// guardedReaders installs a package, then reads its fact in a block's
// always, in its rescue and in the handler the install notifies.
const guardedReaders = `
id: guarded-readers
hosts: fleet
tasks:
  - name: install
    tfx.install: {name: nginx}
    notify: configure
  - name: guarded
    block:
      - name: inner
        tfx.probe: {}
    rescue:
      - name: in the rescue
        when: facts["tfx.pkg.nginx"] is defined
        tfx.probe: {}
    always:
      - name: in the always
        when: facts["tfx.pkg.nginx"] is defined
        tfx.probe: {}
handlers:
  - name: configure
    when: facts["tfx.pkg.nginx"] is defined
    tfx.probe: {}
`

// TestCheckedFactReads_AlwaysRescueAndHandlersFollowWhatTheyRead: the always
// task and the handler are tainted by the install before them, a rescue,
// which a check never runs, is not, and each reader counts the install as
// gathered before it; a reader before the install does not.
func TestCheckedFactReads_AlwaysRescueAndHandlersFollowWhatTheyRead(t *testing.T) {
	registerFactsRead(t)
	dag := factsDAG(t, guardedReaders)
	readers := map[string]string{}
	for _, r := range engine.FactReadsOf(dag) {
		readers[dag.Nodes[r.Node].Name] = r.Node
	}
	if len(readers) != 3 {
		t.Fatalf("readers %v, want the rescue, the always and the handler", readers)
	}
	taints := engine.CheckedFactReads(collection.ModeCheck, dag)
	for name, wantTaint := range map[string]bool{"in the always": true, "configure": true, "in the rescue": false} {
		node := readers[name]
		if got := len(taints[node]) > 0; got != wantTaint {
			t.Errorf("%s (%s): tainted %v, want %v", name, node, got, wantTaint)
		}
		if !engine.DeclaredBefore(dag, node, "tfx.pkg.nginx") {
			t.Errorf("%s (%s): the install before it does not count as gathered first", name, node)
		}
	}
	before := factsDAG(t, "id: before\nhosts: fleet\ntasks:\n  - name: guarded\n    block:\n      - name: inner\n        tfx.probe: {}\n    always:\n      - name: reads\n        when: facts[\"tfx.pkg.nginx\"] is defined\n        tfx.probe: {}\n  - name: install\n    tfx.install: {name: nginx}\n")
	if engine.DeclaredBefore(before, "tasks[0].always[0]", "tfx.pkg.nginx") {
		t.Error("an install after the always task counted as gathered before it")
	}
}

// TestFactsCheck_AnAlwaysTaskReadingAnInstallsFactIsUnchecked: in a check the
// always task is reported unchecked and the rescue is not predicted at all.
func TestFactsCheck_AnAlwaysTaskReadingAnInstallsFactIsUnchecked(t *testing.T) {
	registerFactsRead(t)
	result, err := newCheckExecutor(factsFleet(), engine.WithFactReader(storedNginx()), engine.WithMode(collection.ModeCheck)).Run(context.Background(), factsDAG(t, guardedReaders))
	if err != nil {
		t.Fatal(err)
	}
	for device, n := range resultsOf(result, "tasks[1].always[0]") {
		if !n.Unchecked {
			t.Errorf("the always task on %s: %s, want it unchecked", device, describe(n))
		}
	}
	for device, n := range resultsOf(result, "tasks[1].rescue[0]") {
		if n.Unchecked || !n.Conditional {
			t.Errorf("the rescue on %s: %s, want it not predicted", device, describe(n))
		}
	}
}

// Tests that a Runner refuses a dispatched runbook whose condition reads
// stored facts before its first task, since it holds no store and is handed
// no fact, and that a rollback of such a runbook, whose steps read none, is
// not refused for it.
package native

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// factsReadingRunbook changes the device first, then decides on a stored
// fact: refusing at the second task would come after the first had run.
const factsReadingRunbook = `id: pb-1
tasks:
  - name: first
    noop: {changed: true}
  - name: on seventeen
    when: facts["os.version"] == "17.3"
    noop: {changed: true}
`

// TestExecute_RefusesARunbookReadingStoredFactsBeforeItsFirstTask: the
// dispatch is refused naming the task and the key, saying nothing ran and
// what works on this tier, and the job log holds only the start and the
// refusal: no task, the first included, ran.
func TestExecute_RefusesARunbookReadingStoredFactsBeforeItsFirstTask(t *testing.T) {
	bus := &mockBus{}
	adapter, err := NewAdapter(bus, writeRunbook(t, "pb-1", factsReadingRunbook), nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = adapter.Execute(context.Background(), wire.DispatchPayload{JobID: "job-1", RunbookID: "pb-1", DeviceName: "router1", DeviceHost: "10.0.0.1"})
	if err == nil {
		t.Fatal("a runbook reading stored facts ran on a Runner")
	}
	for _, want := range []string{"fails validation, so no task ran", `tasks[1] (name "on seventeen")`, `facts["os.version"]`, "Runner cannot read stored facts in this release", "registered result"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal %q does not say %q", err, want)
		}
	}
	events := bus.logEvents("job-1")
	if len(events) != 2 {
		t.Fatalf("job log has %d events, want only the start and the refusal", len(events))
	}
	var last wire.JobEvent
	if err := json.Unmarshal(events[1].Data, &last); err != nil {
		t.Fatal(err)
	}
	if last.Status != "failed" || !strings.Contains(last.EventData.Message, "Runner cannot read stored facts") {
		t.Errorf("the job log ends %q (%q), want the refusal", last.Status, last.EventData.Message)
	}
}

// TestValidateDispatch_ARollbackOfAFactsRunbookIsNotRefusedForIt: the steps a
// rollback runs in place of the runbook's tasks read no fact, so the Runner's
// check refuses nothing for facts, whatever the runbook it undoes reads.
func TestValidateDispatch_ARollbackOfAFactsRunbookIsNotRefusedForIt(t *testing.T) {
	ranRollbackFixture(t)
	device := &inventorytest.Stub{StubID: "dev-1", StubName: "web1", StubState: inventory.StateActive}
	eval, err := engine.NewCELEvaluator()
	if err != nil {
		t.Fatal(err)
	}
	ran, err := engine.NewBuilder(eval).BuildFromYAML([]byte("id: ran\ntasks:\n  - name: make it\n    rbtest.make:\n      path: /tmp/made\n  - name: on seventeen\n    when: facts[\"os.version\"] == \"17.3\"\n    rbtest.make:\n      path: /tmp/other\n"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := validateDispatch(ran, device, collection.ModeExecute); err == nil || !strings.Contains(err.Error(), "Runner cannot read stored facts") {
		t.Fatalf("the runbook itself: %v, want it refused for its fact read", err)
	}
	step := wire.RollbackStep{Node: "tasks[0]", Emitter: "rbtest.make", Method: "rbtest.remove", Params: map[string]any{"path": "/tmp/made"}, Name: "undo tasks[0]", Source: "recorded"}
	payload := wire.DispatchPayload{RunbookID: "ran", DeviceName: "web1", Rollback: &wire.Rollback{Of: "job-1", DAGVersion: ran.Version, Steps: []wire.RollbackStep{step}}}
	undo, _, _, err := rollbackDAG(nil, ran, payload)
	if err != nil {
		t.Fatalf("rollbackDAG: %v", err)
	}
	if _, err := validateDispatch(undo, device, collection.ModeExecute); err != nil && strings.Contains(err.Error(), "stored facts") {
		t.Fatalf("the rollback was refused for the runbook's fact read: %v", err)
	}
}

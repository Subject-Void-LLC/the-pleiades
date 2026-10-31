// Tests of serial: on the CLI: which devices each batch reaches, that the
// batches are one run, and that a failed batch stops the rollout.
package main

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/lock"
	"github.com/Subject-Void-LLC/the-pleiades/internal/validate"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
)

// batchLog is an ActionExecutor recording each task's device in order, and
// failing the devices named in fail.
type batchLog struct {
	mu    sync.Mutex
	order []string
	fail  map[string]bool
}

func (b *batchLog) Execute(_ context.Context, task *engine.Task, d inventory.InventoryItem) (engine.ActionResult, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.order = append(b.order, task.Name+"@"+d.Name())
	if b.fail[d.Name()] {
		return engine.ActionResult{}, errors.New("failed as asked")
	}
	return engine.ActionResult{}, nil
}

// serialWorld is five web devices and one lab server outside the hosts.
func serialWorld() validate.WorldView {
	var items []inventory.InventoryItem
	for _, name := range []string{"w1", "w2", "w3", "w4", "w5"} {
		items = append(items, &inventorytest.Stub{StubID: inventory.DeviceID(name), StubName: name, StubTags: []inventory.Tag{"web"}, StubState: inventory.StateActive, Caps: []capability.Name{}})
	}
	items = append(items, &inventorytest.Stub{StubID: "lab", StubName: "lab", StubState: inventory.StateActive})
	return validate.WorldView{Items: items}
}

// runSerial builds runbook, plans its batches over serialWorld and runs them.
func runSerial(t *testing.T, runbook string, log *batchLog) (engine.RunResult, *serialPlan, error, string) {
	t.Helper()
	eval, err := engine.NewCELEvaluator()
	if err != nil {
		t.Fatal(err)
	}
	dag, err := engine.NewBuilder(eval).BuildFromYAML([]byte(runbook))
	if err != nil {
		t.Fatal(err)
	}
	world, plan := planSerial(serialWorld(), dag)
	if plan == nil {
		t.Fatal("a runbook with serial: has no batch plan")
	}
	x := engine.NewExecutor(world, log, lock.NewInProcessManager(), event.NewInProcessBus(), engine.NewInProcessWorkflowContext(), 0)
	result, err, stopped := runBatches(context.Background(), x, dag, plan, false)
	msg := ""
	if stopped != nil {
		msg = stopped.Message + " | " + strings.Join(stopped.Devices, ",")
	}
	return result, plan, err, msg
}

// TestSerial_RunsTheWholeRunbookOneBatchAtATime: serial: 2 over five hosts is
// batches of 2, 2 and 1, each running every task before the next starts; a
// task naming a device outside the hosts reaches it in every batch; and the
// batches are one run with one numbering.
func TestSerial_RunsTheWholeRunbookOneBatchAtATime(t *testing.T) {
	log := &batchLog{}
	result, plan, err, stopped := runSerial(t, `id: s
hosts: web
serial: 2
tasks:
  - name: a
    noop: {}
  - name: b
    noop: {}
  - name: lab
    noop:
      target: lab
`, log)
	if err != nil || result.HasErrors() || stopped != "" {
		t.Fatalf("run: %v, stopped %q, %+v", err, stopped, result.Nodes)
	}
	if len(plan.batches) != 3 {
		t.Fatalf("%d batches, want 3", len(plan.batches))
	}
	want := "a@w1,a@w2,b@w1,b@w2,lab@lab,a@w3,a@w4,b@w3,b@w4,lab@lab,a@w5,b@w5,lab@lab"
	if got := strings.Join(sortedLevels(log.order), ","); got != want {
		t.Errorf("ran %s,\nwant %s", got, want)
	}
	if result.RunID == "" || result.Sequence < len(result.Nodes) {
		t.Errorf("run id %q, last sequence %d for %d results: the batches are not one numbered run", result.RunID, result.Sequence, len(result.Nodes))
	}
}

// TestSerial_AFailedBatchStopsTheRollout: a failure in the second batch ends
// the run there; the third batch never runs, and the warning names its
// devices.
func TestSerial_AFailedBatchStopsTheRollout(t *testing.T) {
	log := &batchLog{fail: map[string]bool{"w3": true}}
	result, _, err, stopped := runSerial(t, `id: s
hosts: web
serial: 2
tasks:
  - name: a
    noop: {}
`, log)
	if err != nil || !result.HasErrors() {
		t.Fatalf("run: %v, HasErrors %v", err, result.HasErrors())
	}
	for _, entry := range log.order {
		if strings.HasSuffix(entry, "@w5") {
			t.Errorf("the batch after the failure ran: %v", log.order)
		}
	}
	if !strings.Contains(stopped, "batch 2 of 3 failed") || !strings.HasSuffix(stopped, "| w5") {
		t.Errorf("stopped warning %q", stopped)
	}
}

// sortedLevels orders each batch's concurrent device runs of one task by
// device name, keeping task and batch order, so the order is comparable.
func sortedLevels(order []string) []string {
	out := append([]string(nil), order...)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0; j-- {
			ti, _, _ := strings.Cut(out[j], "@")
			tp, _, _ := strings.Cut(out[j-1], "@")
			if ti != tp || out[j] >= out[j-1] {
				break
			}
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// Internal tests of rescueWanted, the check that keeps a narrowed rescue from
// reading as a rescue that finished, for a block that is not one unit (its
// frames keyed by device), and of the one list of recorded journal kinds.
package engine

import (
	"slices"
	"testing"
)

// TestRescueWanted_ReadsEveryDevicesFrame: in a block whose frames are kept
// per device, a rescue task is wanted while any device's frame is answering a
// failure of its block, and not otherwise; a task outside a rescue never is.
func TestRescueWanted_ReadsEveryDevicesFrame(t *testing.T) {
	eval, err := NewCELEvaluator()
	if err != nil {
		t.Fatal(err)
	}
	dag, err := NewBuilder(eval).BuildFromYAML([]byte("id: w\nhosts: web\ntasks:\n  - block:\n      - name: b\n        noop: {}\n    rescue:\n      - name: r\n        noop: {}\n"))
	if err != nil {
		t.Fatal(err)
	}
	r := &run{dag: dag, handlers: newHandlers(dag)}
	const rescue, block = "tasks[0].rescue[0]", "tasks[0].block[0]"
	if r.handlers.key(rescue, "") != "" {
		t.Fatal("the block runs as one unit; this test needs frames kept per device")
	}
	if r.rescueWanted(rescue) || r.rescueWanted(block) {
		t.Fatal("wanted with no failure")
	}
	r.handlers.push("w2", &handlerFrame{block: "tasks[0]", part: PartBlock, origin: NodeResult{NodeID: block, Device: "w2"}})
	if !r.rescueWanted(rescue) {
		t.Error("a device's failure did not want its block's rescue")
	}
	if r.rescueWanted(block) {
		t.Error("a block task was wanted as a rescue")
	}
}

// TestRecordedKinds_AreTheValidatorsLists: the exported lists are the ones
// the projection and the reader check, each a copy.
func TestRecordedKinds_AreTheValidatorsLists(t *testing.T) {
	stages, kinds := RecordedFailureStages(), RecordedSkipKinds()
	if len(stages) != 12 || len(kinds) != 8 {
		t.Fatalf("%d stages and %d skip kinds, want 12 and 8", len(stages), len(kinds))
	}
	for _, s := range stages {
		if !IsRecordedFailureStage(s) {
			t.Errorf("stage %q listed but refused", s)
		}
	}
	for _, k := range kinds {
		if !IsRecordedSkipKind(k) {
			t.Errorf("skip kind %q listed but refused", k)
		}
	}
	stages[0] = "changed"
	if slices.Contains(RecordedFailureStages(), FailureStage("changed")) {
		t.Error("a caller's change reached the engine's list")
	}
}

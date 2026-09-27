// Tests for virt.vbox.snapshot.delete against the model host.
package snapshot

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/vboxmanage"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/vboxmanage/vboxmanagetest"
)

func TestDelete(t *testing.T) {
	vm := lab()
	model := vboxmanagetest.New(vm)
	onModel(t, model)
	rc := newRecorder()
	result, err := call(t, "virt.vbox.snapshot.delete", false, rc, map[string]any{"vm": "ubuntu-lab", "name": "clean"})
	if err != nil || !result.Changed {
		t.Fatalf("%+v, %v", result, err)
	}
	if len(vm.Snapshots) != 0 || rc.stats["uuid"] != cleanUUID {
		t.Errorf("snapshots %+v, stats %v", vm.Snapshots, rc.stats)
	}
	if before, after := rc.diff(t); before["exists"] != true || after["exists"] != false {
		t.Errorf("diff = %v -> %v", before, after)
	}
	if _, ok := rc.stats[sdk.StatInverse]; ok {
		t.Error("an irreversible delete recorded an inverse")
	}
	rc = newRecorder()
	result, err = call(t, "virt.vbox.snapshot.delete", false, rc, map[string]any{"vm": "ubuntu-lab", "name": "clean"})
	if err != nil || result.Changed {
		t.Fatalf("deleting it again: %+v, %v", result, err)
	}
	if before, after := rc.diff(t); before["exists"] != false || after["exists"] != false {
		t.Errorf("an unchanged diff = %v -> %v", before, after)
	}
	if _, ok := rc.stats["uuid"]; ok {
		t.Error("an unchanged delete reported a UUID")
	}
}

func TestDelete_OfARunningVM(t *testing.T) {
	vm := lab()
	vm.State = vboxmanage.StateRunning
	onModel(t, vboxmanagetest.New(vm))
	if result, err := call(t, "virt.vbox.snapshot.delete", false, newRecorder(), map[string]any{"vm": "ubuntu-lab", "name": "clean"}); err != nil || !result.Changed || len(vm.Snapshots) != 0 {
		t.Errorf("%+v, %v", result, err)
	}
}

// TestDelete_TheInverseOfATake runs the instruction a take records, which
// names the snapshot by UUID, after a second snapshot took the same name.
func TestDelete_TheInverseOfATake(t *testing.T) {
	vm := twoClean()
	model := vboxmanagetest.New(vm)
	onModel(t, model)
	rc := newRecorder()
	result, err := call(t, "virt.vbox.snapshot.delete", false, rc, map[string]any{"vm": "ubuntu-lab", "name": "clean", "uuid": secondUUID})
	if err != nil || !result.Changed {
		t.Fatalf("%+v, %v", result, err)
	}
	if len(vm.Snapshots) != 1 || vm.Snapshots[0].UUID != cleanUUID {
		t.Errorf("the wrong snapshot went: %+v", vm.Snapshots)
	}
	if _, after := rc.diff(t); after["exists"] != true {
		t.Errorf("after = %v; another snapshot still has the name", after)
	}
	// The snapshot that instruction named is gone and another has the name:
	// refused, rather than deleting the other or calling it done.
	_, err = call(t, "virt.vbox.snapshot.delete", false, newRecorder(), map[string]any{"vm": "ubuntu-lab", "name": "clean", "uuid": secondUUID})
	if err == nil || !strings.Contains(err.Error(), "the ones named so are "+cleanUUID) {
		t.Errorf("a UUID that is gone: %v", err)
	}
	if len(vm.Snapshots) != 1 {
		t.Errorf("snapshots = %+v", vm.Snapshots)
	}
}

func TestDelete_Check(t *testing.T) {
	vm := lab()
	model := vboxmanagetest.New(vm)
	onModel(t, model)
	rc := newRecorder()
	result, err := call(t, "virt.vbox.snapshot.delete", true, rc, map[string]any{"vm": "ubuntu-lab", "name": "clean"})
	if err != nil || !result.Changed || rc.stats["uuid"] != cleanUUID {
		t.Fatalf("%+v, %v, %v", result, err, rc.stats)
	}
	if len(changes(model)) != 0 || len(vm.Snapshots) != 1 {
		t.Errorf("a check acted: %v", changes(model))
	}
}

func TestDelete_Refusals(t *testing.T) {
	model := vboxmanagetest.New(twoClean())
	onModel(t, model)
	if _, err := call(t, "virt.vbox.snapshot.delete", false, newRecorder(), map[string]any{"vm": "ubuntu-lab", "name": "clean"}); err == nil || !strings.Contains(err.Error(), "say which with uuid") {
		t.Errorf("an ambiguous name: %v", err)
	}
	model = vboxmanagetest.New(lab())
	model.Fail = map[string]vboxmanage.Output{"VBoxManage.exe snapshot": {ExitCode: 1, Stderr: "VBoxManage.exe: error: Code E_ACCESSDENIED\r\n"}}
	onModel(t, model)
	if _, err := call(t, "virt.vbox.snapshot.delete", false, newRecorder(), map[string]any{"vm": "ubuntu-lab", "name": "clean"}); err == nil || !strings.Contains(err.Error(), "E_ACCESSDENIED") {
		t.Errorf("a failed delete: %v", err)
	}
	model.Fail = nil
	for _, stat := range []string{"uuid", sdk.StatDiff} {
		rc := newRecorder()
		rc.failOn = stat
		if _, err := call(t, "virt.vbox.snapshot.delete", false, rc, map[string]any{"vm": "ubuntu-lab", "name": "clean"}); err == nil {
			t.Errorf("recording %s failed and the delete did not", stat)
		}
	}
}

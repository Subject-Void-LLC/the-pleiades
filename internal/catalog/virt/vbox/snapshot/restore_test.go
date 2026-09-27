// Tests for virt.vbox.snapshot.restore against the model host.
package snapshot

import (
	"errors"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/vboxmanage"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/vboxmanage/vboxmanagetest"
)

func TestRestore(t *testing.T) {
	vm := twoClean()
	vm.Snapshots[1].Name = "later"
	model := vboxmanagetest.New(vm)
	onModel(t, model)
	rc := newRecorder()
	result, err := call(t, "virt.vbox.snapshot.restore", false, rc, map[string]any{"vm": "ubuntu-lab", "name": "clean"})
	if err != nil || !result.Changed {
		t.Fatalf("%+v, %v", result, err)
	}
	if vm.Current != cleanUUID || rc.stats["uuid"] != cleanUUID {
		t.Errorf("current = %s, stats %v", vm.Current, rc.stats)
	}
	if _, ok := rc.stats[sdk.StatInverse]; ok {
		t.Error("an irreversible restore recorded an inverse")
	}
	// Restoring the current snapshot again still discards what the VM did
	// since, so it is still a change.
	if result, err := call(t, "virt.vbox.snapshot.restore", false, newRecorder(), map[string]any{"vm": "ubuntu-lab", "name": "clean"}); err != nil || !result.Changed {
		t.Errorf("a second restore: %+v, %v", result, err)
	}
}

func TestRestore_OfAnAbortedOrSavedVM(t *testing.T) {
	for _, state := range []string{vboxmanage.StateAborted, vboxmanage.StateSaved} {
		vm := lab()
		vm.State = state
		onModel(t, vboxmanagetest.New(vm))
		if _, err := call(t, "virt.vbox.snapshot.restore", false, newRecorder(), map[string]any{"vm": "ubuntu-lab", "name": "clean"}); err != nil {
			t.Errorf("%s: %v", state, err)
		}
	}
}

func TestRestore_ByUUIDWhenSeveralShareTheName(t *testing.T) {
	vm := twoClean()
	model := vboxmanagetest.New(vm)
	onModel(t, model)
	_, err := call(t, "virt.vbox.snapshot.restore", false, newRecorder(), map[string]any{"vm": "ubuntu-lab", "name": "clean"})
	if err == nil || !strings.Contains(err.Error(), "say which with uuid") || !strings.Contains(err.Error(), cleanUUID+", "+secondUUID) {
		t.Errorf("an ambiguous name: %v", err)
	}
	if _, err := call(t, "virt.vbox.snapshot.restore", false, newRecorder(), map[string]any{"vm": "ubuntu-lab", "name": "clean", "uuid": strings.ToUpper(cleanUUID)}); err != nil || vm.Current != cleanUUID {
		t.Errorf("by UUID: %v, current %s", err, vm.Current)
	}
	_, err = call(t, "virt.vbox.snapshot.restore", false, newRecorder(), map[string]any{"vm": "ubuntu-lab", "name": "clean", "uuid": "00000000-0000-4000-8000-000000000009"})
	if err == nil || !strings.Contains(err.Error(), "has UUID 00000000-0000-4000-8000-000000000009") {
		t.Errorf("a UUID that is none of them: %v", err)
	}
}

func TestRestore_Check(t *testing.T) {
	vm := twoClean()
	vm.Snapshots[1].Name = "later"
	model := vboxmanagetest.New(vm)
	onModel(t, model)
	rc := newRecorder()
	result, err := call(t, "virt.vbox.snapshot.restore", true, rc, map[string]any{"vm": "ubuntu-lab", "name": "clean"})
	if err != nil || !result.Changed || rc.stats["uuid"] != cleanUUID {
		t.Fatalf("%+v, %v, %v", result, err, rc.stats)
	}
	if len(changes(model)) != 0 || vm.Current != secondUUID {
		t.Errorf("a check acted: %v", changes(model))
	}
}

func TestRestore_Refusals(t *testing.T) {
	for _, state := range []string{vboxmanage.StateRunning, vboxmanage.StatePaused} {
		vm := lab()
		vm.State = state
		model := vboxmanagetest.New(vm)
		onModel(t, model)
		for _, check := range []bool{false, true} {
			_, err := call(t, "virt.vbox.snapshot.restore", check, newRecorder(), map[string]any{"vm": "ubuntu-lab", "name": "clean"})
			if err == nil || !strings.Contains(err.Error(), "stop it first with virt.vbox.vm.stop") {
				t.Errorf("%s, check %v: %v", state, check, err)
			}
		}
		if len(changes(model)) != 0 {
			t.Errorf("%s: %v", state, changes(model))
		}
	}
	model := vboxmanagetest.New(lab())
	onModel(t, model)
	if _, err := call(t, "virt.vbox.snapshot.restore", false, newRecorder(), map[string]any{"vm": "ubuntu-lab", "name": "gone"}); err == nil || !strings.Contains(err.Error(), `no snapshot named "gone"`) {
		t.Errorf("a missing snapshot: %v", err)
	}
	model.Fail = map[string]vboxmanage.Output{"VBoxManage.exe snapshot": {ExitCode: 1, Stderr: "VBoxManage.exe: error: Code E_ACCESSDENIED\r\n"}}
	if _, err := call(t, "virt.vbox.snapshot.restore", false, newRecorder(), map[string]any{"vm": "ubuntu-lab", "name": "clean"}); err == nil || !strings.Contains(err.Error(), "E_ACCESSDENIED") {
		t.Errorf("a failed restore: %v", err)
	}
	model.Fail = nil
	rc := newRecorder()
	rc.failOn = "uuid"
	if _, err := call(t, "virt.vbox.snapshot.restore", false, rc, map[string]any{"vm": "ubuntu-lab", "name": "clean"}); err == nil {
		t.Error("recording the uuid failed and the restore did not")
	}
}

// TestRestore_CheckBeforeTheSnapshotOrVMExists covers a check of a
// restore whose snapshot, or whose VM, an earlier task in the same run
// would make: it cannot be checked, rather than failing.
func TestRestore_CheckBeforeTheSnapshotOrVMExists(t *testing.T) {
	model := vboxmanagetest.New(lab())
	onModel(t, model)
	for _, params := range []map[string]any{
		{"vm": "ubuntu-lab", "name": "later"},
		{"vm": "not-yet", "name": "clean"},
	} {
		_, err := call(t, "virt.vbox.snapshot.restore", true, newRecorder(), params)
		var cannot *collection.CannotCheckError
		if !errors.As(err, &cannot) {
			t.Errorf("%v: %v", params, err)
		}
	}
}

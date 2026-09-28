// Tests for virt.vbox.snapshot.take, and for what every snapshot method
// refuses before it reaches a host.
package snapshot

import (
	"reflect"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/vboxmanage"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/vboxmanage/vboxmanagetest"
)

func TestRegistered(t *testing.T) {
	for fqcn, reversible := range map[string]bool{"virt.vbox.snapshot.take": true, "virt.vbox.snapshot.restore": false, "virt.vbox.snapshot.delete": false} {
		d, ok := collection.Lookup(fqcn)
		if !ok {
			t.Fatalf("%s is not registered", fqcn)
		}
		m := d.Manifest
		if m.Status != collection.StatusImplemented || !m.SupportsCheck || d.Check == nil || m.Reversibility.Reversible != reversible {
			t.Errorf("%s: status %v, check %v, reversible %v", fqcn, m.Status, m.SupportsCheck, m.Reversibility.Reversible)
		}
		if !reflect.DeepEqual(m.RequiredCapabilities, []capability.Name{capability.NameVirtualBox}) {
			t.Errorf("%s requires %v", fqcn, m.RequiredCapabilities)
		}
	}
}

func TestTake(t *testing.T) {
	vm := lab()
	vm.Snapshots, vm.Current = nil, ""
	model := vboxmanagetest.New(vm)
	onModel(t, model)
	rc := newRecorder()
	result, err := call(t, "virt.vbox.snapshot.take", false, rc, map[string]any{"vm": "ubuntu-lab", "name": "clean", "description": "fresh install, before any test"})
	if err != nil || !result.Changed {
		t.Fatalf("%+v, %v", result, err)
	}
	if len(vm.Snapshots) != 1 || vm.Snapshots[0].Name != "clean" || vm.Snapshots[0].Description != "fresh install, before any test" {
		t.Fatalf("snapshots = %+v", vm.Snapshots)
	}
	uuid := vm.Snapshots[0].UUID
	if rc.stats["uuid"] != uuid {
		t.Errorf("uuid = %v, want %s", rc.stats["uuid"], uuid)
	}
	if before, after := rc.diff(t); before["exists"] != false || after["exists"] != true {
		t.Errorf("diff = %v -> %v", before, after)
	}
	want := map[string]any{"fqcn": "virt.vbox.snapshot.delete", "params": map[string]any{"vm": "ubuntu-lab", "name": "clean", "uuid": uuid},
		"description": "Delete snapshot clean of ubuntu-lab, which this task took."}
	if !reflect.DeepEqual(rc.stats[sdk.StatInverse], want) {
		t.Errorf("inverse = %v", rc.stats[sdk.StatInverse])
	}

	// A second take under the name takes nothing and reports the first.
	rc = newRecorder()
	result, err = call(t, "virt.vbox.snapshot.take", false, rc, map[string]any{"vm": "ubuntu-lab", "name": "clean"})
	if err != nil || result.Changed || len(vm.Snapshots) != 1 || rc.stats["uuid"] != uuid {
		t.Errorf("a second take: %+v, %v, %d snapshots, stats %v", result, err, len(vm.Snapshots), rc.stats)
	}
	if _, ok := rc.stats[sdk.StatInverse]; ok {
		t.Error("an unchanged take recorded an inverse")
	}
}

func TestTake_OfARunningVM(t *testing.T) {
	vm := lab()
	vm.State = vboxmanage.StateRunning
	onModel(t, vboxmanagetest.New(vm))
	if result, err := call(t, "virt.vbox.snapshot.take", false, newRecorder(), map[string]any{"vm": "ubuntu-lab", "name": "live"}); err != nil || !result.Changed {
		t.Errorf("%+v, %v", result, err)
	}
}

func TestTake_UnderANameSeveralShare(t *testing.T) {
	model := vboxmanagetest.New(twoClean())
	onModel(t, model)
	rc := newRecorder()
	if result, err := call(t, "virt.vbox.snapshot.take", false, rc, map[string]any{"vm": "ubuntu-lab", "name": "clean"}); err != nil || result.Changed {
		t.Fatalf("%+v, %v", result, err)
	}
	if rc.stats["uuid"] != cleanUUID {
		t.Errorf("uuid = %v, want the first listed, %s", rc.stats["uuid"], cleanUUID)
	}
}

func TestTake_Check(t *testing.T) {
	vm := lab()
	model := vboxmanagetest.New(vm)
	onModel(t, model)
	rc := newRecorder()
	result, err := call(t, "virt.vbox.snapshot.take", true, rc, map[string]any{"vm": "ubuntu-lab", "name": "next"})
	if err != nil || !result.Changed {
		t.Fatalf("%+v, %v", result, err)
	}
	if _, ok := rc.stats[sdk.StatInverse]; ok || len(changes(model)) != 0 || len(vm.Snapshots) != 1 {
		t.Errorf("a check acted: %v, %v", rc.stats, changes(model))
	}
	if _, ok := rc.stats["uuid"]; ok {
		t.Error("a check reported a UUID for a snapshot that does not exist")
	}
}

func TestTake_Refusals(t *testing.T) {
	model := vboxmanagetest.New(lab())
	onModel(t, model)
	for name, params := range map[string]map[string]any{
		"a uuid":                 {"vm": "ubuntu-lab", "name": "next", "uuid": cleanUUID},
		"a two-line description": {"vm": "ubuntu-lab", "name": "next", "description": "one\ntwo"},
		"a long description":     {"vm": "ubuntu-lab", "name": "next", "description": strings.Repeat("x", 201)},
	} {
		if _, err := call(t, "virt.vbox.snapshot.take", false, newRecorder(), params); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if calls := model.Calls(); len(calls) != 0 {
		t.Errorf("refused calls reached the host: %v", calls)
	}
	model.Fail = map[string]vboxmanage.Output{"VBoxManage.exe snapshot": {ExitCode: 1, Stderr: "VBoxManage.exe: error: Code E_ACCESSDENIED\r\n"}}
	if _, err := call(t, "virt.vbox.snapshot.take", false, newRecorder(), map[string]any{"vm": "ubuntu-lab", "name": "next"}); err == nil || !strings.Contains(err.Error(), "E_ACCESSDENIED") {
		t.Errorf("a failed take: %v", err)
	}
	model.Fail = nil
	for _, stat := range []string{"uuid", sdk.StatDiff, sdk.StatInverse} {
		rc := newRecorder()
		rc.failOn = stat
		if _, err := call(t, "virt.vbox.snapshot.take", false, rc, map[string]any{"vm": "ubuntu-lab", "name": "take-" + stat}); err == nil {
			t.Errorf("recording %s failed and the take did not", stat)
		}
	}
	rc := newRecorder()
	rc.failOn = "uuid"
	if _, err := call(t, "virt.vbox.snapshot.take", false, rc, map[string]any{"vm": "ubuntu-lab", "name": "clean"}); err == nil {
		t.Error("recording an existing snapshot's uuid failed and the take did not")
	}
}

// TestRefusedBeforeTheHost covers what each method refuses from its
// parameters alone: nothing reaches the host.
func TestRefusedBeforeTheHost(t *testing.T) {
	model := vboxmanagetest.New(lab())
	onModel(t, model)
	for _, fqcn := range []string{"virt.vbox.snapshot.take", "virt.vbox.snapshot.restore", "virt.vbox.snapshot.delete"} {
		for name, params := range map[string]map[string]any{
			"no vm":                  {"name": "clean"},
			"no name":                {"vm": "ubuntu-lab"},
			"a quoted VM":            {"vm": `lab" & calc`, "name": "clean"},
			"a snapshot name with /": {"vm": "ubuntu-lab", "name": "../clean"},
			"a long snapshot name":   {"vm": "ubuntu-lab", "name": strings.Repeat("a", 64)},
			"a uuid that is not one": {"vm": "ubuntu-lab", "name": "clean", "uuid": "clean"},
		} {
			if _, err := call(t, fqcn, false, newRecorder(), params); err == nil || !strings.Contains(err.Error(), fqcn) {
				t.Errorf("%s, %s: err = %v, want a refusal naming the method", fqcn, name, err)
			}
		}
		if _, err := call(t, fqcn, false, newRecorder(), map[string]any{"vm": "no-such-vm", "name": "clean"}); err == nil ||
			!strings.Contains(err.Error(), `host "vengeance" has no VM named "no-such-vm"`) {
			t.Errorf("%s, a missing VM: %v", fqcn, err)
		}
	}
	for _, c := range model.Calls() {
		if !strings.HasPrefix(c, "VBoxManage.exe showvminfo no-such-vm ") {
			t.Errorf("a refused call reached the host: %s", c)
		}
	}
	model.Fail = map[string]vboxmanage.Output{"VBoxManage.exe showvminfo": {ExitCode: 1, Stderr: "VBoxManage.exe: error: Code E_ACCESSDENIED\r\n"}}
	if _, err := call(t, "virt.vbox.snapshot.delete", false, newRecorder(), map[string]any{"vm": "ubuntu-lab", "name": "clean"}); err == nil || !strings.Contains(err.Error(), "E_ACCESSDENIED") {
		t.Errorf("a failed read: %v", err)
	}
}

// TestTheRealHostNeedsTheCapability runs the real hostFunc, which builds
// a host only from a device that declares VirtualBox.
func TestTheRealHostNeedsTheCapability(t *testing.T) {
	plain := &inventorytest.Stub{StubName: "plain", Caps: []capability.Name{capability.NameWinRM}}
	d, _ := collection.Lookup("virt.vbox.snapshot.take")
	_, err := d.Invoke(t.Context(), newRecorder(), plain, map[string]any{"vm": "ubuntu-lab", "name": "clean"})
	if err == nil || !strings.Contains(err.Error(), "virtualbox: true") {
		t.Errorf("err = %v, want the missing capability named", err)
	}
}

// Tests for a VM's second disk: virt.vbox.vm.attach_disk and detach_disk,
// against the model host.
package vm

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/vboxmanage"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/vboxmanage/vboxmanagetest"
)

// osDisk is the lab VM's own disk, in its first SATA port.
const osDisk = `G:\PleiadesLab\ubuntu-lab\ubuntu-lab.vdi`

// dataDisk is where the VM's data disk goes when no path is given.
const dataDisk = `G:\PleiadesLab\ubuntu-lab\ubuntu-lab-data.vdi`

// withSATA is a stopped lab VM with a SATA controller: its own disk in port
// 0 and two free ports.
func withSATA() *vboxmanagetest.VM {
	vm := lab()
	vm.Folder = `G:\PleiadesLab\ubuntu-lab`
	vm.Slots = []vboxmanage.Slot{
		{Controller: "SATA", ControllerType: "IntelAhci", Port: 0, Medium: osDisk},
		{Controller: "SATA", ControllerType: "IntelAhci", Port: 1, Medium: "none"},
		{Controller: "SATA", ControllerType: "IntelAhci", Port: 2, Medium: "none"},
	}
	return vm
}

// modelWith is a model host holding vm, its own disk a file the model has.
func modelWith(vm *vboxmanagetest.VM) *vboxmanagetest.Host {
	model := vboxmanagetest.New(vm)
	model.Files = map[string][]byte{osDisk: make([]byte, 1024)}
	return model
}

func TestDiskMethods_Registered(t *testing.T) {
	for _, fqcn := range []string{"virt.vbox.vm.attach_disk", "virt.vbox.vm.detach_disk"} {
		d, ok := collection.Lookup(fqcn)
		if !ok {
			t.Fatalf("%s is not registered", fqcn)
		}
		m := d.Manifest
		if m.Status != collection.StatusImplemented || !m.SupportsCheck || d.Check == nil || !m.Reversibility.Reversible {
			t.Errorf("%s: status %v, check %v, reversible %v", fqcn, m.Status, m.SupportsCheck, m.Reversibility.Reversible)
		}
	}
}

// TestAttachDisk_MakesAndAttaches: a check predicts a new disk in the first
// free port and sends nothing; a run makes the disk in the VM's folder,
// attaches it there, reads it back, and records a detach that deletes it as
// the inverse, which does.
func TestAttachDisk_MakesAndAttaches(t *testing.T) {
	model := modelWith(withSATA())
	onModel(t, model)
	params := map[string]any{"name": "ubuntu-lab", "size_gb": 40}
	check := newRecorder()
	result, err := call(t, "virt.vbox.vm.attach_disk", true, check, params)
	if err != nil || !result.Changed || len(changes(model)) != 0 {
		t.Fatalf("check: %+v, %v, %v", result, err, changes(model))
	}
	if check.stats["path"] != dataDisk || check.stats["port"] != 1 || check.stats["created"] != true {
		t.Errorf("check stats %v", check.stats)
	}
	rc := newRecorder()
	if result, err := call(t, "virt.vbox.vm.attach_disk", false, rc, params); err != nil || !result.Changed {
		t.Fatalf("%+v, %v", result, err)
	}
	if model.Files[dataDisk] == nil {
		t.Fatal("the disk was not made on the host")
	}
	if slots := model.VM("ubuntu-lab").Slots; slots[1].Medium != dataDisk || slots[2].Medium != "none" {
		t.Fatalf("slots after the attach: %+v", slots)
	}
	if rc.stats["path"] != dataDisk || rc.stats["port"] != 1 || rc.stats["created"] != true {
		t.Errorf("stats %v", rc.stats)
	}
	before, after := rc.diff(t)
	if before["attached"] != false || after["attached"] != true {
		t.Errorf("diff %v -> %v", before, after)
	}
	want := map[string]any{"fqcn": "virt.vbox.vm.detach_disk", "params": map[string]any{"name": "ubuntu-lab", "path": dataDisk, "delete": true},
		"description": "Take " + dataDisk + " off ubuntu-lab and delete it from the host, since this task made it.", sdk.InverseKeepOnFailureKey: true}
	if inverse := rc.stats[sdk.StatInverse]; !reflect.DeepEqual(inverse, want) {
		t.Fatalf("inverse %v\nwant %v", inverse, want)
	}
	made := strings.Join(changes(model), "\n")
	if !strings.Contains(made, "createmedium disk --filename "+dataDisk+" --size 40960") || !strings.Contains(made, "storageattach ubuntu-lab --storagectl SATA --port 1 --device 0 --type hdd --medium "+dataDisk) {
		t.Errorf("the run made:\n%s", made)
	}

	// The inverse takes it off and deletes it.
	undo := newRecorder()
	if result, err := call(t, "virt.vbox.vm.detach_disk", false, undo, want["params"].(map[string]any)); err != nil || !result.Changed {
		t.Fatalf("undo: %+v, %v", result, err)
	}
	if slots := model.VM("ubuntu-lab").Slots; slots[1].Medium != "none" || model.Files[dataDisk] != nil {
		t.Errorf("after the undo: slot %+v, file %v", slots[1], model.Files[dataDisk] != nil)
	}
	if undo.stats["detached"] != true || undo.stats["deleted"] != true || undo.stats[sdk.StatInverse] != nil {
		t.Errorf("undo stats %v: a deleted disk has no inverse", undo.stats)
	}
}

// TestAttachDisk_TakesAnEmptyDVDDrivesPort: with no free SATA port, the port
// holding an empty DVD drive (a Windows clone's ejected seed) is taken: the
// drive is removed and the disk put there.
func TestAttachDisk_TakesAnEmptyDVDDrivesPort(t *testing.T) {
	vm := withSATA()
	vm.Slots = []vboxmanage.Slot{
		{Controller: "SATA", ControllerType: "IntelAhci", Port: 0, Medium: osDisk},
		{Controller: "SATA", ControllerType: "IntelAhci", Port: 1, Medium: "emptydrive"},
	}
	model := modelWith(vm)
	onModel(t, model)
	check := newRecorder()
	if result, err := call(t, "virt.vbox.vm.attach_disk", true, check, map[string]any{"name": "ubuntu-lab", "size_gb": 60}); err != nil || !result.Changed || check.stats["port"] != 1 || len(changes(model)) != 0 {
		t.Fatalf("check: %+v, %v, %v, %v", result, err, check.stats, changes(model))
	}
	rc := newRecorder()
	if result, err := call(t, "virt.vbox.vm.attach_disk", false, rc, map[string]any{"name": "ubuntu-lab", "size_gb": 60}); err != nil || !result.Changed || rc.stats["port"] != 1 {
		t.Fatalf("%+v, %v, %v", result, err, rc.stats)
	}
	if slots := model.VM("ubuntu-lab").Slots; slots[1].Medium != dataDisk {
		t.Errorf("slot 1 holds %q, want the disk", slots[1].Medium)
	}
	made := strings.Join(changes(model), "\n")
	if !strings.Contains(made, "storageattach ubuntu-lab --storagectl SATA --port 1 --device 0 --medium none") || strings.Index(made, "--medium none") > strings.Index(made, "--type hdd") {
		t.Errorf("the empty drive was not removed before the disk went in:\n%s", made)
	}
}

// TestAttachDisk_AlreadyAttached: a disk already in a slot is no change,
// even while the VM runs, and nothing is sent.
func TestAttachDisk_AlreadyAttached(t *testing.T) {
	vm := withSATA()
	vm.State = vboxmanage.StateRunning
	vm.Slots[1].Medium = dataDisk
	model := modelWith(vm)
	onModel(t, model)
	for _, check := range []bool{true, false} {
		rc := newRecorder()
		result, err := call(t, "virt.vbox.vm.attach_disk", check, rc, map[string]any{"name": "ubuntu-lab"})
		if err != nil || result.Changed || rc.stats["port"] != 1 || rc.stats["created"] != false || rc.stats[sdk.StatInverse] != nil {
			t.Fatalf("check %v: %+v, %v, %v", check, result, err, rc.stats)
		}
	}
	if c := changes(model); len(c) != 0 {
		t.Errorf("calls %v", c)
	}
}

// TestAttachDisk_AnExistingDiskIsAttachedNotMade: a disk the host holds is
// attached as it is, with no size needed, and its inverse keeps it; the
// detach that follows records an attach as its own inverse.
func TestAttachDisk_AnExistingDiskIsAttachedNotMade(t *testing.T) {
	model := modelWith(withSATA())
	onModel(t, model)
	const kept = `G:\PleiadesLab\shared\scratch.vdi`
	if err := model.VBoxHost().CreateDisk(context.Background(), kept, 2048); err != nil {
		t.Fatal(err)
	}
	rc := newRecorder()
	if result, err := call(t, "virt.vbox.vm.attach_disk", false, rc, map[string]any{"name": "ubuntu-lab", "path": kept}); err != nil || !result.Changed {
		t.Fatalf("%+v, %v", result, err)
	}
	if rc.stats["created"] != false || rc.stats["port"] != 1 {
		t.Errorf("stats %v", rc.stats)
	}
	inverse, _ := rc.stats[sdk.StatInverse].(map[string]any)
	if params, _ := inverse["params"].(map[string]any); params["delete"] != false {
		t.Errorf("the inverse of attaching a disk the host had deletes it: %v", inverse)
	}
	detach := newRecorder()
	if result, err := call(t, "virt.vbox.vm.detach_disk", false, detach, map[string]any{"name": "ubuntu-lab", "path": kept}); err != nil || !result.Changed {
		t.Fatalf("detach: %+v, %v", result, err)
	}
	if model.Files[kept] == nil || detach.stats["deleted"] != false {
		t.Error("a detach that was not asked to delete deleted the disk")
	}
	want := map[string]any{"fqcn": "virt.vbox.vm.attach_disk", "params": map[string]any{"name": "ubuntu-lab", "path": kept},
		"description": "Attach " + kept + " to ubuntu-lab again, as it was before this task.", sdk.InverseKeepOnFailureKey: true}
	if !reflect.DeepEqual(detach.stats[sdk.StatInverse], want) {
		t.Errorf("detach inverse %v\nwant %v", detach.stats[sdk.StatInverse], want)
	}
}

// TestAttachDisk_Refusals: a running or paused VM is unchecked in a check
// and refused in a run; a saved one is refused either way; a new disk with
// no size, a path that is not a host path, a VM with no free SATA port and a
// VM that is not there are refused, each naming the fix.
func TestAttachDisk_Refusals(t *testing.T) {
	inState := func(state string) *vboxmanagetest.VM {
		vm := withSATA()
		vm.State = state
		return vm
	}
	full := withSATA()
	full.Slots = full.Slots[:1]
	for name, tc := range map[string]struct {
		vm     *vboxmanagetest.VM
		params map[string]any
		want   string
	}{
		"running":           {inState(vboxmanage.StateRunning), map[string]any{"name": "ubuntu-lab", "size_gb": 1}, "stop it first with virt.vbox.vm.stop"},
		"saved":             {inState(vboxmanage.StateSaved), map[string]any{"name": "ubuntu-lab", "size_gb": 1}, "is saved; VirtualBox attaches a disk"},
		"no size for a new": {withSATA(), map[string]any{"name": "ubuntu-lab"}, "size_gb says how big"},
		"a size under 1":    {withSATA(), map[string]any{"name": "ubuntu-lab", "size_gb": 0}, "size_gb must be at least 1"},
		"not a host path":   {withSATA(), map[string]any{"name": "ubuntu-lab", "path": "/tmp/x.vdi", "size_gb": 1}, "is not an absolute path"},
		"no free port":      {full, map[string]any{"name": "ubuntu-lab", "size_gb": 1}, "no free SATA port"},
		"no such VM":        {withSATA(), map[string]any{"name": "other", "size_gb": 1}, `has no VM named "other"`},
	} {
		t.Run(name, func(t *testing.T) {
			model := modelWith(tc.vm)
			onModel(t, model)
			_, err := call(t, "virt.vbox.vm.attach_disk", false, newRecorder(), tc.params)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err %v, want %q", err, tc.want)
			}
			if c := changes(model); len(c) != 0 {
				t.Errorf("a refusal sent %v", c)
			}
		})
	}
	for _, state := range []string{vboxmanage.StateRunning, vboxmanage.StatePaused} {
		onModel(t, modelWith(inState(state)))
		_, err := call(t, "virt.vbox.vm.attach_disk", true, newRecorder(), map[string]any{"name": "ubuntu-lab", "size_gb": 1})
		var cannot *collection.CannotCheckError
		if !errors.As(err, &cannot) || !strings.Contains(cannot.Reason, "unless an earlier task stops it") {
			t.Errorf("a check of a %s VM: %v", state, err)
		}
	}
}

// TestDiskMethods_EachStepFailing walks the failure paths after a change:
// an attach that fails after this task made the disk deletes it again, so
// nothing is orphaned; a read-back that fails after the attach leaves the
// detach in the journal, kept through the failure, with a warning; a delete
// that fails after the detach leaves the attach in the journal the same way.
func TestDiskMethods_EachStepFailing(t *testing.T) {
	refused := vboxmanage.Output{ExitCode: 1, Stderr: "VBoxManage.exe: error: refused\r\n"}

	// The attach refused: the disk the task made is deleted again.
	model := modelWith(withSATA())
	model.Fail = map[string]vboxmanage.Output{"VBoxManage.exe storageattach": refused}
	onModel(t, model)
	rc := newRecorder()
	_, err := call(t, "virt.vbox.vm.attach_disk", false, rc, map[string]any{"name": "ubuntu-lab", "size_gb": 1})
	if err == nil || !strings.Contains(err.Error(), "refused") || !strings.Contains(err.Error(), "the disk this task made at "+dataDisk+" was deleted again") {
		t.Errorf("a refused attach: %v", err)
	}
	if model.Files[dataDisk] != nil || rc.stats[sdk.StatInverse] != nil {
		t.Errorf("a refused attach left the disk (%v) or an undo (%v)", model.Files[dataDisk] != nil, rc.stats[sdk.StatInverse])
	}
	// A disk the host already had is not deleted when the attach fails.
	model = modelWith(withSATA())
	if err := model.VBoxHost().CreateDisk(context.Background(), dataDisk, 1024); err != nil {
		t.Fatal(err)
	}
	model.Fail = map[string]vboxmanage.Output{"VBoxManage.exe storageattach": refused}
	onModel(t, model)
	if _, err := call(t, "virt.vbox.vm.attach_disk", false, newRecorder(), map[string]any{"name": "ubuntu-lab"}); err == nil || strings.Contains(err.Error(), "deleted again") || model.Files[dataDisk] == nil {
		t.Errorf("a refused attach of the host's own disk: %v, file kept %v", err, model.Files[dataDisk] != nil)
	}

	// The read-back after the attach fails: the disk is on the VM, its undo
	// is journaled and kept, and the task passes with a warning.
	model = modelWith(withSATA())
	model.Fail = map[string]vboxmanage.Output{"VBoxManage.exe showvminfo": refused}
	model.Skip = map[string]int{"VBoxManage.exe showvminfo": 1}
	onModel(t, model)
	rc = newRecorder()
	result, err := call(t, "virt.vbox.vm.attach_disk", false, rc, map[string]any{"name": "ubuntu-lab", "size_gb": 1})
	if err != nil || !result.Changed || rc.stats["port"] != 1 {
		t.Fatalf("a failed read-back: %+v, %v, %v", result, err, rc.stats)
	}
	inverse, _ := rc.stats[sdk.StatInverse].(map[string]any)
	if inverse[sdk.InverseKeepOnFailureKey] != true || inverse["fqcn"] != "virt.vbox.vm.detach_disk" {
		t.Errorf("the undo is not kept through a failure: %v", inverse)
	}
	if w, _ := rc.stats[sdk.StatWarnings].([]string); len(w) != 1 || !strings.Contains(w[0], "could not be read back") {
		t.Errorf("warnings = %v", rc.stats[sdk.StatWarnings])
	}

	// The delete after the detach fails: the disk is off the VM, and the
	// attach that puts it back is journaled and kept.
	vm := withSATA()
	vm.Slots[1].Medium = dataDisk
	model = modelWith(vm)
	// The disk is registered on the host, as an attached disk is.
	if err := model.VBoxHost().CreateDisk(context.Background(), dataDisk, 1024); err != nil {
		t.Fatal(err)
	}
	model.Fail = map[string]vboxmanage.Output{"VBoxManage.exe closemedium": refused}
	onModel(t, model)
	rc = newRecorder()
	if _, err := call(t, "virt.vbox.vm.detach_disk", false, rc, map[string]any{"name": "ubuntu-lab", "path": dataDisk, "delete": true}); err == nil || !strings.Contains(err.Error(), "is off \"ubuntu-lab\" and could not be deleted") {
		t.Errorf("a refused delete: %v", err)
	}
	if slots := model.VM("ubuntu-lab").Slots; slots[1].Medium != "none" {
		t.Errorf("the disk is still in slot 1: %+v", slots[1])
	}
	inverse, _ = rc.stats[sdk.StatInverse].(map[string]any)
	if inverse[sdk.InverseKeepOnFailureKey] != true || inverse["fqcn"] != "virt.vbox.vm.attach_disk" {
		t.Errorf("the attach that puts the disk back is not journaled and kept: %v", inverse)
	}

	// A detach whose read-back fails passes with a warning, its undo kept.
	vm = withSATA()
	vm.Slots[1].Medium = dataDisk
	model = modelWith(vm)
	model.Fail = map[string]vboxmanage.Output{"VBoxManage.exe showvminfo": refused}
	model.Skip = map[string]int{"VBoxManage.exe showvminfo": 1}
	onModel(t, model)
	rc = newRecorder()
	if result, err := call(t, "virt.vbox.vm.detach_disk", false, rc, map[string]any{"name": "ubuntu-lab", "path": dataDisk}); err != nil || !result.Changed {
		t.Fatalf("a failed read-back after a detach: %+v, %v", result, err)
	}
	inverse, _ = rc.stats[sdk.StatInverse].(map[string]any)
	if inverse[sdk.InverseKeepOnFailureKey] != true || rc.stats[sdk.StatWarnings] == nil {
		t.Errorf("inverse %v, warnings %v", inverse, rc.stats[sdk.StatWarnings])
	}
}

// TestDetachDisk_NotAttached: a disk no slot holds is no change, and with
// delete, one the host still holds is deleted anyway, as the one left
// behind by a VM that was deleted would be.
func TestDetachDisk_NotAttached(t *testing.T) {
	model := modelWith(withSATA())
	onModel(t, model)
	const stray = `G:\PleiadesLab\stray.vdi`
	if err := model.VBoxHost().CreateDisk(context.Background(), stray, 1024); err != nil {
		t.Fatal(err)
	}
	rc := newRecorder()
	if result, err := call(t, "virt.vbox.vm.detach_disk", false, rc, map[string]any{"name": "ubuntu-lab", "path": stray}); err != nil || result.Changed || rc.stats["detached"] != false {
		t.Fatalf("%+v, %v, %v", result, err, rc.stats)
	}
	check := newRecorder()
	if result, err := call(t, "virt.vbox.vm.detach_disk", true, check, map[string]any{"name": "ubuntu-lab", "path": stray, "delete": true}); err != nil || !result.Changed || check.stats["deleted"] != true || model.Files[stray] == nil {
		t.Fatalf("check with delete: %+v, %v, %v", result, err, check.stats)
	}
	rc = newRecorder()
	if result, err := call(t, "virt.vbox.vm.detach_disk", false, rc, map[string]any{"name": "ubuntu-lab", "path": stray, "delete": true}); err != nil || !result.Changed || rc.stats["deleted"] != true || model.Files[stray] != nil {
		t.Fatalf("delete: %+v, %v, %v", result, err, rc.stats)
	}
	if _, err := call(t, "virt.vbox.vm.detach_disk", false, newRecorder(), map[string]any{"name": "ubuntu-lab", "path": stray, "delete": "yes"}); err == nil || !strings.Contains(err.Error(), "delete must be true or false") {
		t.Errorf("a delete that is not a bool: %v", err)
	}
	vm := withSATA()
	vm.State = vboxmanage.StateRunning
	vm.Slots[1].Medium = dataDisk
	onModel(t, modelWith(vm))
	if _, err := call(t, "virt.vbox.vm.detach_disk", false, newRecorder(), map[string]any{"name": "ubuntu-lab", "path": dataDisk}); err == nil || !strings.Contains(err.Error(), "detaches a disk from a VM only while it is powered off") {
		t.Errorf("a running VM: %v", err)
	}
}

// TestDataDiskPath reads the VM's folder from its settings file's path,
// whichever separator the host wrote, and gives nothing with no folder.
func TestDataDiskPath(t *testing.T) {
	for cfg, want := range map[string]string{
		`G:\PleiadesLab\bsd-check\bsd-check.vbox`: `G:\PleiadesLab\bsd-check\bsd-check-data.vdi`,
		"G:/PleiadesLab/bsd-check/bsd-check.vbox": `G:\PleiadesLab\bsd-check\bsd-check-data.vdi`,
		"":               "",
		"bsd-check.vbox": "",
	} {
		if got := dataDiskPath(vboxmanage.Machine{ConfigFile: cfg}, "bsd-check"); got != want {
			t.Errorf("dataDiskPath(%q) = %q, want %q", cfg, got, want)
		}
	}
}

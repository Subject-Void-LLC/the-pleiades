// Tests of the failures virt.vbox.vm.attach_disk and detach_disk meet before
// and after a change: bad parameters, the host refusing each step, and the
// context refusing each stat, against the model host.
package vm

import (
	"context"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/vboxmanage"
)

// refusedOutput is VBoxManage refusing a verb.
var refusedOutput = vboxmanage.Output{ExitCode: 1, Stderr: "VBoxManage.exe: error: refused\r\n"}

// TestAttachDisk_FailsOnEachRefusal: bad parameters, a VM with no known
// folder, and the host refusing the list of disks, the make, or the removal
// of an empty DVD drive each fail the task; a disk this task made is deleted
// again when the drive cannot be removed, and a delete that fails too is
// named.
func TestAttachDisk_FailsOnEachRefusal(t *testing.T) {
	noFolder := withSATA()
	noFolder.Folder = ""
	for name, tc := range map[string]struct {
		params map[string]any
		fail   string
		want   string
	}{
		"no name":             {map[string]any{"size_gb": 1}, "", "name"},
		"a size that is text": {map[string]any{"name": "ubuntu-lab", "size_gb": "big"}, "", "size_gb"},
		"no list of disks":    {map[string]any{"name": "ubuntu-lab", "size_gb": 1}, "VBoxManage.exe list hdds", "refused"},
		"the make is refused": {map[string]any{"name": "ubuntu-lab", "size_gb": 1}, "VBoxManage.exe createmedium", "refused"},
	} {
		t.Run(name, func(t *testing.T) {
			model := modelWith(withSATA())
			if tc.fail != "" {
				model.Fail = map[string]vboxmanage.Output{tc.fail: refusedOutput}
			}
			onModel(t, model)
			_, err := call(t, "virt.vbox.vm.attach_disk", false, newRecorder(), tc.params)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err %v, want %q", err, tc.want)
			}
			if model.Files[dataDisk] != nil {
				t.Error("a failed attach left a disk on the host")
			}
		})
	}

	// A VM whose folder the host does not report needs a path.
	onModel(t, modelWith(noFolder))
	if _, err := call(t, "virt.vbox.vm.attach_disk", false, newRecorder(), map[string]any{"name": "ubuntu-lab", "size_gb": 1}); err == nil || !strings.Contains(err.Error(), "folder on the host is not known") {
		t.Errorf("a VM with no folder: %v", err)
	}

	// The empty DVD drive cannot be removed: the disk made is deleted again,
	// and when that delete is refused too, the error says the disk is left.
	for _, deleteRefused := range []bool{false, true} {
		vm := withSATA()
		vm.Slots = vm.Slots[:2]
		vm.Slots[1].Medium = "emptydrive"
		model := modelWith(vm)
		model.Fail = map[string]vboxmanage.Output{"VBoxManage.exe storageattach": refusedOutput}
		if deleteRefused {
			model.Fail["VBoxManage.exe closemedium"] = refusedOutput
		}
		onModel(t, model)
		rc := newRecorder()
		_, err := call(t, "virt.vbox.vm.attach_disk", false, rc, map[string]any{"name": "ubuntu-lab", "size_gb": 1})
		want := "was deleted again"
		if deleteRefused {
			want = "could not be deleted again"
		}
		if err == nil || !strings.Contains(err.Error(), "removing the empty DVD drive") || !strings.Contains(err.Error(), want) {
			t.Errorf("delete refused %v: %v, want %q", deleteRefused, err, want)
		}
		if rc.stats[sdk.StatInverse] != nil {
			t.Errorf("a disk never attached journaled an undo: %v", rc.stats[sdk.StatInverse])
		}
	}
}

// TestAttachDisk_FailsWhenAStatCannotBeRecorded: the path, the diff, the
// undo and a warning each failing to record fail the task, in a run, in a
// check, and for a disk already attached.
func TestAttachDisk_FailsWhenAStatCannotBeRecorded(t *testing.T) {
	for _, key := range []string{statPath, sdk.StatDiff, sdk.StatInverse} {
		model := modelWith(withSATA())
		onModel(t, model)
		failing := newRecorder()
		failing.failOn = key
		if _, err := call(t, "virt.vbox.vm.attach_disk", false, failing, map[string]any{"name": "ubuntu-lab", "size_gb": 1}); err == nil || !strings.Contains(err.Error(), "recorder: refusing") {
			t.Errorf("a run refusing %s: %v", key, err)
		}
	}
	onModel(t, modelWith(withSATA()))
	failing := newRecorder()
	failing.failOn = sdk.StatDiff
	if _, err := call(t, "virt.vbox.vm.attach_disk", true, failing, map[string]any{"name": "ubuntu-lab", "size_gb": 1}); err == nil {
		t.Error("a check refusing the diff passed")
	}
	attached := withSATA()
	attached.Slots[1].Medium = dataDisk
	onModel(t, modelWith(attached))
	failing = newRecorder()
	failing.failOn = statPath
	if _, err := call(t, "virt.vbox.vm.attach_disk", false, failing, map[string]any{"name": "ubuntu-lab"}); err == nil {
		t.Error("a disk already attached passed with its path refused")
	}
	// The warning of a failed read-back cannot be recorded.
	model := modelWith(withSATA())
	model.Fail = map[string]vboxmanage.Output{"VBoxManage.exe showvminfo": refusedOutput}
	model.Skip = map[string]int{"VBoxManage.exe showvminfo": 1}
	onModel(t, model)
	failing = newRecorder()
	failing.failOn = sdk.StatWarnings
	if _, err := call(t, "virt.vbox.vm.attach_disk", false, failing, map[string]any{"name": "ubuntu-lab", "size_gb": 1}); err == nil || !strings.Contains(err.Error(), "recorder: refusing") {
		t.Errorf("a refused warning: %v", err)
	}
}

// TestDetachDisk_FailsOnEachRefusal: a missing or relative path, the host
// refusing the list of disks, the detach, or the delete of a disk no slot
// holds, and the context refusing each stat, each fail the task.
func TestDetachDisk_FailsOnEachRefusal(t *testing.T) {
	for name, tc := range map[string]struct {
		params   map[string]any
		attached bool
		fail     string
		failOn   string
		want     string
	}{
		"no name":                 {map[string]any{"path": dataDisk}, true, "", "", "name"},
		"no path":                 {map[string]any{"name": "ubuntu-lab"}, true, "", "", "path"},
		"a relative path":         {map[string]any{"name": "ubuntu-lab", "path": "x.vdi"}, true, "", "", "is not an absolute path"},
		"no list of disks":        {map[string]any{"name": "ubuntu-lab", "path": dataDisk, "delete": true}, true, "VBoxManage.exe list hdds", "", "refused"},
		"the detach is refused":   {map[string]any{"name": "ubuntu-lab", "path": dataDisk}, true, "VBoxManage.exe storageattach", "", "refused"},
		"a stray delete refused":  {map[string]any{"name": "ubuntu-lab", "path": dataDisk, "delete": true}, false, "VBoxManage.exe closemedium", "", "refused"},
		"the undo cannot record":  {map[string]any{"name": "ubuntu-lab", "path": dataDisk}, true, "", sdk.StatInverse, "recorder: refusing"},
		"the stats cannot record": {map[string]any{"name": "ubuntu-lab", "path": dataDisk}, true, "", statDetached, "recorder: refusing"},
		"the diff cannot record":  {map[string]any{"name": "ubuntu-lab", "path": dataDisk}, true, "", sdk.StatDiff, "recorder: refusing"},
		"undo and delete refused": {map[string]any{"name": "ubuntu-lab", "path": dataDisk, "delete": true}, true, "VBoxManage.exe closemedium", sdk.StatInverse, "recorder: refusing"},
	} {
		t.Run(name, func(t *testing.T) {
			vm := withSATA()
			if tc.attached {
				vm.Slots[1].Medium = dataDisk
			}
			model := modelWith(vm)
			if err := model.VBoxHost().CreateDisk(context.Background(), dataDisk, 1024); err != nil {
				t.Fatal(err)
			}
			if tc.fail != "" {
				model.Fail = map[string]vboxmanage.Output{tc.fail: refusedOutput}
			}
			onModel(t, model)
			rc := newRecorder()
			rc.failOn = tc.failOn
			if _, err := call(t, "virt.vbox.vm.detach_disk", false, rc, tc.params); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err %v, want %q", err, tc.want)
			}
		})
	}
	// The warning of a failed read-back after a detach cannot be recorded.
	vm := withSATA()
	vm.Slots[1].Medium = dataDisk
	model := modelWith(vm)
	model.Fail = map[string]vboxmanage.Output{"VBoxManage.exe showvminfo": refusedOutput}
	model.Skip = map[string]int{"VBoxManage.exe showvminfo": 1}
	onModel(t, model)
	failing := newRecorder()
	failing.failOn = sdk.StatWarnings
	if _, err := call(t, "virt.vbox.vm.detach_disk", false, failing, map[string]any{"name": "ubuntu-lab", "path": dataDisk}); err == nil || !strings.Contains(err.Error(), "recorder: refusing") {
		t.Errorf("a refused warning: %v", err)
	}
}

// virt.vbox.vm.delete on a registration VirtualBox cannot read.
package vm

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/vboxmanage"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/vboxmanage/vboxmanagetest"
)

// TestDelete_AnInaccessibleVMIsUnregisteredOnly: a VM list vms names
// <inaccessible>, named by its UUID, is checked and then unregistered with
// nothing deleted, and the others are left alone; a UUID guard that names
// another VM refuses it; a UUID no inaccessible VM has is an ordinary name.
func TestDelete_AnInaccessibleVMIsUnregisteredOnly(t *testing.T) {
	const ghost = "9883f6c3-b17d-4077-aa52-d9c49a6912a5"
	other := running()
	model := vboxmanagetest.New(&vboxmanagetest.VM{Name: "gone", UUID: ghost, State: vboxmanage.StatePoweroff, Inaccessible: true}, other)
	onModel(t, model)

	if _, err := call(t, "virt.vbox.vm.delete", false, newRecorder(), map[string]any{"name": ghost, "uuid": other.UUID}); err == nil || !strings.Contains(err.Error(), "nothing was unregistered") {
		t.Errorf("a guard naming another VM = %v", err)
	}
	check := newRecorder()
	if res, err := call(t, "virt.vbox.vm.delete", true, check, map[string]any{"name": ghost}); err != nil || !res.Changed || check.stats[statInaccessible] != true {
		t.Fatalf("a check = %+v, %v, %v", res, err, check.stats)
	}
	if model.VM(ghost) == nil {
		t.Fatal("a check unregistered the VM")
	}
	rc := newRecorder()
	if res, err := call(t, "virt.vbox.vm.delete", false, rc, map[string]any{"name": ghost}); err != nil || !res.Changed {
		t.Fatalf("delete = %+v, %v", res, err)
	}
	if model.VM(ghost) != nil || model.VM(other.Name) == nil {
		t.Errorf("after: the inaccessible VM %v, the other %v", model.VM(ghost), model.VM(other.Name))
	}
	for _, c := range model.Calls() {
		if strings.Contains(c, "--delete") {
			t.Errorf("an inaccessible VM's files were asked to be deleted: %s", c)
		}
	}
	if before, after := rc.diff(t); before["exists"] != true || after["exists"] != false {
		t.Errorf("diff %v -> %v", before, after)
	}
	// Gone now, so the same UUID is a name with no VM: no change.
	if res, err := call(t, "virt.vbox.vm.delete", false, newRecorder(), map[string]any{"name": ghost}); err != nil || res.Changed {
		t.Errorf("again = %+v, %v", res, err)
	}
}

// TestDelete_AnInaccessibleVMsFailures: a list that fails, a stat that cannot
// be kept and an unregister VirtualBox refuses each fail the task, and the
// registration is still there after the last two.
func TestDelete_AnInaccessibleVMsFailures(t *testing.T) {
	const ghost = "9883f6c3-b17d-4077-aa52-d9c49a6912a5"
	refused := vboxmanage.Output{ExitCode: 1, Stderr: "VBoxManage.exe: error: E_ACCESSDENIED\r\n"}
	for name, tc := range map[string]struct {
		fail   string
		failOn string
	}{
		"the list fails":        {fail: "VBoxManage.exe list vms"},
		"the stat is refused":   {failOn: statUUID},
		"unregister is refused": {fail: "VBoxManage.exe unregistervm " + ghost},
	} {
		model := vboxmanagetest.New(&vboxmanagetest.VM{Name: "gone", UUID: ghost, State: vboxmanage.StatePoweroff, Inaccessible: true})
		if tc.fail != "" {
			model.Fail = map[string]vboxmanage.Output{tc.fail: refused}
		}
		onModel(t, model)
		rc := newRecorder()
		rc.failOn = tc.failOn
		if _, err := call(t, "virt.vbox.vm.delete", false, rc, map[string]any{"name": ghost}); err == nil {
			t.Errorf("%s: the task passed", name)
		}
		if tc.fail != "VBoxManage.exe list vms" && model.VM(ghost) == nil {
			t.Errorf("%s: the registration is gone anyway", name)
		}
	}
}

// virt.vbox.vm.delete on a VM whose state virt.vbox.vm.stop saved.
package vm

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/vboxmanage"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/vboxmanage/vboxmanagetest"
)

// TestDelete_ASavedVMIsDiscardedFirst: a saved VM counts as off, but
// VirtualBox refuses to detach its seed image while the saved state holds
// it, so the delete discards that state first, and only on a real run: a
// check sends nothing. A discard VirtualBox refuses fails the task with the
// VM still registered.
func TestDelete_ASavedVMIsDiscardedFirst(t *testing.T) {
	model := vboxmanagetest.New(base())
	onModel(t, model)
	if _, err := call(t, "virt.vbox.vm.clone", false, seeded(), cloneParams()); err != nil {
		t.Fatal(err)
	}
	model.VM("ubuntu-lab").State = vboxmanage.StateSaved
	seed := defaultHome + `\ubuntu-lab\seed.iso`

	cloned := len(changes(model))
	if res, err := call(t, "virt.vbox.vm.delete", true, newRecorder(), map[string]any{"name": "ubuntu-lab"}); err != nil || !res.Changed {
		t.Fatalf("check: %+v, %v", res, err)
	}
	if sent := changes(model)[cloned:]; len(sent) != 0 {
		t.Fatalf("a check changed the host: %v", sent)
	}

	model.Fail = map[string]vboxmanage.Output{"VBoxManage.exe discardstate": {ExitCode: 1, Stderr: "VBoxManage.exe: error: E_FAIL\r\n"}}
	if _, err := call(t, "virt.vbox.vm.delete", false, newRecorder(), map[string]any{"name": "ubuntu-lab"}); err == nil || !strings.Contains(err.Error(), "E_FAIL") {
		t.Errorf("a refused discard: %v", err)
	}
	if model.VM("ubuntu-lab") == nil {
		t.Fatal("a refused discard deleted the VM anyway")
	}

	model.Fail = nil
	before := len(model.Calls())
	if res, err := call(t, "virt.vbox.vm.delete", false, newRecorder(), map[string]any{"name": "ubuntu-lab"}); err != nil || !res.Changed || model.VM("ubuntu-lab") != nil {
		t.Fatalf("delete: %+v, %v", res, err)
	}
	if _, left := model.Files[seed]; left {
		t.Error("the seed image is left")
	}
	discarded, detached := -1, -1
	for i, c := range model.Calls()[before:] {
		switch {
		case strings.HasPrefix(c, "VBoxManage.exe discardstate ubuntu-lab"):
			discarded = i
		case strings.HasPrefix(c, "VBoxManage.exe storageattach ubuntu-lab") && detached < 0:
			detached = i
		}
	}
	if discarded < 0 || detached < discarded {
		t.Errorf("discardstate at %d, the first detach at %d: the state must go first\n%s", discarded, detached, strings.Join(model.Calls()[before:], "\n"))
	}
}

// Tests for which DVD images virt.vbox.vm.delete deletes with a VM: only
// one in the VM's own folder, decided by path rather than by text.
package vm

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/vboxmanage/vboxmanagetest"
)

// TestDelete_AnImageOutsideTheFolderByNameIsKept: a DVD image whose path
// begins with the VM's folder's text but climbs out of it, through ".."
// written with a forward slash or with a backslash, is somewhere else, so
// the delete detaches it and keeps it, as it keeps a shared installer ISO.
// Before 2026-11-13 the folder test compared text, and CheckPath read only
// backslashes, so the forward-slash one went to closemedium --delete and
// was deleted, and the backslash one failed the delete.
func TestDelete_AnImageOutsideTheFolderByNameIsKept(t *testing.T) {
	folder := defaultHome + `\ubuntu-lab`
	for _, shared := range []string{folder + `/../../iso/tools.iso`, folder + `\..\..\iso\tools.iso`} {
		model := vboxmanagetest.New(base())
		onModel(t, model)
		if _, err := call(t, "virt.vbox.vm.clone", false, seeded(), cloneParams()); err != nil {
			t.Fatal(err)
		}
		model.SetFile(shared, []byte("shared"))
		model.VM("ubuntu-lab").Slots[1].Medium = shared
		if _, err := call(t, "virt.vbox.vm.delete", false, newRecorder(), map[string]any{"name": "ubuntu-lab"}); err != nil {
			t.Fatalf("%q: %v", shared, err)
		}
		if model.VM("ubuntu-lab") != nil {
			t.Errorf("%q: the VM is still there", shared)
		}
		if _, ok := model.Files[shared]; !ok {
			t.Errorf("%q, outside the VM's folder, was deleted with it", shared)
		}
		for _, c := range model.Calls() {
			if strings.Contains(c, "closemedium dvd") && strings.Contains(c, "tools.iso") {
				t.Errorf("%q: the delete sent %s", shared, c)
			}
		}
	}
}

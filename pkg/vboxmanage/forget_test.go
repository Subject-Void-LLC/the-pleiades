// Forget and Ref.Inaccessible, against the model host.
package vboxmanage_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/vboxmanage"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/vboxmanage/vboxmanagetest"
)

// TestForget_UnregistersByUUIDOnly: an inaccessible VM lists under its
// placeholder name and is forgotten by its UUID; a name that is not a UUID
// is refused before VirtualBox is asked.
func TestForget_UnregistersByUUIDOnly(t *testing.T) {
	const ghost = "9883f6c3-b17d-4077-aa52-d9c49a6912a5"
	model := vboxmanagetest.New(&vboxmanagetest.VM{Name: "gone", UUID: ghost, State: vboxmanage.StatePoweroff, Inaccessible: true})
	h := model.VBoxHost()
	ctx := context.Background()
	refs, err := h.List(ctx)
	if err != nil || len(refs) != 1 || !refs[0].Inaccessible() || refs[0].Name != vboxmanage.InaccessibleName {
		t.Fatalf("list = %+v, %v", refs, err)
	}
	if err := h.Forget(ctx, "gone"); err == nil {
		t.Error("a name that is not a UUID was forgotten")
	}
	if err := h.Forget(ctx, ghost); err != nil {
		t.Fatal(err)
	}
	if refs, _ := h.List(ctx); len(refs) != 0 {
		t.Errorf("after forgetting: %+v", refs)
	}
	if (vboxmanage.Ref{Name: "lab"}).Inaccessible() {
		t.Error("a named VM reads as inaccessible")
	}
}

// TestForget_RefusesABaseWithLinkedClones: a base whose snapshot a linked
// clone was made from is not forgotten, whether VirtualBox can read it or
// not, and the refusal names the clone; once the clone is gone, the base
// is forgotten. Forgetting the base would leave the clone's disk
// descending from a disk no registered machine owns.
func TestForget_RefusesABaseWithLinkedClones(t *testing.T) {
	const snap = "3c7f4f7e-2a47-4a4d-9b0e-6a4d2f1b8c11"
	for _, unreadable := range []bool{false, true} {
		base := &vboxmanagetest.VM{Name: "ws2025-base", UUID: "7d0b3f7c-4a30-4c43-9b3b-2d8f31c4d7a1", State: vboxmanage.StatePoweroff,
			Folder: `G:\PleiadesLab\ws2025-base`, Snapshots: []vboxmanagetest.Snapshot{{Name: "base", UUID: snap}}, Current: snap,
			Slots: []vboxmanage.Slot{{Controller: "SATA", Medium: `G:\PleiadesLab\ws2025-base\ws2025-base.vdi`}}}
		model := vboxmanagetest.New(base)
		h := model.VBoxHost()
		ctx := context.Background()
		if err := h.CloneLinked(ctx, "ws2025-base", snap, "win-lab", `G:\PleiadesLab`); err != nil {
			t.Fatal(err)
		}
		clone := model.VM("win-lab")
		base.Inaccessible = unreadable
		err := h.Forget(ctx, base.UUID)
		if err == nil || !strings.Contains(err.Error(), "win-lab ("+clone.UUID+")") {
			t.Fatalf("unreadable %v: forgetting a base with a linked clone: %v", unreadable, err)
		}
		if model.VM(base.UUID) == nil {
			t.Fatalf("unreadable %v: the base was forgotten anyway", unreadable)
		}
		for _, c := range model.Calls() {
			if strings.HasPrefix(c, "VBoxManage.exe unregistervm") {
				t.Errorf("unreadable %v: a refused forget still ran %s", unreadable, c)
			}
		}
		if err := h.Unregister(ctx, "win-lab"); err != nil {
			t.Fatal(err)
		}
		if err := h.Forget(ctx, base.UUID); err != nil || model.VM(base.UUID) != nil {
			t.Errorf("unreadable %v: with its clone gone, forgetting the base: %v", unreadable, err)
		}
	}
}

// TestForget_ReadingTheDisksFails: a failed read of the host's disks is
// the error, and nothing is forgotten on a guess.
func TestForget_ReadingTheDisksFails(t *testing.T) {
	const ghost = "9883f6c3-b17d-4077-aa52-d9c49a6912a5"
	model := vboxmanagetest.New(&vboxmanagetest.VM{Name: "gone", UUID: ghost, State: vboxmanage.StatePoweroff, Inaccessible: true})
	model.Fail = map[string]vboxmanage.Output{"VBoxManage.exe list --long hdds": {ExitCode: 1, Stderr: "VBoxManage.exe: error: refused\r\n"}}
	if err := model.VBoxHost().Forget(context.Background(), ghost); err == nil || model.VM(ghost) == nil {
		t.Errorf("forget with the disks unread: %v, still registered %v", err, model.VM(ghost) != nil)
	}
}

// How the model answers what a prune reads and removes.
package vboxmanagetest

import (
	"context"
	"reflect"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/vboxmanage"
)

func TestPruneAnswers(t *testing.T) {
	vm := &VM{Name: "web", UUID: "11111111-1111-4111-8111-111111111111", State: vboxmanage.StatePoweroff,
		Folder: `G:\Lab\web`, Slots: []vboxmanage.Slot{{Controller: "SATA", Medium: `G:\Lab\web\web.vdi`}}}
	model := New(vm)
	model.Orphans = []Orphan{
		{UUID: "22222222-2222-4222-8222-222222222222", Location: `G:\Lab\old\old.vdi`},
		{UUID: "33333333-3333-4333-8333-333333333333", Parent: "22222222-2222-4222-8222-222222222222", Location: `G:\Lab\old\Snapshots/{c}.vdi`, Gone: true},
	}
	model.SetFile(`G:\Lab\old\old.vdi`, []byte("disk"))
	model.SetFile(`G:\Lab\files\note.txt`, []byte("x"))
	model.SetFile(`D:\Else\x\y.txt`, []byte("x"))
	h := model.VBoxHost()
	ctx := context.Background()

	if folder, err := h.MachineFolder(ctx); err != nil || folder != defaultMachineFolder {
		t.Errorf("machine folder %q, %v", folder, err)
	}
	model.SetFile(`G:\Lab\old\old.vbox-prev`, []byte("<VirtualBox/>"))
	// files holds no settings file, so it is no machine folder.
	folders, err := h.MachineFolders(ctx, `G:\Lab`)
	if want := []string{`G:\Lab\old`, `G:\Lab\web`}; err != nil || !reflect.DeepEqual(folders, want) {
		t.Errorf("folders %v, %v, want %v", folders, err, want)
	}
	media, err := h.Media(ctx)
	if err != nil {
		t.Fatal(err)
	}
	states := map[string]string{}
	for _, m := range media {
		states[m.UUID] = m.State
	}
	if states["33333333-3333-4333-8333-333333333333"] != vboxmanage.StateInaccessible || states["22222222-2222-4222-8222-222222222222"] != "created" {
		t.Errorf("states %v", states)
	}
	if err := h.CloseDisk(ctx, "22222222-2222-4222-8222-222222222222", true); err == nil {
		t.Error("a disk with a child was forgotten")
	}
	if err := h.CloseDisk(ctx, "33333333-3333-4333-8333-333333333333", false); err != nil {
		t.Fatal(err)
	}
	if err := h.CloseDisk(ctx, "22222222-2222-4222-8222-222222222222", true); err != nil || model.Files[`G:\Lab\old\old.vdi`] != nil || len(model.Orphans) != 0 {
		t.Errorf("forgetting the parent: %v, orphans %v", err, model.Orphans)
	}
	if err := h.RemoveFolder(ctx, `G:\Lab\files`); err != nil || model.Files[`G:\Lab\files\note.txt`] != nil || model.Files[`D:\Else\x\y.txt`] == nil {
		t.Errorf("removing a folder: %v, files %v", err, model.Files)
	}
	if folders, err := h.MachineFolders(ctx, `Z:\Nothing`); err != nil || folders != nil {
		t.Errorf("an empty root: %v, %v", folders, err)
	}
}

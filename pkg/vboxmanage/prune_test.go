// PlanPrune and the prune host calls, against hand-built input and the
// model host.
package vboxmanage_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/vboxmanage"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/vboxmanage/vboxmanagetest"
)

const pruneRoot = `G:\Lab`

// pruneMedia is a host's disks: a VM in use, a leftover chain from a clone
// whose base was forgotten, a disk a VM detached and kept in its folder,
// an unused disk with a child still in use, and one outside the root.
func pruneMedia() []vboxmanage.Medium {
	use := func(name string) []vboxmanage.MediumUser {
		return []vboxmanage.MediumUser{{Name: name, UUID: "11111111-1111-4111-8111-111111111111"}}
	}
	return []vboxmanage.Medium{
		{UUID: "a", Location: `G:\Lab\web\web.vdi`, Users: use("web")},
		{UUID: "b", Location: `G:\Lab\gone-base\gone-base.vdi`, State: vboxmanage.StateInaccessible},
		{UUID: "c", Parent: "b", Location: `G:\Lab\gone\Snapshots\{c}.vdi`},
		{UUID: "d", Parent: "c", Location: `G:\Lab\gone\Snapshots\{d}.vdi`},
		{UUID: "e", Location: `G:\Lab\web\kept-data.vdi`},
		{UUID: "f", Location: `G:\Lab\shared\base.vdi`},
		{UUID: "g", Parent: "f", Location: `G:\Lab\clone\{g}.vdi`, Users: use("clone")},
		{UUID: "h", Location: `D:\elsewhere\loose.vdi`},
		{UUID: "i", Location: `G:\Lab\disks\data.vdi`, Users: use("web")},
	}
}

func TestPlanPrune(t *testing.T) {
	in := vboxmanage.PruneInput{
		Root:    pruneRoot,
		Media:   pruneMedia(),
		Configs: []string{`G:\Lab\web\web.vbox`, `G:\Lab\clone\clone.vbox`},
		Folders: []string{`G:\Lab\web`, `G:\Lab\gone`, `G:\Lab\gone-base`, `G:\Lab\shared`, `G:\Lab\clone`, `G:\Lab\empty`},
	}
	got := vboxmanage.PlanPrune(in)
	want := vboxmanage.PrunePlan{
		// The chain goes newest first; the base whose file is gone is only
		// forgotten.
		Disks: []vboxmanage.PruneDisk{
			{UUID: "d", Location: `G:\Lab\gone\Snapshots\{d}.vdi`},
			{UUID: "c", Location: `G:\Lab\gone\Snapshots\{c}.vdi`},
			{UUID: "b", Location: `G:\Lab\gone-base\gone-base.vdi`, Gone: true},
		},
		// shared holds a disk a clone in use descends from; web and clone
		// hold registered VMs' settings.
		Folders: []string{`G:\Lab\empty`, `G:\Lab\gone`, `G:\Lab\gone-base`},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("plan\n%+v\nwant\n%+v", got, want)
	}
	// A VM VirtualBox cannot read could own any folder: none is removed,
	// though the disks still go.
	in.Unreadable = true
	if got := vboxmanage.PlanPrune(in); len(got.Folders) != 0 || len(got.Disks) != 3 {
		t.Errorf("with an unreadable VM: %+v", got)
	}
	// The root itself is never a folder to remove, nor a folder outside it.
	in = vboxmanage.PruneInput{Root: pruneRoot, Folders: []string{pruneRoot, `D:\other`}}
	if got := vboxmanage.PlanPrune(in); len(got.Folders) != 0 {
		t.Errorf("root or outside: %+v", got.Folders)
	}
	// A parent loop, which VirtualBox never writes, ends and removes nothing.
	in = vboxmanage.PruneInput{Root: pruneRoot, Media: []vboxmanage.Medium{
		{UUID: "x", Parent: "y", Location: `G:\Lab\l\x.vdi`}, {UUID: "y", Parent: "x", Location: `G:\Lab\l\y.vdi`}}}
	if got := vboxmanage.PlanPrune(in); len(got.Disks) != 0 {
		t.Errorf("a loop: %+v", got.Disks)
	}
}

func TestPruneHostCalls(t *testing.T) {
	model := vboxmanagetest.New()
	model.Orphans = []vboxmanagetest.Orphan{
		{UUID: "22222222-2222-4222-8222-222222222222", Location: `G:\Lab\gone\{a}.vdi`},
		{UUID: "33333333-3333-4333-8333-333333333333", Parent: "22222222-2222-4222-8222-222222222222", Location: `G:\Lab\gone\{b}.vdi`, Gone: true},
	}
	model.SetFile(`G:\Lab\gone\{a}.vdi`, []byte("disk"))
	model.SetFile(`G:\Lab\gone\gone.vbox`, []byte("<VirtualBox/>"))
	h := model.VBoxHost()
	ctx := context.Background()
	if folder, err := h.MachineFolder(ctx); err != nil || folder != `C:\Users\pleiades-gate\VirtualBox VMs` {
		t.Errorf("machine folder %q, %v", folder, err)
	}
	if folders, err := h.MachineFolders(ctx, pruneRoot); err != nil || !reflect.DeepEqual(folders, []string{`G:\Lab\gone`}) {
		t.Errorf("folders %v, %v", folders, err)
	}
	if folders, err := h.MachineFolders(ctx, `G:\Nothing`); err != nil || folders != nil {
		t.Errorf("a missing root: %v, %v", folders, err)
	}
	if err := h.CloseDisk(ctx, "22222222-2222-4222-8222-222222222222", true); err == nil {
		t.Error("a disk with a child was closed")
	}
	if err := h.CloseDisk(ctx, "33333333-3333-4333-8333-333333333333", false); err != nil {
		t.Fatal(err)
	}
	if err := h.CloseDisk(ctx, "22222222-2222-4222-8222-222222222222", true); err != nil || model.Files[`G:\Lab\gone\{a}.vdi`] != nil {
		t.Errorf("closing the parent: %v", err)
	}
	if err := h.CloseDisk(ctx, "not-a-uuid", false); err == nil {
		t.Error("a disk named by no UUID was closed")
	}
	if err := h.RemoveFolder(ctx, `G:\Lab\gone`); err != nil || model.Files[`G:\Lab\gone\gone.vbox`] != nil {
		t.Errorf("removing the folder: %v", err)
	}
	for _, bad := range []string{"", `G:\Lab\it's`} {
		if _, err := h.MachineFolders(ctx, bad); err == nil {
			t.Errorf("folders of %q", bad)
		}
		if err := h.RemoveFolder(ctx, bad); err == nil {
			t.Errorf("removing %q", bad)
		}
	}
}

func TestPruneHostCallsFailing(t *testing.T) {
	model := vboxmanagetest.New()
	model.Fail = map[string]vboxmanage.Output{
		"VBoxManage.exe list systemproperties": {ExitCode: 1, Stderr: "VBoxManage.exe: error: refused\r\n"},
		"powershell folders":                   {ExitCode: 1, Stderr: "Access is denied.\r\n"},
		"powershell remove folder":             {ExitCode: 1, Stderr: "Access is denied.\r\n"},
	}
	h := model.VBoxHost()
	ctx := context.Background()
	if _, err := h.MachineFolder(ctx); err == nil {
		t.Error("a refused systemproperties named a folder")
	}
	if _, err := h.MachineFolders(ctx, pruneRoot); err == nil {
		t.Error("a refused listing listed")
	}
	if err := h.RemoveFolder(ctx, `G:\Lab\x`); err == nil {
		t.Error("a refused removal removed")
	}
	model.Fail = map[string]vboxmanage.Output{"VBoxManage.exe list systemproperties": {Stdout: "API version: 7_2\r\n"}}
	if _, err := h.MachineFolder(ctx); err == nil {
		t.Error("an answer naming no folder named one")
	}
}

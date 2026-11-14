// PlanPrune and the prune host calls, against hand-built input and the
// model host.
package vboxmanage_test

import (
	"context"
	"reflect"
	"strings"
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

// TestPlanPrune_NothingOutsideTheRootByName: every way a disk's path can
// begin with the machine folder's text and not be inside it is planned for
// nothing: a ".." segment between either separator, a segment of only dots
// and spaces, a doubled separator, a UNC or \\?\ path (even one naming the
// same folder through a share), another drive, a stream, a relative path,
// and a name differing only outside ASCII. Before 2026-11-13 the paths with
// ".." and the Kelvin sign were planned, since within compared text after
// strings.ToLower, which folds U+212A to k.
func TestPlanPrune_NothingOutsideTheRootByName(t *testing.T) {
	plan := func(root, location string) vboxmanage.PrunePlan {
		return vboxmanage.PlanPrune(vboxmanage.PruneInput{Root: root, Media: []vboxmanage.Medium{{UUID: "z", Location: location}}})
	}
	// Controls: a disk inside the root, in any ASCII case or separator, goes.
	for _, inside := range []string{`G:\Lab\x.vdi`, `g:\LAB\old\x.vdi`, `G:/Lab/old/x.vdi`, `G:\Lab\old.\x.vdi`} {
		if got := plan(pruneRoot, inside); len(got.Disks) != 1 {
			t.Errorf("%q, inside %s, is not planned: %+v", inside, pruneRoot, got)
		}
	}
	for _, outside := range []string{
		`G:\Lab\..\Windows\x.vdi`, `G:\Lab\gone\..\..\Windows\x.vdi`, `G:/Lab/../Windows/x.vdi`, `G:\Lab/..\Windows\x.vdi`,
		`G:\Lab\.\x.vdi`, `G:\Lab\...\x.vdi`, `G:\Lab\.. \x.vdi`, `G:\Lab\\x.vdi`,
		`\\server\share\Lab\x.vdi`, `\\?\G:\Lab\x.vdi`, `\\localhost\G$\Lab\x.vdi`, `C:\Lab\x.vdi`,
		`G:\Lab\x.vdi:hidden`, `Lab\x.vdi`, `G:Lab\x.vdi`, `G:\Lab`, "",
	} {
		if got := plan(pruneRoot, outside); len(got.Disks) != 0 {
			t.Errorf("%q is planned for deletion under %s: %+v", outside, pruneRoot, got.Disks)
		}
	}
	if got := plan(`G:\Kits`, "G:\\\u212Aits\\x.vdi"); len(got.Disks) != 0 {
		t.Errorf("a disk under the Kelvin sign's folder is planned under G:\\Kits: %+v", got.Disks)
	}
	if got := plan(`G:\Lab\..\Windows`, `G:\Windows\x.vdi`); len(got.Disks) != 0 {
		t.Errorf("a root holding \"..\" planned %+v", got.Disks)
	}
	// Folders the same way: none but the one inside is planned.
	got := vboxmanage.PlanPrune(vboxmanage.PruneInput{Root: pruneRoot, Folders: []string{
		`G:\Lab\..`, `G:\Lab/..`, `G:\Lab\..\Windows`, `G:\Lab\.`, `\\localhost\G$\Lab\old`, `G:\Lab\old`,
	}})
	if !reflect.DeepEqual(got.Folders, []string{`G:\Lab\old`}) {
		t.Errorf("folders %q, want only G:\\Lab\\old", got.Folders)
	}
	if !vboxmanage.Within(`G:\Lab`, `g:/lab/old`) || vboxmanage.Within(`G:\Lab`, `G:\Lab2\x`) || vboxmanage.Within(`G:\Lab`, `G:\Lab\..\x`) {
		t.Error("Within does not compare as PlanPrune does")
	}
}

// TestPlanPrune_Links: a disk at or under a link stays, with every disk it
// descends from, and a folder that is a link or holds one is kept; a path
// that keeps what is under it but cannot be placed (a VM's settings named
// through a share) keeps everything.
func TestPlanPrune_Links(t *testing.T) {
	in := vboxmanage.PruneInput{
		Root: pruneRoot,
		Media: []vboxmanage.Medium{
			{UUID: "j", Location: `G:\Lab\junction\x.vdi`},
			{UUID: "l", Location: `G:\Lab\plain\link.vdi`},
			{UUID: "p", Location: `G:\Lab\chain\base.vdi`},
			{UUID: "c", Parent: "p", Location: `G:\Lab\deep\j\child.vdi`},
			{UUID: "ok", Location: `G:\Lab\old\x.vdi`},
		},
		Folders: []string{`G:\Lab\chain`, `G:\Lab\deep`, `G:\Lab\junction`, `G:\Lab\old`, `G:\Lab\plain`},
		Links:   []string{`G:\Lab\junction`, `G:\Lab\plain\link.vdi`, `G:\Lab\deep\j`},
	}
	want := vboxmanage.PrunePlan{Disks: []vboxmanage.PruneDisk{{UUID: "ok", Location: `G:\Lab\old\x.vdi`}}, Folders: []string{`G:\Lab\old`}}
	if got := vboxmanage.PlanPrune(in); !reflect.DeepEqual(got, want) {
		t.Errorf("plan\n%+v\nwant\n%+v", got, want)
	}
	in.Configs = []string{`\\localhost\G$\Lab\old\old.vbox`}
	if got := vboxmanage.PlanPrune(in); len(got.Disks) != 0 || len(got.Folders) != 0 {
		t.Errorf("a VM's settings nobody can place kept nothing: %+v", got)
	}
}

// TestPruneHostCalls_Links: Links lists the model's links under a root and
// nothing outside it, and RemoveFolder refuses a folder holding one,
// removing nothing in it.
func TestPruneHostCalls_Links(t *testing.T) {
	model := vboxmanagetest.New()
	model.Links = []string{`G:\Lab\gone\Snapshots`, `G:\Other\j`}
	model.SetFile(`G:\Lab\gone\gone.vbox`, []byte("<VirtualBox/>"))
	h := model.VBoxHost()
	ctx := context.Background()
	if links, err := h.Links(ctx, pruneRoot); err != nil || !reflect.DeepEqual(links, []string{`G:\Lab\gone\Snapshots`}) {
		t.Errorf("links %v, %v", links, err)
	}
	if links, err := h.Links(ctx, `G:\Empty`); err != nil || links != nil {
		t.Errorf("a root with none: %v, %v", links, err)
	}
	if err := h.RemoveFolder(ctx, `G:\Lab\gone`); err == nil || !strings.Contains(err.Error(), "link") || model.Files[`G:\Lab\gone\gone.vbox`] == nil {
		t.Errorf("a folder holding a link: %v", err)
	}
	model.Fail = map[string]vboxmanage.Output{"powershell links": {ExitCode: 1, Stderr: "Access is denied.\r\n"}}
	if _, err := h.Links(ctx, pruneRoot); err == nil {
		t.Error("a refused walk listed links")
	}
	model.Fail = map[string]vboxmanage.Output{"powershell links": {ExitCode: 3}}
	if links, err := h.Links(ctx, pruneRoot); err != nil || links != nil {
		t.Errorf("a root that is not there: %v, %v", links, err)
	}
}

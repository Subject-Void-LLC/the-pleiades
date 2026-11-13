// Tests for virt.vbox.media.prune against the model host: a check lists
// exactly what a run removes and changes nothing, a run removes only that,
// and every read or removal failing fails the task.
package media

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/vboxmanage"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/vboxmanage/vboxmanagetest"
)

// recorder is a RunbookContext keeping each stat a method sets.
type recorder struct{ stats map[string]any }

func (r *recorder) InjectSecrets() map[string]string { return nil }

func (r *recorder) SetStat(key string, value any) error {
	if r.stats == nil {
		r.stats = map[string]any{}
	}
	r.stats[key] = value
	return nil
}

func (r *recorder) EmitFact(key string, value any) error { return r.SetStat(key, value) }

// vboxHost is a VirtualBox host whose record names a machine folder.
type vboxHost struct {
	inventorytest.Stub
	folder string
}

func (h *vboxHost) VBoxManagePath() string { return vboxmanagetest.Path }
func (h *vboxHost) VMFolder() string       { return h.folder }

// onModel makes the method reach model.
func onModel(t *testing.T, model *vboxmanagetest.Host) {
	t.Helper()
	old := hostFunc
	hostFunc = func(sdk.RunbookContext, inventory.InventoryItem) (vboxmanage.Host, error) {
		return model.VBoxHost(), nil
	}
	t.Cleanup(func() { hostFunc = old })
}

const root = `G:\Lab`

// leftovers is a host holding one registered VM, and what a failed delete
// left: a disk chain no VM uses, one of its files already gone, and the
// two folders holding them.
func leftovers() *vboxmanagetest.Host {
	web := &vboxmanagetest.VM{Name: "web", UUID: "11111111-1111-4111-8111-111111111111", State: vboxmanage.StatePoweroff,
		Folder: root + `\web`, Slots: []vboxmanage.Slot{{Controller: "SATA", Medium: root + `\web\web.vdi`}}}
	model := vboxmanagetest.New(web)
	model.Orphans = []vboxmanagetest.Orphan{
		{UUID: "22222222-2222-4222-8222-222222222222", Location: root + `\gone-base\gone-base.vdi`},
		{UUID: "33333333-3333-4333-8333-333333333333", Parent: "22222222-2222-4222-8222-222222222222", Location: root + `\gone\Snapshots\{c}.vdi`, Gone: true},
	}
	model.SetFile(root+`\gone-base\gone-base.vdi`, []byte("disk"))
	model.SetFile(root+`\gone-base\gone-base.vbox-prev`, []byte("<VirtualBox/>"))
	// A folder of something else under the machine folder: never touched.
	model.SetFile(root+`\notes\todo.txt`, []byte("keep"))
	model.SetFile(root+`\gone\gone.vbox`, []byte("<VirtualBox/>"))
	model.SetFile(root+`\web\web.vbox`, []byte("<VirtualBox/>"))
	return model
}

func host() *vboxHost {
	return &vboxHost{Stub: inventorytest.Stub{StubName: "vengeance", Caps: []capability.Name{capability.NameVirtualBox}}, folder: root}
}

// changes are the calls that are not reads.
func changes(model *vboxmanagetest.Host) []string {
	var out []string
	for _, c := range model.Calls() {
		if strings.HasPrefix(c, "VBoxManage.exe closemedium") || strings.HasPrefix(c, "powershell remove") {
			out = append(out, c)
		}
	}
	return out
}

func call(t *testing.T, check bool, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	t.Helper()
	d, ok := collection.Lookup("virt.vbox.media.prune")
	if !ok {
		t.Fatal("virt.vbox.media.prune is not registered")
	}
	if check {
		return d.Check(context.Background(), rc, device, params)
	}
	return d.Invoke(context.Background(), rc, device, params)
}

func TestPrune_Registered(t *testing.T) {
	d, ok := collection.Lookup("virt.vbox.media.prune")
	if !ok || d.Manifest.Status != collection.StatusImplemented || !d.Manifest.SupportsCheck || d.Check == nil ||
		d.Manifest.Reversibility.Reversible || d.Manifest.Reversibility.Notes == "" {
		t.Fatalf("%+v", d.Manifest)
	}
}

// TestPrune_CheckThenRun: the check lists the chain, newest first, and the
// two leftover folders, and changes nothing; a run removes exactly those,
// deleting the file of the disk that has one and only forgetting the one
// whose file is gone; the registered VM's disk and folder stay; and a
// second run finds nothing.
func TestPrune_CheckThenRun(t *testing.T) {
	model := leftovers()
	onModel(t, model)
	check := &recorder{}
	if result, err := call(t, true, check, host(), nil); err != nil || !result.Changed || len(changes(model)) != 0 {
		t.Fatalf("check: %+v, %v, %v", result, err, changes(model))
	}
	wantDisks := []any{
		map[string]any{"uuid": "33333333-3333-4333-8333-333333333333", "location": root + `\gone\Snapshots\{c}.vdi`, "gone": true},
		map[string]any{"uuid": "22222222-2222-4222-8222-222222222222", "location": root + `\gone-base\gone-base.vdi`, "gone": false},
	}
	wantFolders := []any{root + `\gone`, root + `\gone-base`}
	if !reflect.DeepEqual(check.stats["disks"], wantDisks) || !reflect.DeepEqual(check.stats["folders"], wantFolders) {
		t.Fatalf("check listed %v and %v", check.stats["disks"], check.stats["folders"])
	}
	run := &recorder{}
	if result, err := call(t, false, run, host(), nil); err != nil || !result.Changed {
		t.Fatalf("run: %+v, %v", result, err)
	}
	if !reflect.DeepEqual(run.stats["disks"], check.stats["disks"]) || !reflect.DeepEqual(run.stats["folders"], check.stats["folders"]) {
		t.Errorf("the run removed %v and %v, not what the check listed", run.stats["disks"], run.stats["folders"])
	}
	want := []string{
		"VBoxManage.exe closemedium disk 33333333-3333-4333-8333-333333333333",
		"VBoxManage.exe closemedium disk 22222222-2222-4222-8222-222222222222 --delete",
		`powershell remove folder G:\Lab\gone`,
		`powershell remove folder G:\Lab\gone-base`,
	}
	if got := changes(model); !reflect.DeepEqual(got, want) {
		t.Errorf("changes\n%q\nwant\n%q", got, want)
	}
	if len(model.Orphans) != 0 || model.Files[root+`\web\web.vbox`] == nil || model.Files[root+`\gone\gone.vbox`] != nil || model.Files[root+`\notes\todo.txt`] == nil {
		t.Errorf("orphans %v, files %v", model.Orphans, model.Files)
	}
	again := &recorder{}
	if result, err := call(t, false, again, host(), nil); err != nil || result.Changed {
		t.Errorf("a second run: %+v, %v", result, err)
	}
}

// TestPrune_Refusals: parameters are refused, since a check is the list;
// a VM VirtualBox cannot read keeps every folder; and a host whose record
// names no folder is pruned under VirtualBox's own default.
func TestPrune_Refusals(t *testing.T) {
	model := leftovers()
	onModel(t, model)
	if _, err := call(t, false, &recorder{}, host(), map[string]any{"dry_run": true}); err == nil || !strings.Contains(err.Error(), "takes no parameters") {
		t.Errorf("a parameter: %v", err)
	}
	unreadable := &vboxmanagetest.VM{Name: "lost", UUID: "44444444-4444-4444-8444-444444444444", State: vboxmanage.StatePoweroff, Inaccessible: true}
	model = vboxmanagetest.New(unreadable)
	model.SetFile(root+`\gone\gone.vbox`, []byte("<VirtualBox/>"))
	onModel(t, model)
	rc := &recorder{}
	if _, err := call(t, false, rc, host(), nil); err != nil {
		t.Fatal(err)
	}
	if folders, _ := rc.stats["folders"].([]any); len(folders) != 0 || model.Files[root+`\gone\gone.vbox`] == nil {
		t.Errorf("a folder went while a VM was unreadable: %v", rc.stats["folders"])
	}
	for _, drive := range []string{`G:\`, `G:`} {
		onModel(t, leftovers())
		if _, err := call(t, false, &recorder{}, &vboxHost{Stub: host().Stub, folder: drive}, nil); err == nil || !strings.Contains(err.Error(), "drive's root") {
			t.Errorf("a machine folder of %q: %v", drive, err)
		}
	}
	model = vboxmanagetest.New()
	model.SetFile(`C:\Users\pleiades-gate\VirtualBox VMs\old\old.vbox`, []byte("x"))
	model.SetFile(root+`\keep\keep.vbox`, []byte("x"))
	onModel(t, model)
	plain := &inventorytest.Stub{StubName: "vengeance", Caps: []capability.Name{capability.NameVirtualBox}}
	if _, err := call(t, false, &recorder{}, plain, nil); err != nil {
		t.Fatal(err)
	}
	if model.Files[`C:\Users\pleiades-gate\VirtualBox VMs\old\old.vbox`] != nil || model.Files[root+`\keep\keep.vbox`] == nil {
		t.Errorf("pruned the wrong folder: %v", model.Files)
	}
}

// TestPrune_StepsFailing: every read and every removal failing fails the
// task, naming what failed.
func TestPrune_StepsFailing(t *testing.T) {
	refused := vboxmanage.Output{ExitCode: 1, Stderr: "VBoxManage.exe: error: refused\r\n"}
	for _, fail := range []string{
		"VBoxManage.exe list --long hdds",
		"VBoxManage.exe list vms",
		"VBoxManage.exe showvminfo",
		"powershell folders",
		"VBoxManage.exe closemedium disk 33333333",
		"powershell remove folder",
	} {
		model := leftovers()
		model.Fail = map[string]vboxmanage.Output{fail: refused}
		onModel(t, model)
		if _, err := call(t, false, &recorder{}, host(), nil); err == nil {
			t.Errorf("%s failing: the prune succeeded", fail)
		}
	}
	model := leftovers()
	model.Fail = map[string]vboxmanage.Output{"VBoxManage.exe list systemproperties": refused}
	onModel(t, model)
	plain := &inventorytest.Stub{StubName: "vengeance", Caps: []capability.Name{capability.NameVirtualBox}}
	if _, err := call(t, false, &recorder{}, plain, nil); err == nil {
		t.Error("an unread machine folder: the prune succeeded")
	}
	old := hostFunc
	hostFunc = func(sdk.RunbookContext, inventory.InventoryItem) (vboxmanage.Host, error) {
		return vboxmanage.Host{}, fmt.Errorf("no way there")
	}
	defer func() { hostFunc = old }()
	if _, err := call(t, false, &recorder{}, host(), nil); err == nil || !strings.Contains(err.Error(), "no way there") {
		t.Errorf("an unreachable host: %v", err)
	}
}

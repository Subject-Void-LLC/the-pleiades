// Tests for how the methods that make and remove VMs fail at each step,
// and for the host's own VM folder, against the model host.
package vm

import (
	"context"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/vboxmanage"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/vboxmanage/vboxmanagetest"
)

// failure is a VBoxManage refusal.
var failure = vboxmanage.Output{ExitCode: 1, Stderr: "VBoxManage.exe: error: refused\r\n"}

func TestClone_EachStepFailing(t *testing.T) {
	for step, want := range map[string]string{
		"VBoxManage.exe clonevm":       "refused",
		"VBoxManage.exe modifyvm":      "was deleted",
		"VBoxManage.exe storageattach": "was deleted",
	} {
		model := vboxmanagetest.New(base())
		model.Fail = map[string]vboxmanage.Output{step: failure}
		onModel(t, model)
		if _, err := call(t, "virt.vbox.vm.clone", false, seeded(), cloneParams()); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s failing: %v, want one mentioning %q", step, err, want)
		}
		if model.VM("ubuntu-lab") != nil {
			t.Errorf("%s failing left the VM", step)
		}
	}
	noIDE := base()
	noIDE.Slots = noIDE.Slots[2:]
	model := vboxmanagetest.New(noIDE)
	onModel(t, model)
	if _, err := call(t, "virt.vbox.vm.clone", false, seeded(), cloneParams()); err == nil || !strings.Contains(err.Error(), "no free IDE slot") {
		t.Errorf("a base with no IDE slot: %v", err)
	}
	model = vboxmanagetest.New(base())
	model.Fail = map[string]vboxmanage.Output{"VBoxManage.exe showvminfo": failure}
	onModel(t, model)
	for _, check := range []bool{false, true} {
		if _, err := call(t, "virt.vbox.vm.clone", check, seeded(), cloneParams()); err == nil {
			t.Errorf("check %v: a host that cannot be read", check)
		}
	}
	for _, stat := range []string{"address", "uuid", sdk.StatDiff, sdk.StatInverse} {
		onModel(t, vboxmanagetest.New(base()))
		rc := seeded()
		rc.failOn = stat
		if _, err := call(t, "virt.vbox.vm.clone", false, rc, cloneParams()); err == nil {
			t.Errorf("recording %s failed and the clone did not", stat)
		}
	}
	existing := vboxmanagetest.New(base(), booting(""))
	onModel(t, existing)
	rc := seeded()
	rc.failOn = "uuid"
	if _, err := call(t, "virt.vbox.vm.clone", false, rc, cloneParams()); err == nil {
		t.Error("recording an existing VM's uuid failed and the clone did not")
	}
	twice := base()
	twice.Snapshots = append(twice.Snapshots, vboxmanagetest.Snapshot{Name: "base", UUID: "00000000-0000-4000-8000-000000000077", Parent: baseSnap})
	onModel(t, vboxmanagetest.New(twice))
	if _, err := call(t, "virt.vbox.vm.clone", false, seeded(), cloneParams()); err == nil || !strings.Contains(err.Error(), "has 2 snapshots") {
		t.Errorf("a name two snapshots share: %v", err)
	}
	badFolder := base()
	badFolder.Folder = `G:\Pleiades*Lab`
	onModel(t, vboxmanagetest.New(badFolder))
	if err := deleteVM(context.Background(), vboxmanagetest.New(badFolder).VBoxHost(), "ubuntu-2404-base"); err == nil {
		t.Error("a VM folder no path can name was used")
	}
}

// vboxHost is a VirtualBox host whose record names the folder VMs go in.
type vboxHost struct{ *inventorytest.Stub }

func (vboxHost) VBoxManagePath() string { return vboxmanagetest.Path }
func (vboxHost) VMFolder() string       { return `G:\PleiadesLab` }

func TestClone_IntoTheHostsVMFolder(t *testing.T) {
	model := vboxmanagetest.New(base())
	onModel(t, model)
	d, _ := collection.Lookup("virt.vbox.vm.clone")
	device := vboxHost{&inventorytest.Stub{StubName: "vengeance", Caps: []capability.Name{capability.NameVirtualBox}}}
	if _, err := d.Invoke(context.Background(), seeded(), device, cloneParams()); err != nil {
		t.Fatal(err)
	}
	if vm := model.VM("ubuntu-lab"); vm.Folder != `G:\PleiadesLab\ubuntu-lab` {
		t.Errorf("folder %q", vm.Folder)
	}
	if vmFolder(host) != "" {
		t.Error("a host naming no folder gave one")
	}
}

func TestDelete_EachStepFailing(t *testing.T) {
	for step, want := range map[string]string{
		"VBoxManage.exe storageattach": "refused",
		"VBoxManage.exe closemedium":   "refused",
		"powershell remove":            "removing",
	} {
		model := vboxmanagetest.New(base())
		onModel(t, model)
		if _, err := call(t, "virt.vbox.vm.clone", false, seeded(), cloneParams()); err != nil {
			t.Fatal(err)
		}
		model.Fail = map[string]vboxmanage.Output{step: failure}
		if _, err := call(t, "virt.vbox.vm.delete", false, newRecorder(), map[string]any{"name": "ubuntu-lab"}); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s failing: %v, want one mentioning %q", step, err, want)
		}
	}
	model := vboxmanagetest.New(base())
	model.Fail = map[string]vboxmanage.Output{"VBoxManage.exe showvminfo": failure}
	onModel(t, model)
	if _, err := call(t, "virt.vbox.vm.delete", false, newRecorder(), map[string]any{"name": "ubuntu-2404-base"}); err == nil {
		t.Error("a VM that cannot be read was deleted")
	}
	if err := deleteVM(context.Background(), model.VBoxHost(), "ubuntu-2404-base"); err == nil {
		t.Error("deleteVM read a VM that cannot be read")
	}
}

func TestImportOva_Failures(t *testing.T) {
	model := vboxmanagetest.New()
	model.Fail = map[string]vboxmanage.Output{"VBoxManage.exe showvminfo": failure}
	onModel(t, model)
	if _, err := call(t, "virt.vbox.vm.import_ova", false, newRecorder(), map[string]any{"name": "b", "path": ova}); err == nil {
		t.Error("a host that cannot be read")
	}
	onModel(t, vboxmanagetest.New(base()))
	rc := newRecorder()
	rc.failOn = "uuid"
	if _, err := call(t, "virt.vbox.vm.import_ova", false, rc, map[string]any{"name": "ubuntu-2404-base", "path": ova}); err == nil {
		t.Error("recording an existing VM's uuid failed and the import did not")
	}
}

func TestHostKeys_ABlockThatDoesNotParse(t *testing.T) {
	log := `G:\PleiadesLab\ubuntu-lab\console.log`
	model := vboxmanagetest.New(booting(log))
	model.SetFile(log, []byte("-----BEGIN SSH HOST KEY KEYS-----\nssh-ed25519 AAAAbroken\n-----END SSH HOST KEY KEYS-----\n"))
	onModel(t, model)
	if _, err := call(t, "virt.vbox.vm.host_keys", false, newRecorder(), map[string]any{"name": "ubuntu-lab"}); err == nil || !strings.Contains(err.Error(), "does not parse") {
		t.Errorf("a broken block: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := waitForKeys(ctx, vboxmanagetest.New(booting(log)).VBoxHost(), "ubuntu-lab", log, false, 1<<30); err == nil {
		t.Error("a cancelled wait kept waiting")
	}
}

// TestLaterReadsFailing covers each read a method makes after it acts:
// the host answered the first reads and then stopped answering.
func TestLaterReadsFailing(t *testing.T) {
	type attempt struct {
		fqcn   string
		vms    func() []*vboxmanagetest.VM
		params map[string]any
		rc     func() *recorder
		read   string
		skip   int
	}
	running := func() []*vboxmanagetest.VM { r := booting(""); r.Name = "ubuntu-lab"; return []*vboxmanagetest.VM{r} }
	for name, a := range map[string]attempt{
		"import, after importing":  {"virt.vbox.vm.import_ova", func() []*vboxmanagetest.VM { return nil }, map[string]any{"name": "b", "path": ova}, newRecorder, "VBoxManage.exe showvminfo b", 1},
		"clone, after cloning":     {"virt.vbox.vm.clone", func() []*vboxmanagetest.VM { return []*vboxmanagetest.VM{base()} }, cloneParams(), seeded, "VBoxManage.exe showvminfo ubuntu-lab", 1},
		"clone, after configuring": {"virt.vbox.vm.clone", func() []*vboxmanagetest.VM { return []*vboxmanagetest.VM{base()} }, cloneParams(), seeded, "VBoxManage.exe showvminfo ubuntu-lab", 2},
		"clone, after attaching":   {"virt.vbox.vm.clone", func() []*vboxmanagetest.VM { return []*vboxmanagetest.VM{base()} }, cloneParams(), seeded, "VBoxManage.exe showvminfo ubuntu-lab", 3},
		"stop, after stopping":     {"virt.vbox.vm.stop", running, map[string]any{"name": "ubuntu-lab", "mode": "poweroff"}, newRecorder, "VBoxManage.exe showvminfo ubuntu-lab", 1},
		"stop, while waiting":      {"virt.vbox.vm.stop", running, map[string]any{"name": "ubuntu-lab"}, newRecorder, "VBoxManage.exe showvminfo ubuntu-lab", 1},
	} {
		model := vboxmanagetest.New(a.vms()...)
		model.Appliances[ova] = appliance()
		model.Fail = map[string]vboxmanage.Output{a.read: failure}
		model.Skip = map[string]int{a.read: a.skip}
		onModel(t, model)
		if _, err := call(t, a.fqcn, false, a.rc(), a.params); err == nil {
			t.Errorf("%s: a read that failed was not reported", name)
		}
	}
	// start, after the autostart service started it.
	stopped := base()
	stopped.Name = "ubuntu-lab"
	model := vboxmanagetest.New(stopped)
	model.Fail = map[string]vboxmanage.Output{"VBoxManage.exe showvminfo ubuntu-lab": failure}
	model.Skip = map[string]int{"VBoxManage.exe showvminfo ubuntu-lab": 2}
	onModel(t, model)
	if _, err := call(t, "virt.vbox.vm.start", false, newRecorder(), map[string]any{"name": "ubuntu-lab"}); err == nil {
		t.Error("start: a read after starting failed and was not reported")
	}
}

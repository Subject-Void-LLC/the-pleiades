// Tests for the methods that make and remove VMs (virt.vbox.vm.import_ova,
// clone and delete) and for reading a new VM's host keys, against the
// model host.
package vm

import (
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/vboxmanage"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/vboxmanage/vboxmanagetest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

const (
	ova         = `G:\PleiadesLab\ubuntu-24.04-server-cloudimg-amd64.ova`
	baseSnap    = "88573fb2-b63c-454f-a963-b0a46faf551f"
	labKey      = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIGRkT5Qe8cr6G8m3J1W0d8o6xrXEm1yH4r2hH0kRk3p4 root@ubuntu-lab"
	labHash     = "$6$saltstring$svn8UoSVapNtMuq1ukKS4tPQd8iKwSMHWjl/O817G3uBnIFNjnQJuesI68u4OTLiBFdcbYEdFCoEOfaS35inz1"
	defaultHome = `C:\Users\pleiades-gate\VirtualBox VMs`
)

// appliance is Ubuntu's cloud image as importing it makes it: a free IDE
// slot, a disk, and the bridged adapter it asks for.
func appliance() *vboxmanagetest.VM {
	return &vboxmanagetest.VM{MemoryMB: 1024, CPUs: 2, NICs: map[int]vboxmanage.NIC{1: {Kind: "bridged"}},
		Slots: []vboxmanage.Slot{
			{Controller: "IDE", ControllerType: "PIIX4", Medium: "none"},
			{Controller: "IDE", ControllerType: "PIIX4", Device: 1, Medium: "none"},
			{Controller: "SCSI", ControllerType: "LsiLogic", Medium: `G:\PleiadesLab\ubuntu-2404-base\disk.vmdk`},
		}}
}

// base is the imported base with its never-booted snapshot.
func base() *vboxmanagetest.VM {
	vm := appliance()
	vm.Name, vm.UUID, vm.State, vm.Folder = "ubuntu-2404-base", "58af4b55-8f5b-4126-b11d-3fff38f2b9ca", vboxmanage.StatePoweroff, `G:\PleiadesLab\ubuntu-2404-base`
	vm.NICs = map[int]vboxmanage.NIC{}
	vm.Snapshots = []vboxmanagetest.Snapshot{{Name: "base", UUID: baseSnap}}
	vm.Current = baseSnap
	return vm
}

// seeded is a recorder holding the login the engine derives for a clone.
func seeded() *recorder {
	r := newRecorder()
	r.secrets = map[string]string{wire.SecretSeedUsername: "root", wire.SecretSeedAuthorizedKey: labKey, wire.SecretSeedPasswordHash: labHash}
	return r
}

func cloneParams() map[string]any {
	return map[string]any{"name": "ubuntu-lab", "from": "ubuntu-2404-base", "snapshot": "base", "login": "ubuntu-lab", "address": "192.168.56.10/24", "memory_mb": 2048, "cpus": 2}
}

func TestImportOva(t *testing.T) {
	model := vboxmanagetest.New()
	model.Appliances[ova] = appliance()
	onModel(t, model)
	params := map[string]any{"name": "ubuntu-2404-base", "path": ova}
	check := newRecorder()
	if result, err := call(t, "virt.vbox.vm.import_ova", true, check, params); err != nil || !result.Changed || len(changes(model)) != 0 {
		t.Fatalf("check: %+v, %v, %v", result, err, changes(model))
	}
	rc := newRecorder()
	result, err := call(t, "virt.vbox.vm.import_ova", false, rc, params)
	if err != nil || !result.Changed {
		t.Fatalf("%+v, %v", result, err)
	}
	vm := model.VM("ubuntu-2404-base")
	if vm == nil || vm.NICs[1].Kind != "none" || rc.stats["uuid"] != vm.UUID {
		t.Fatalf("imported %+v, stats %v", vm, rc.stats)
	}
	want := map[string]any{"fqcn": "virt.vbox.vm.delete", "params": map[string]any{"name": "ubuntu-2404-base"}, "description": "Delete ubuntu-2404-base, which this task imported."}
	if !reflect.DeepEqual(rc.stats[sdk.StatInverse], want) {
		t.Errorf("inverse = %v", rc.stats[sdk.StatInverse])
	}
	again := newRecorder()
	if result, err := call(t, "virt.vbox.vm.import_ova", false, again, params); err != nil || result.Changed || again.stats["uuid"] != vm.UUID {
		t.Errorf("a second import: %+v, %v", result, err)
	}
	for why, p := range map[string]map[string]any{
		"no path":        {"name": "x"},
		"a bad path":     {"name": "x", "path": "image.ova"},
		"a missing file": {"name": "x", "path": `G:\missing.ova`},
	} {
		if _, err := call(t, "virt.vbox.vm.import_ova", false, newRecorder(), p); err == nil {
			t.Errorf("%s: accepted", why)
		}
	}
	for _, stat := range []string{"uuid", sdk.StatDiff, sdk.StatInverse} {
		fresh := vboxmanagetest.New()
		fresh.Appliances[ova] = appliance()
		onModel(t, fresh)
		failing := newRecorder()
		failing.failOn = stat
		if _, err := call(t, "virt.vbox.vm.import_ova", false, failing, params); err == nil {
			t.Errorf("recording %s failed and the import did not", stat)
		}
	}
}

func TestClone(t *testing.T) {
	model := vboxmanagetest.New(base())
	onModel(t, model)
	fixed := time.Date(2026, 9, 27, 15, 0, 0, 0, time.UTC)
	oldNow := now
	now = func() time.Time { return fixed }
	t.Cleanup(func() { now = oldNow })

	check := seeded()
	if result, err := call(t, "virt.vbox.vm.clone", true, check, cloneParams()); err != nil || !result.Changed || len(changes(model)) != 0 {
		t.Fatalf("check: %+v, %v, %v", result, err, changes(model))
	}
	rc := seeded()
	result, err := call(t, "virt.vbox.vm.clone", false, rc, cloneParams())
	if err != nil || !result.Changed {
		t.Fatalf("%+v, %v", result, err)
	}
	vm := model.VM("ubuntu-lab")
	folder := defaultHome + `\ubuntu-lab`
	if vm == nil || vm.LinkedFrom != "ubuntu-2404-base" || vm.MemoryMB != 2048 || vm.CPUs != 2 || vm.ConsoleLog != folder+`\console.log` {
		t.Fatalf("made %+v", vm)
	}
	if vm.NICs[1].Kind != "nat" || vm.NICs[2].Kind != "hostonly" || vm.NICs[2].HostOnlyAdapter != "VirtualBox Host-Only Ethernet Adapter" {
		t.Errorf("NICs %+v", vm.NICs)
	}
	iso := model.Files[folder+`\seed.iso`]
	if vm.Slots[0].Medium != folder+`\seed.iso` || !strings.Contains(string(iso), "CD001") {
		t.Fatalf("seed slot %+v, image of %d bytes", vm.Slots[0], len(iso))
	}
	for _, want := range []string{labKey, labHash, "instance-id: " + vm.UUID, "192.168.56.10/24", strings.ToLower(vm.NICs[2].MAC[:2]) + ":"} {
		if !strings.Contains(string(iso), want) {
			t.Errorf("the seed lacks %q", want)
		}
	}
	if rc.stats["uuid"] != vm.UUID || rc.stats["address"] != "192.168.56.10" {
		t.Errorf("stats %v", rc.stats)
	}
	if inverse := rc.stats[sdk.StatInverse].(map[string]any); inverse["fqcn"] != "virt.vbox.vm.delete" {
		t.Errorf("inverse %v", inverse)
	}
	again := seeded()
	if result, err := call(t, "virt.vbox.vm.clone", false, again, cloneParams()); err != nil || result.Changed {
		t.Errorf("a second clone: %+v, %v", result, err)
	}
}

func TestClone_Refusals(t *testing.T) {
	model := vboxmanagetest.New(base())
	onModel(t, model)
	for why, change := range map[string]func(map[string]any){
		"no from":         func(p map[string]any) { delete(p, "from") },
		"a bad from":      func(p map[string]any) { p["from"] = "a b" },
		"no snapshot":     func(p map[string]any) { delete(p, "snapshot") },
		"a bad snapshot":  func(p map[string]any) { p["snapshot"] = "a/b" },
		"no login":        func(p map[string]any) { delete(p, "login") },
		"no address":      func(p map[string]any) { delete(p, "address") },
		"no prefix":       func(p map[string]any) { p["address"] = "192.168.56.10" },
		"IPv6":            func(p map[string]any) { p["address"] = "fd00::10/64" },
		"a /32":           func(p map[string]any) { p["address"] = "192.168.56.10/32" },
		"a bad host name": func(p map[string]any) { p["hostname"] = "ubuntu_lab" },
		"a bad adapter":   func(p map[string]any) { p["host_only_adapter"] = `a"b` },
		"no memory":       func(p map[string]any) { p["memory_mb"] = 0 },
		"text for CPUs":   func(p map[string]any) { p["cpus"] = "two" },
	} {
		p := cloneParams()
		change(p)
		if _, err := call(t, "virt.vbox.vm.clone", false, seeded(), p); err == nil {
			t.Errorf("%s: accepted", why)
		}
	}
	if _, err := call(t, "virt.vbox.vm.clone", false, newRecorder(), cloneParams()); err == nil || !strings.Contains(err.Error(), "user name") {
		t.Errorf("no seeded login: %v", err)
	}
	if len(changes(model)) != 0 {
		t.Errorf("a refused clone changed the host: %v", changes(model))
	}
	for why, tt := range map[string]struct {
		params map[string]any
		check  bool
		want   string
	}{
		"a check of a base not made yet":      {map[string]any{"from": "later"}, true, "cannot be checked"},
		"a check of a snapshot not taken yet": {map[string]any{"snapshot": "later"}, true, "cannot be checked"},
		"a snapshot that is not there":        {map[string]any{"snapshot": "later"}, false, `has 0 snapshots named "later"`},
	} {
		p := cloneParams()
		for k, v := range tt.params {
			p[k] = v
		}
		_, err := call(t, "virt.vbox.vm.clone", tt.check, seeded(), p)
		if err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: %v, want one mentioning %q", why, err, tt.want)
		}
	}
}

// TestClone_AFailureDeletesWhatWasMade covers a clone that fails after
// VirtualBox made the VM: the VM is deleted, so a later run makes it
// again rather than taking a half-made one for finished.
func TestClone_AFailureDeletesWhatWasMade(t *testing.T) {
	model := vboxmanagetest.New(base())
	model.Fail = map[string]vboxmanage.Output{"powershell upload": {ExitCode: 1, Stderr: "Access to the path is denied.\r\n"}}
	onModel(t, model)
	_, err := call(t, "virt.vbox.vm.clone", false, seeded(), cloneParams())
	if err == nil || !strings.Contains(err.Error(), "was deleted") || model.VM("ubuntu-lab") != nil {
		t.Errorf("err = %v, VM left %v", err, model.VM("ubuntu-lab"))
	}
	model = vboxmanagetest.New(base())
	model.Fail = map[string]vboxmanage.Output{
		"powershell upload":           {ExitCode: 1, Stderr: "Access to the path is denied.\r\n"},
		"VBoxManage.exe unregistervm": {ExitCode: 1, Stderr: "VBoxManage.exe: error: locked\r\n"},
	}
	onModel(t, model)
	if _, err := call(t, "virt.vbox.vm.clone", false, seeded(), cloneParams()); err == nil || !strings.Contains(err.Error(), "failed too") {
		t.Errorf("a cleanup that fails: %v", err)
	}
}

func TestDelete(t *testing.T) {
	model := vboxmanagetest.New(base())
	onModel(t, model)
	if _, err := call(t, "virt.vbox.vm.clone", false, seeded(), cloneParams()); err != nil {
		t.Fatal(err)
	}
	folder := defaultHome + `\ubuntu-lab`
	model.SetFile(folder+`\console.log`, []byte("boot"))
	shared := `G:\iso\tools.iso`
	model.SetFile(shared, []byte("shared"))
	model.VM("ubuntu-lab").Slots[1].Medium = shared

	check := newRecorder()
	if result, err := call(t, "virt.vbox.vm.delete", true, check, map[string]any{"name": "ubuntu-lab"}); err != nil || !result.Changed {
		t.Fatalf("check: %+v, %v", result, err)
	}
	rc := newRecorder()
	result, err := call(t, "virt.vbox.vm.delete", false, rc, map[string]any{"name": "ubuntu-lab"})
	if err != nil || !result.Changed || model.VM("ubuntu-lab") != nil {
		t.Fatalf("%+v, %v", result, err)
	}
	for _, gone := range []string{folder + `\seed.iso`, folder + `\console.log`} {
		if _, ok := model.Files[gone]; ok {
			t.Errorf("%s is left", gone)
		}
	}
	if _, ok := model.Files[shared]; !ok {
		t.Error("a shared ISO was deleted")
	}
	if _, ok := rc.stats[sdk.StatInverse]; ok {
		t.Error("an irreversible delete recorded an inverse")
	}
	if result, err := call(t, "virt.vbox.vm.delete", false, newRecorder(), map[string]any{"name": "ubuntu-lab"}); err != nil || result.Changed {
		t.Errorf("deleting it again: %+v, %v", result, err)
	}
	if _, err := call(t, "virt.vbox.vm.delete", false, newRecorder(), map[string]any{"name": "ubuntu-2404-base"}); err != nil {
		t.Errorf("the base with its clone gone: %v", err)
	}
}

func TestDelete_Refusals(t *testing.T) {
	vm := base()
	vm.State = vboxmanage.StateRunning
	model := vboxmanagetest.New(vm)
	onModel(t, model)
	for _, check := range []bool{false, true} {
		if _, err := call(t, "virt.vbox.vm.delete", check, newRecorder(), map[string]any{"name": "ubuntu-2404-base"}); err == nil || !strings.Contains(err.Error(), "stop it first") {
			t.Errorf("check %v, a running VM: %v", check, err)
		}
	}
	vm.State = vboxmanage.StatePoweroff
	vm.Folder = ""
	if _, err := call(t, "virt.vbox.vm.delete", false, newRecorder(), map[string]any{"name": "ubuntu-2404-base"}); err == nil || !strings.Contains(err.Error(), "no folder") {
		t.Errorf("a VM whose folder is unknown: %v", err)
	}
	vm.Folder = `G:\PleiadesLab\ubuntu-2404-base`
	model.Fail = map[string]vboxmanage.Output{"VBoxManage.exe unregistervm": {ExitCode: 1, Stderr: "VBoxManage.exe: error: Cannot delete\r\n"}}
	if _, err := call(t, "virt.vbox.vm.delete", false, newRecorder(), map[string]any{"name": "ubuntu-2404-base"}); err == nil || !strings.Contains(err.Error(), "Cannot delete") {
		t.Errorf("VirtualBox refusing: %v", err)
	}
	failing := newRecorder()
	failing.failOn = "uuid"
	model.Fail = nil
	if _, err := call(t, "virt.vbox.vm.delete", false, failing, map[string]any{"name": "ubuntu-2404-base"}); err == nil {
		t.Error("recording the uuid failed and the delete did not")
	}
}

// console is the console log a real first boot wrote.
func console(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile("../../../../../pkg/cloudinit/testdata/console-ubuntu-2404.log")
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// booting is a running VM whose console log is at log.
func booting(log string) *vboxmanagetest.VM {
	vm := base()
	vm.Name, vm.State, vm.ConsoleLog = "ubuntu-lab", vboxmanage.StateRunning, log
	return vm
}

func TestHostKeys(t *testing.T) {
	log := `G:\PleiadesLab\ubuntu-lab\console.log`
	model := vboxmanagetest.New(booting(log))
	onModel(t, model)
	check := newRecorder()
	if _, err := call(t, "virt.vbox.vm.host_keys", true, check, map[string]any{"name": "ubuntu-lab"}); err != nil || len(check.stats["ssh_host_keys"].([]string)) != 0 {
		t.Fatalf("a check before the keys: %v, %v", check.stats, err)
	}
	go func() {
		time.Sleep(20 * time.Millisecond)
		model.SetFile(log, console(t))
	}()
	rc := newRecorder()
	if _, err := call(t, "virt.vbox.vm.host_keys", false, rc, map[string]any{"name": "ubuntu-lab", "timeout": 10}); err != nil {
		t.Fatal(err)
	}
	keys := rc.stats["ssh_host_keys"].([]string)
	if len(keys) != 3 || !strings.HasPrefix(keys[1], "ssh-ed25519 ") {
		t.Errorf("keys %q", keys)
	}
	if len(changes(model)) == 0 || strings.Contains(strings.Join(changes(model), " "), "VBoxManage.exe modifyvm") {
		t.Errorf("calls %v", changes(model))
	}
}

func TestHostKeys_Refusals(t *testing.T) {
	log := `G:\PleiadesLab\ubuntu-lab\console.log`
	stopped := booting(log)
	stopped.State = vboxmanage.StatePoweroff
	noLog := booting("")
	for why, tt := range map[string]struct {
		vm     *vboxmanagetest.VM
		params map[string]any
		want   string
	}{
		"a stopped VM with no keys": {stopped, map[string]any{}, "start it"},
		"no console log":            {noLog, map[string]any{}, "writes its serial console to no file"},
		"no keys in time":           {booting(log), map[string]any{"timeout": 1}, "within 1s; it printed nothing"},
		"a zero timeout":            {booting(log), map[string]any{"timeout": 0}, "positive"},
		"a text timeout":            {booting(log), map[string]any{"timeout": "soon"}, "timeout"},
	} {
		onModel(t, vboxmanagetest.New(tt.vm))
		p := map[string]any{"name": "ubuntu-lab"}
		for k, v := range tt.params {
			p[k] = v
		}
		if _, err := call(t, "virt.vbox.vm.host_keys", false, newRecorder(), p); err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: %v, want one mentioning %q", why, err, tt.want)
		}
	}
	hung := vboxmanagetest.New(booting(log))
	hung.SetFile(log, []byte("[    1.5] raid6: avx2x4   gen() 41843 MB/s\r\n[    1.6] raid6: \x1b[31mavx2x2\r\n\r\n"))
	onModel(t, hung)
	if _, err := call(t, "virt.vbox.vm.host_keys", false, newRecorder(), map[string]any{"name": "ubuntu-lab", "timeout": 1}); err == nil ||
		!strings.Contains(err.Error(), `the last line it printed was "[    1.6] raid6: \x1b[31mavx2x2", and its log is`) {
		t.Errorf("a boot that stopped: %v", err)
	}
	hung.SetFile(log, []byte(strings.Repeat("x", 200)))
	if _, err := call(t, "virt.vbox.vm.host_keys", false, newRecorder(), map[string]any{"name": "ubuntu-lab", "timeout": 1}); err == nil ||
		!strings.Contains(err.Error(), `"`+strings.Repeat("x", lastLineMax)+`..."`) {
		t.Errorf("a long last line: %v", err)
	}
	model := vboxmanagetest.New(booting(log))
	model.Fail = map[string]vboxmanage.Output{"powershell read": {ExitCode: 1, Stderr: "Access denied\r\n"}}
	onModel(t, model)
	if _, err := call(t, "virt.vbox.vm.host_keys", false, newRecorder(), map[string]any{"name": "ubuntu-lab"}); err == nil || !strings.Contains(err.Error(), "Access denied") {
		t.Errorf("a log that cannot be read: %v", err)
	}
	model = vboxmanagetest.New(booting(log))
	model.SetFile(log, console(t))
	onModel(t, model)
	failing := newRecorder()
	failing.failOn = "ssh_host_keys"
	if _, err := call(t, "virt.vbox.vm.host_keys", false, failing, map[string]any{"name": "ubuntu-lab"}); err == nil {
		t.Error("recording the keys failed and the method did not")
	}
	var cannot *collection.CannotCheckError
	if _, err := call(t, "virt.vbox.vm.host_keys", true, newRecorder(), map[string]any{"name": "missing"}); !errors.As(err, &cannot) {
		t.Errorf("a check of a VM not made yet: %v", err)
	}
}

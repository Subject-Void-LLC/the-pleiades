// Tests for the methods that make Windows bases and seed Windows clones
// (virt.vbox.vm.import_disk, install and eject_seed, and clone's answer
// file), against the model host.
package vm

import (
	"bytes"
	"errors"
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
	vhdx      = `G:\iso\server-2025.vhdx`
	serverISO = `G:\iso\server-2025.iso`
	winSnap   = "3c7f4f7e-2a47-4a4d-9b0e-6a4d2f1b8c11"
	labPass   = "Lab-Passw0rd-1"
)

// diskStart is a disk's first 1024 bytes: a boot signature, and a GPT
// header's signature after it when gpt is set.
func diskStart(gpt bool) []byte {
	b := make([]byte, 1024)
	b[510], b[511] = 0x55, 0xAA
	if gpt {
		copy(b[512:], "EFI PART")
	}
	return b
}

// fixNow pins the time seeds and marks are dated with.
func fixNow(t *testing.T) time.Time {
	t.Helper()
	fixed := time.Date(2026, 9, 27, 15, 0, 0, 0, time.UTC)
	old := now
	now = func() time.Time { return fixed }
	t.Cleanup(func() { now = old })
	return fixed
}

// windowsBase is a Windows base made from a generalized image, with its
// never-booted snapshot and an IDE controller for a seed.
func windowsBase() *vboxmanagetest.VM {
	vm := base()
	vm.Name, vm.UUID, vm.Folder = "ws2025-base", "7d0b3f7c-4a30-4c43-9b3b-2d8f31c4d7a1", `G:\PleiadesLab\ws2025-base`
	vm.OSType, vm.Firmware = "Windows Server 2025 (64-bit)", "EFI"
	vm.Slots = []vboxmanage.Slot{
		{Controller: "IDE", ControllerType: "PIIX4", Medium: "none"},
		{Controller: "SATA", ControllerType: "IntelAhci", Medium: `G:\PleiadesLab\ws2025-base\ws2025-base.vdi`},
		{Controller: "SATA", ControllerType: "IntelAhci", Port: 1, Medium: "none"},
	}
	vm.Snapshots = []vboxmanagetest.Snapshot{{Name: "base", UUID: winSnap}}
	vm.Current = winSnap
	return vm
}

// administrator is a recorder holding the login the engine derives for a
// Windows clone: the Administrator's password itself.
func administrator() *recorder {
	r := newRecorder()
	r.secrets = map[string]string{wire.SecretSeedUsername: "Administrator", wire.SecretSeedPassword: labPass}
	return r
}

func winCloneParams() map[string]any {
	return map[string]any{"name": "win-lab", "from": "ws2025-base", "snapshot": "base", "login": "win-lab", "address": "192.168.56.30/24"}
}

func TestTheWindowsMethodsAreImplemented(t *testing.T) {
	for _, fqcn := range []string{"virt.vbox.vm.import_disk", "virt.vbox.vm.install", "virt.vbox.vm.eject_seed"} {
		d, ok := collection.Lookup(fqcn)
		if !ok || d.Manifest.Status != collection.StatusImplemented || d.Check == nil || d.Manifest.Reversibility.Notes == "" {
			t.Errorf("%s: %+v", fqcn, d.Manifest)
		}
	}
	if d, _ := collection.Lookup("virt.vbox.vm.clone"); !d.Manifest.SeedsLoginPassword {
		t.Error("clone does not say it can seed a Windows VM's password")
	}
}

func TestCloneWindows(t *testing.T) {
	model := vboxmanagetest.New(windowsBase())
	onModel(t, model)
	fixNow(t)
	check := administrator()
	if result, err := call(t, "virt.vbox.vm.clone", true, check, winCloneParams()); err != nil || !result.Changed || len(changes(model)) != 0 {
		t.Fatalf("check: %+v, %v, %v", result, err, changes(model))
	}
	if check.stats[statSize] != "small" {
		t.Errorf("a Windows clone with no size is %v, not small", check.stats[statSize])
	}
	rc := administrator()
	if result, err := call(t, "virt.vbox.vm.clone", false, rc, winCloneParams()); err != nil || !result.Changed {
		t.Fatalf("%+v, %v", result, err)
	}
	vm := model.VM("win-lab")
	if vm == nil || vm.MemoryMB != 2048 || vm.CPUs != 1 || vm.NICs[2].Kind != "hostonly" {
		t.Fatalf("cloned %+v", vm)
	}
	seed := model.Files[vm.Folder+`\seed.iso`]
	mac := vm.NICs[2].MAC
	dashed := strings.Join([]string{mac[0:2], mac[2:4], mac[4:6], mac[6:8], mac[8:10], mac[10:12]}, "-")
	for _, want := range []string{"<ComputerName>win-lab</ComputerName>", "<Value>" + labPass + "</Value>", "<Identifier>" + dashed + "</Identifier>", "192.168.56.30/24"} {
		if !bytes.Contains(seed, []byte(want)) {
			t.Errorf("the seed does not hold %s", want)
		}
	}
	if !bytes.Contains(seed, []byte{0, 'A', 0, 'u', 0, 't', 0, 'o'}) {
		t.Error("the seed does not name Autounattend.xml")
	}
	// On SATA, which Windows sees from its first instant, not IDE.
	for _, s := range vm.Slots {
		if strings.HasSuffix(s.Medium, `\seed.iso`) && (s.Controller != "SATA" || s.Port != 1) {
			t.Errorf("the seed went in %+v", s)
		}
	}
	noSATA := windowsBase()
	noSATA.Name, noSATA.Slots = "no-sata", noSATA.Slots[:2]
	model = vboxmanagetest.New(noSATA)
	onModel(t, model)
	p := winCloneParams()
	p["from"] = "no-sata"
	if _, err := call(t, "virt.vbox.vm.clone", false, administrator(), p); err == nil || !strings.Contains(err.Error(), "SATA port with no drive") {
		t.Errorf("a Windows base with no free SATA port: %v", err)
	}
}

func TestCloneRefusesALoginOfTheOtherKind(t *testing.T) {
	linux := base()
	model := vboxmanagetest.New(windowsBase(), linux)
	onModel(t, model)
	notAdmin := administrator()
	notAdmin.secrets[wire.SecretSeedUsername] = "labuser"
	weak := administrator()
	weak.secrets[wire.SecretSeedPassword] = "password"
	longName := winCloneParams()
	longName["name"] = "windows-lab-machine"
	for why, tt := range map[string]struct {
		rc     *recorder
		params map[string]any
		want   string
	}{
		"a key login for a Windows VM":  {seeded(), winCloneParams(), "is a Windows VM"},
		"a password login for Linux":    {administrator(), cloneParams(), "is not a Windows VM"},
		"a Windows login not the admin": {notAdmin, winCloneParams(), "names \"labuser\""},
		"a password Windows refuses":    {weak, winCloneParams(), "Windows refuses a password"},
		"a computer name too long":      {administrator(), longName, "computer name"},
	} {
		if _, err := call(t, "virt.vbox.vm.clone", false, tt.rc, tt.params); err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: %v, want an error mentioning %q", why, err, tt.want)
		}
	}
	if len(changes(model)) != 0 {
		t.Errorf("a refused clone changed the host: %v", changes(model))
	}
}

func TestImportDisk(t *testing.T) {
	model := vboxmanagetest.New()
	model.SetFile(vhdx, diskStart(true))
	onModel(t, model)
	params := map[string]any{"name": "ws2025-base", "path": vhdx, "os_type": "Windows2025_64"}
	check := newRecorder()
	if result, err := call(t, "virt.vbox.vm.import_disk", true, check, params); err != nil || !result.Changed || len(changes(model)) != 0 {
		t.Fatalf("check: %+v, %v, %v", result, err, changes(model))
	}
	if _, set := check.stats[statFirmware]; set {
		t.Errorf("a check with firmware auto said %v without reading the disk", check.stats[statFirmware])
	}
	named := newRecorder()
	withFirmware := map[string]any{"name": "ws2025-base", "path": vhdx, "os_type": "Windows2025_64", "firmware": "bios"}
	if _, err := call(t, "virt.vbox.vm.import_disk", true, named, withFirmware); err != nil || named.stats[statFirmware] != "bios" {
		t.Errorf("a check naming bios: %v, %v", named.stats, err)
	}

	rc := newRecorder()
	if result, err := call(t, "virt.vbox.vm.import_disk", false, rc, params); err != nil || !result.Changed {
		t.Fatalf("%+v, %v", result, err)
	}
	vm := model.VM("ws2025-base")
	disk := defaultHome + `\ws2025-base\ws2025-base.vdi`
	if vm == nil || vm.Firmware != "EFI" || vm.OSType != "Windows Server 2025 (64-bit)" || rc.stats[statUUID] != vm.UUID || rc.stats[statFirmware] != "efi" {
		t.Fatalf("imported %+v, stats %v", vm, rc.stats)
	}
	attached := false
	for _, s := range vm.Slots {
		attached = attached || (s.Controller == vboxmanage.ControllerSATA && s.Medium == disk)
	}
	if !attached || !bytes.Equal(model.Files[disk], diskStart(true)) || !bytes.Equal(model.Files[vhdx], diskStart(true)) {
		t.Errorf("the copy is not the VM's disk, or the image changed: %+v", vm.Slots)
	}
	want := map[string]any{"fqcn": "virt.vbox.vm.delete", "params": map[string]any{"name": "ws2025-base", "uuid": rc.stats["uuid"]}, "description": "Delete ws2025-base and the disk copied for it, which this task made."}
	if !reflect.DeepEqual(rc.stats[sdk.StatInverse], want) {
		t.Errorf("inverse = %v", rc.stats[sdk.StatInverse])
	}
	again := newRecorder()
	if result, err := call(t, "virt.vbox.vm.import_disk", false, again, params); err != nil || result.Changed || again.stats[statFirmware] != "efi" {
		t.Errorf("a second import: %+v, %v, %v", result, err, again.stats)
	}
}

func TestImportDiskReadsABIOSDisk(t *testing.T) {
	model := vboxmanagetest.New()
	model.SetFile(vhdx, diskStart(false))
	onModel(t, model)
	rc := newRecorder()
	if _, err := call(t, "virt.vbox.vm.import_disk", false, rc, map[string]any{"name": "old", "path": vhdx, "os_type": "Windows2022_64", "timeout": 60}); err != nil {
		t.Fatal(err)
	}
	if model.VM("old").Firmware != "BIOS" || rc.stats[statFirmware] != "bios" {
		t.Errorf("a disk with a master boot record: %+v", rc.stats)
	}
}

func TestImportDiskRefusals(t *testing.T) {
	model := vboxmanagetest.New()
	model.SetFile(vhdx, diskStart(true))
	model.SetFile(`G:\iso\blank.vhdx`, make([]byte, 1024))
	onModel(t, model)
	for why, p := range map[string]map[string]any{
		"no path":             {"name": "x", "os_type": "Windows2025_64"},
		"a relative path":     {"name": "x", "path": "a.vhdx", "os_type": "Windows2025_64"},
		"no OS type":          {"name": "x", "path": vhdx},
		"a bad OS type":       {"name": "x", "path": vhdx, "os_type": "Windows 2025"},
		"an unknown firmware": {"name": "x", "path": vhdx, "os_type": "Windows2025_64", "firmware": "uefi"},
		"no timeout":          {"name": "x", "path": vhdx, "os_type": "Windows2025_64", "timeout": 0},
		"a bad name":          {"name": "a b", "path": vhdx, "os_type": "Windows2025_64"},
		"a blank disk":        {"name": "x", "path": `G:\iso\blank.vhdx`, "os_type": "Windows2025_64"},
		"a missing image":     {"name": "x", "path": `G:\iso\missing.vhdx`, "os_type": "Windows2025_64", "firmware": "efi"},
	} {
		if _, err := call(t, "virt.vbox.vm.import_disk", false, newRecorder(), p); err == nil {
			t.Errorf("%s: accepted", why)
		}
	}
	if model.VM("x") != nil {
		t.Error("a refused import left a VM behind")
	}
}

func TestImportDiskCleansUpAFailure(t *testing.T) {
	params := map[string]any{"name": "ws2025-base", "path": vhdx, "os_type": "Windows2025_64"}
	disk := defaultHome + `\ws2025-base\ws2025-base.vdi`
	for key, want := range map[string]string{
		"VBoxManage.exe createvm":                                    "what was made of ws2025-base was deleted",
		"VBoxManage.exe modifyvm":                                    "what was made of ws2025-base was deleted",
		"VBoxManage.exe clonemedium":                                 "what was made of ws2025-base was deleted",
		"VBoxManage.exe storageattach ws2025-base --storagectl SATA": "what was made of ws2025-base was deleted",
	} {
		model := vboxmanagetest.New()
		model.SetFile(vhdx, diskStart(true))
		model.Fail = map[string]vboxmanage.Output{key: {ExitCode: 1, Stderr: "VBoxManage.exe: error: refused\r\n"}}
		onModel(t, model)
		_, err := call(t, "virt.vbox.vm.import_disk", false, newRecorder(), params)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s refused: %v", key, err)
		}
		if model.VM("ws2025-base") != nil {
			t.Errorf("%s refused: the VM was left", key)
		}
		if _, left := model.Files[disk]; left {
			t.Errorf("%s refused: the copied disk was left", key)
		}
	}
	// A cleanup that fails too says so.
	model := vboxmanagetest.New()
	model.SetFile(vhdx, diskStart(true))
	model.Fail = map[string]vboxmanage.Output{
		"VBoxManage.exe storageattach ws2025-base --storagectl SATA": {ExitCode: 1, Stderr: "VBoxManage.exe: error: refused\r\n"},
		"VBoxManage.exe unregistervm":                                {ExitCode: 1, Stderr: "VBoxManage.exe: error: locked\r\n"},
	}
	onModel(t, model)
	if _, err := call(t, "virt.vbox.vm.import_disk", false, newRecorder(), params); err == nil || !strings.Contains(err.Error(), "then failed too") {
		t.Errorf("a failed cleanup: %v", err)
	}
	for _, stat := range []string{statUUID, statFirmware, sdk.StatDiff, sdk.StatInverse} {
		fresh := vboxmanagetest.New()
		fresh.SetFile(vhdx, diskStart(true))
		onModel(t, fresh)
		failing := newRecorder()
		failing.failOn = stat
		if _, err := call(t, "virt.vbox.vm.import_disk", false, failing, params); err == nil {
			t.Errorf("recording %s failed and the import did not", stat)
		}
	}
}

// installParams asks for Server Core from the evaluation ISO.
func installParams() map[string]any {
	return map[string]any{"name": "ws2025-core", "installer": "windows", "iso": serverISO, "image": "Windows Server 2025 Standard Evaluation", "os_type": "Windows2025_64"}
}

// installModel is a host holding the ISO, with another VM running so a
// start goes straight through, whose install VM shuts itself down after
// reads reads of its state.
func installModel(t *testing.T, reads int) *vboxmanagetest.Host {
	t.Helper()
	model := vboxmanagetest.New(running())
	model.SetFile(serverISO, []byte("an ISO"))
	model.GuestShutdownReads = map[string]int{"ws2025-core": reads}
	onModel(t, model)
	old := installPoll
	installPoll = time.Millisecond
	t.Cleanup(func() { installPoll = old })
	return model
}

func TestInstall(t *testing.T) {
	model := installModel(t, 3)
	fixed := fixNow(t)
	check := newRecorder()
	if result, err := call(t, "virt.vbox.vm.install", true, check, installParams()); err != nil || !result.Changed || len(changes(model)) != 0 {
		t.Fatalf("check: %+v, %v, %v", result, err, changes(model))
	}
	if check.stats[statSize] != "medium" {
		t.Errorf("an install with no size is %v", check.stats[statSize])
	}
	rc := newRecorder()
	if result, err := call(t, "virt.vbox.vm.install", false, rc, installParams()); err != nil || !result.Changed {
		t.Fatalf("%+v, %v", result, err)
	}
	vm := model.VM("ws2025-core")
	if vm == nil || vm.State != vboxmanage.StatePoweroff || vm.Autostart || vm.Firmware != "BIOS" || vm.MemoryMB != 4096 || vm.CPUs != 2 {
		t.Fatalf("installed %+v", vm)
	}
	if vm.Extra[vboxmanage.ExtraInstalled] != fixed.Format(time.RFC3339) {
		t.Errorf("marked %v", vm.Extra)
	}
	for _, s := range vm.Slots {
		if s.Controller == vboxmanage.ControllerIDE && s.Medium != "none" {
			t.Errorf("a DVD was left in: %+v", s)
		}
	}
	if _, left := model.Files[vm.Folder+`\`+answerFile]; left {
		t.Error("the answer file was left on the host")
	}
	if _, kept := model.Files[serverISO]; !kept {
		t.Error("the ISO was deleted")
	}
	calls := strings.Join(model.Calls(), "\n")
	if !strings.Contains(calls, "createmedium disk --filename "+vm.Folder+`\ws2025-core.vdi --size 65536`) {
		t.Errorf("no 64 GB disk:\n%s", calls)
	}
	if rc.stats[statUUID] != vm.UUID || rc.stats[statSize] != "medium" {
		t.Errorf("stats %v", rc.stats)
	}
	again := newRecorder()
	if result, err := call(t, "virt.vbox.vm.install", false, again, installParams()); err != nil || result.Changed || again.stats[statUUID] != vm.UUID {
		t.Errorf("a second install: %+v, %v", result, err)
	}
}

func TestInstallRefusesAnUnfinishedOne(t *testing.T) {
	unfinished := windowsBase()
	unfinished.Name = "ws2025-core"
	model := vboxmanagetest.New(unfinished)
	onModel(t, model)
	if _, err := call(t, "virt.vbox.vm.install", false, newRecorder(), installParams()); err == nil || !strings.Contains(err.Error(), "not installed by this method") {
		t.Errorf("a VM this method did not install: %v", err)
	}
}

// TestInstallIsResumedByRunningItAgain stops the first run's wait with a
// failed read, as a VirtualBox hiccup or a task timeout would, and runs
// the task again: it waits for the same install and finishes it.
func TestInstallIsResumedByRunningItAgain(t *testing.T) {
	const key = "VBoxManage.exe showvminfo ws2025-core"
	model := installModel(t, 3)
	model.Fail = map[string]vboxmanage.Output{key: refused}
	model.Skip = map[string]int{key: 5}
	if _, err := call(t, "virt.vbox.vm.install", false, newRecorder(), installParams()); err == nil {
		t.Fatal("the first run's failed read did not stop it")
	}
	model.Fail = nil
	check := newRecorder()
	if result, err := call(t, "virt.vbox.vm.install", true, check, installParams()); err != nil || !result.Changed {
		t.Fatalf("a check of an unfinished install: %+v, %v", result, err)
	}
	again := newRecorder()
	if result, err := call(t, "virt.vbox.vm.install", false, again, installParams()); err != nil || !result.Changed {
		t.Fatalf("the second run: %+v, %v", result, err)
	}
	vm := model.VM("ws2025-core")
	if vm.State != vboxmanage.StatePoweroff || vm.Extra[vboxmanage.ExtraInstalled] == "" || again.stats[statUUID] != vm.UUID {
		t.Errorf("resumed to %+v, stats %v", vm, again.stats)
	}
	if _, left := model.Files[vm.Folder+`\`+answerFile]; left {
		t.Error("the answer file was left")
	}
}

// TestInstallWaitsThroughAnotherClientsLock is the lab's first real
// install: a read of the VM met another client's pending lock mid-wait.
func TestInstallWaitsThroughAnotherClientsLock(t *testing.T) {
	const key = "VBoxManage.exe showvminfo ws2025-core"
	model := installModel(t, 3)
	model.Fail = map[string]vboxmanage.Output{key: {ExitCode: 1,
		Stderr: "VBoxManage.exe: error: The machine 'ws2025-core' already has a lock request pending\r\n"}}
	model.Skip = map[string]int{key: 5}
	result, err := call(t, "virt.vbox.vm.install", false, newRecorder(), map[string]any{"name": "ws2025-core", "installer": "windows", "iso": serverISO, "image": "1", "os_type": "Windows2025_64", "timeout": 1})
	// Every read past the fifth is locked, so the wait runs to its timeout
	// rather than failing on the first lock.
	if err == nil || !strings.Contains(err.Error(), "had not finished within 1s") {
		t.Errorf("a lock mid-wait: %+v, %v", result, err)
	}
}

func TestInstallThatDoesNotFinish(t *testing.T) {
	model := installModel(t, 1<<30)
	params := installParams()
	params["timeout"] = 1
	_, err := call(t, "virt.vbox.vm.install", false, newRecorder(), params)
	vm := model.VM("ws2025-core")
	picture := vm.Folder + `\` + screenshotFile
	if err == nil || !strings.Contains(err.Error(), "had not finished within 1s") || !strings.Contains(err.Error(), picture) {
		t.Errorf("a timeout: %v", err)
	}
	if _, saved := model.Files[picture]; !saved || vm.State != vboxmanage.StateRunning {
		t.Errorf("the VM was not left running with its screen saved: %s", vm.State)
	}

	aborted := installModel(t, -1)
	if _, err := call(t, "virt.vbox.vm.install", false, newRecorder(), installParams()); err == nil || !strings.Contains(err.Error(), "aborted") {
		t.Errorf("an aborted install: %v", err)
	}
	if aborted.VM("ws2025-core") == nil {
		t.Error("an aborted install's VM was deleted, not left to look at")
	}
}

func TestInstallRefusals(t *testing.T) {
	installModel(t, 0)
	for why, change := range map[string]func(map[string]any){
		"no ISO":               func(p map[string]any) { delete(p, "iso") },
		"a relative ISO":       func(p map[string]any) { p["iso"] = "server.iso" },
		"no image":             func(p map[string]any) { delete(p, "image") },
		"a bad image":          func(p map[string]any) { p["image"] = "Windows<Server" },
		"no OS type":           func(p map[string]any) { delete(p, "os_type") },
		"a small disk":         func(p map[string]any) { p["disk_gb"] = 20 },
		"a disk not a number":  func(p map[string]any) { p["disk_gb"] = "big" },
		"a bad size":           func(p map[string]any) { p["size"] = "huge" },
		"a bad language":       func(p map[string]any) { p["language"] = "English" },
		"no timeout":           func(p map[string]any) { p["timeout"] = 0 },
		"a bad name":           func(p map[string]any) { p["name"] = "a b" },
		"too big for the host": func(p map[string]any) { p["size"] = "xlarge" },
	} {
		p := installParams()
		change(p)
		if _, err := call(t, "virt.vbox.vm.install", false, newRecorder(), p); err == nil {
			t.Errorf("%s: accepted", why)
		}
	}
}

func TestInstallCleansUpBeforeItStarts(t *testing.T) {
	for _, key := range []string{"VBoxManage.exe createmedium", "VBoxManage.exe storageattach ws2025-core --storagectl SATA", "VBoxManage.exe storageattach ws2025-core --storagectl IDE"} {
		model := installModel(t, 0)
		model.Fail = map[string]vboxmanage.Output{key: {ExitCode: 1, Stderr: "VBoxManage.exe: error: refused\r\n"}}
		if _, err := call(t, "virt.vbox.vm.install", false, newRecorder(), installParams()); err == nil || !strings.Contains(err.Error(), "was deleted") {
			t.Errorf("%s refused: %v", key, err)
		}
		if model.VM("ws2025-core") != nil {
			t.Errorf("%s refused: the VM was left", key)
		}
	}
	// No VM running and no autostart service: the start fails, and what
	// was made is deleted.
	model := vboxmanagetest.New()
	model.NoAutostartService = true
	model.SetFile(serverISO, []byte("an ISO"))
	onModel(t, model)
	if _, err := call(t, "virt.vbox.vm.install", false, newRecorder(), installParams()); err == nil || model.VM("ws2025-core") != nil {
		t.Errorf("a start that failed: %v", err)
	}
}

// seededVM is a Windows clone still holding its seed.
func seededVM(state string) *vboxmanagetest.VM {
	return &vboxmanagetest.VM{Name: "win-lab", UUID: "5f2c1a3e-7b8d-4e6f-a1b2-c3d4e5f60718", State: state, MemoryMB: 2048, CPUs: 1,
		Folder: `G:\PleiadesLab\win-lab`, OSType: "Windows Server 2025 (64-bit)",
		Slots: []vboxmanage.Slot{
			{Controller: "IDE", ControllerType: "PIIX4", Medium: `G:\PleiadesLab\win-lab\seed.iso`},
			{Controller: "IDE", ControllerType: "PIIX4", Device: 1, Medium: "none"},
		}}
}

func TestEjectSeed(t *testing.T) {
	model := vboxmanagetest.New(seededVM(vboxmanage.StateRunning))
	model.SetFile(`G:\PleiadesLab\win-lab\seed.iso`, []byte("seed"))
	onModel(t, model)
	params := map[string]any{"name": "win-lab"}
	check := newRecorder()
	if result, err := call(t, "virt.vbox.vm.eject_seed", true, check, params); err != nil || !result.Changed || len(changes(model)) != 0 || check.stats[statEjected] != true {
		t.Fatalf("check: %+v, %v, %v, %v", result, err, changes(model), check.stats)
	}
	rc := newRecorder()
	if result, err := call(t, "virt.vbox.vm.eject_seed", false, rc, params); err != nil || !result.Changed {
		t.Fatalf("%+v, %v", result, err)
	}
	if slot := model.VM("win-lab").Slots[0]; slot.Medium != "emptydrive" {
		t.Errorf("the drive holds %s", slot.Medium)
	}
	if _, left := model.Files[`G:\PleiadesLab\win-lab\seed.iso`]; left {
		t.Error("the seed file was left")
	}
	if !strings.Contains(strings.Join(model.Calls(), "\n"), "--medium emptydrive --forceunmount") {
		t.Errorf("the eject did not force a locked tray: %v", model.Calls())
	}
	if before, after := rc.diff(t); before[diffSeeded] != true || after[diffSeeded] != false {
		t.Errorf("diff %v -> %v", before, after)
	}
	again := newRecorder()
	if result, err := call(t, "virt.vbox.vm.eject_seed", false, again, params); err != nil || result.Changed || again.stats[statEjected] != false {
		t.Errorf("a second eject: %+v, %v", result, err)
	}
}

func TestEjectSeedWaitsOutTheDrivesLockAndFinishesALeftSeed(t *testing.T) {
	model := vboxmanagetest.New(seededVM(vboxmanage.StateRunning))
	model.SetFile(`G:\PleiadesLab\win-lab\seed.iso`, []byte("seed"))
	const key = "VBoxManage.exe closemedium dvd"
	model.Fail = map[string]vboxmanage.Output{key: {ExitCode: 1,
		Stderr: "VBoxManage.exe: error: Medium 'G:\\PleiadesLab\\win-lab\\seed.iso' is locked for reading by another task\r\n"}}
	model.Times = map[string]int{key: 2}
	onModel(t, model)
	if _, err := call(t, "virt.vbox.vm.eject_seed", false, newRecorder(), map[string]any{"name": "win-lab"}); err != nil {
		t.Fatalf("a lock held for two tries: %v", err)
	}
	if _, left := model.Files[`G:\PleiadesLab\win-lab\seed.iso`]; left {
		t.Error("the seed was left")
	}

	// A run that emptied the drive but could not delete the seed leaves it
	// on the host: the next run deletes it.
	left := seededVM(vboxmanage.StateRunning)
	left.Slots[0].Medium = "emptydrive"
	model = vboxmanagetest.New(left)
	model.SetFile(`G:\PleiadesLab\win-lab\seed.iso`, []byte("seed"))
	onModel(t, model)
	rc := newRecorder()
	if result, err := call(t, "virt.vbox.vm.eject_seed", false, rc, map[string]any{"name": "win-lab"}); err != nil || !result.Changed || rc.stats[statEjected] != true {
		t.Fatalf("a seed left on the host: %+v, %v", result, err)
	}
	if _, stays := model.Files[`G:\PleiadesLab\win-lab\seed.iso`]; stays {
		t.Error("a seed out of every drive was not deleted")
	}

	// A running VM keeps its image locked until it stops: the drive is
	// emptied, the task says the file stays, and succeeds.
	stuck := vboxmanagetest.New(seededVM(vboxmanage.StateRunning))
	stuck.SetFile(`G:\PleiadesLab\win-lab\seed.iso`, []byte("seed"))
	locked := vboxmanage.Output{ExitCode: 1, Stderr: "VBoxManage.exe: error: Medium is locked for reading by another task\r\n"}
	stuck.Fail = map[string]vboxmanage.Output{key: locked}
	onModel(t, stuck)
	running := newRecorder()
	if result, err := call(t, "virt.vbox.vm.eject_seed", false, running, map[string]any{"name": "win-lab"}); err != nil || !result.Changed || running.stats[statDeleted] != false {
		t.Errorf("a running VM's locked seed: %+v, %v, %v", result, err, running.stats)
	}
	if warnings, _ := running.stats[sdk.StatWarnings].([]string); len(warnings) != 1 || !strings.Contains(warnings[0], "run virt.vbox.vm.eject_seed again once the VM is stopped") {
		t.Errorf("warnings %v", running.stats[sdk.StatWarnings])
	}
	if stuck.VM("win-lab").Slots[0].Medium != "emptydrive" {
		t.Error("the drive was not emptied")
	}
	// Stopped, a lock is a failure to report.
	offLocked := vboxmanagetest.New(seededVM(vboxmanage.StatePoweroff))
	offLocked.SetFile(`G:\PleiadesLab\win-lab\seed.iso`, []byte("seed"))
	offLocked.Fail = map[string]vboxmanage.Output{key: locked}
	onModel(t, offLocked)
	if _, err := call(t, "virt.vbox.vm.eject_seed", false, newRecorder(), map[string]any{"name": "win-lab"}); err == nil || !strings.Contains(err.Error(), "locked") {
		t.Errorf("a stopped VM's locked seed: %v", err)
	}
	for _, stat := range []string{sdk.StatWarnings, statDeleted} {
		failing := vboxmanagetest.New(seededVM(vboxmanage.StateRunning))
		failing.SetFile(`G:\PleiadesLab\win-lab\seed.iso`, []byte("seed"))
		failing.Fail = map[string]vboxmanage.Output{key: locked}
		onModel(t, failing)
		rc := newRecorder()
		rc.failOn = stat
		if _, err := call(t, "virt.vbox.vm.eject_seed", false, rc, map[string]any{"name": "win-lab"}); err == nil {
			t.Errorf("recording %s failed and the task did not", stat)
		}
	}
	unreadable := vboxmanagetest.New(seededVM(vboxmanage.StateRunning))
	unreadable.Fail = map[string]vboxmanage.Output{"powershell read": {ExitCode: 1, Stderr: "Access denied\r\n"}}
	onModel(t, unreadable)
	if _, err := call(t, "virt.vbox.vm.eject_seed", false, newRecorder(), map[string]any{"name": "win-lab"}); err == nil {
		t.Error("ejected with the seed's folder unreadable")
	}
}

func TestEjectSeedFailures(t *testing.T) {
	model := vboxmanagetest.New(seededVM(vboxmanage.StatePoweroff))
	onModel(t, model)
	var cannot *collection.CannotCheckError
	if _, err := call(t, "virt.vbox.vm.eject_seed", true, newRecorder(), map[string]any{"name": "missing"}); !errors.As(err, &cannot) {
		t.Errorf("a check of a VM not there yet: %v", err)
	}
	if _, err := call(t, "virt.vbox.vm.eject_seed", false, newRecorder(), map[string]any{"name": "missing"}); err == nil {
		t.Error("ejected from a VM not there")
	}
	for _, key := range []string{"VBoxManage.exe storageattach", "VBoxManage.exe closemedium"} {
		failing := vboxmanagetest.New(seededVM(vboxmanage.StatePoweroff))
		failing.Fail = map[string]vboxmanage.Output{key: {ExitCode: 1, Stderr: "VBoxManage.exe: error: refused\r\n"}}
		onModel(t, failing)
		if _, err := call(t, "virt.vbox.vm.eject_seed", false, newRecorder(), map[string]any{"name": "win-lab"}); err == nil {
			t.Errorf("%s refused and the eject did not fail", key)
		}
	}
	for _, stat := range []string{statEjected, sdk.StatDiff} {
		onModel(t, vboxmanagetest.New(seededVM(vboxmanage.StatePoweroff)))
		failing := newRecorder()
		failing.failOn = stat
		if _, err := call(t, "virt.vbox.vm.eject_seed", false, failing, map[string]any{"name": "win-lab"}); err == nil {
			t.Errorf("recording %s failed and the eject did not", stat)
		}
	}
}

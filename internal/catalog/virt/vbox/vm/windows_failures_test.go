// Tests for each step of making a Windows base or ejecting a seed
// failing on the model host: what the task says, and what it leaves.
package vm

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/vboxmanage"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/vboxmanage/vboxmanagetest"
)

// refused is what a failing VBoxManage call answers.
var refused = vboxmanage.Output{ExitCode: 1, Stderr: "VBoxManage.exe: error: refused\r\n"}

func TestInstallStepsFailing(t *testing.T) {
	const vm = "ws2025-core"
	for name, tt := range map[string]struct {
		key  string
		skip int
		want string
	}{
		"the answer file's upload":     {key: "powershell upload", want: "was deleted"},
		"reading the new VM":           {key: "VBoxManage.exe showvminfo " + vm, skip: 1, want: "then failed too"},
		"a picture of a stuck install": {key: "VBoxManage.exe controlvm " + vm + " screenshotpng", want: "could not be saved"},
		"clearing the autostart mark":  {key: "VBoxManage.exe modifyvm " + vm + " --autostart-enabled off", want: "refused"},
		"taking the DVDs out":          {key: "VBoxManage.exe storageattach " + vm + " --storagectl IDE --port 0 --device 0 --medium none", want: "refused"},
		"deleting the answer file":     {key: "VBoxManage.exe closemedium dvd", want: "refused"},
		"marking it installed":         {key: "VBoxManage.exe setextradata " + vm, want: "refused"},
	} {
		model := installModel(t, 2)
		if name == "a picture of a stuck install" {
			model.GuestShutdownReads = nil
		}
		model.Fail = map[string]vboxmanage.Output{tt.key: refused}
		model.Skip = map[string]int{tt.key: tt.skip}
		p := installParams()
		p["timeout"] = 1
		if _, err := call(t, "virt.vbox.vm.install", false, newRecorder(), p); err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s failing: %v, want one mentioning %q", name, err, tt.want)
		}
	}
}

func TestInstallRecordingAndReading(t *testing.T) {
	for _, stat := range []string{statSize, statUUID, sdk.StatDiff, sdk.StatInverse} {
		installModel(t, 0)
		failing := newRecorder()
		failing.failOn = stat
		if _, err := call(t, "virt.vbox.vm.install", false, failing, installParams()); err == nil {
			t.Errorf("recording %s failed and the install did not", stat)
		}
	}
	installed := windowsBase()
	installed.Name = "ws2025-core"
	installed.Extra = map[string]string{vboxmanage.ExtraInstalled: "2026-09-27T15:00:00Z"}
	for _, stat := range []string{statUUID, statSize, sdk.StatDiff} {
		onModel(t, vboxmanagetest.New(installed))
		failing := newRecorder()
		failing.failOn = stat
		if _, err := call(t, "virt.vbox.vm.install", false, failing, installParams()); err == nil {
			t.Errorf("recording %s for an installed VM failed and the task did not", stat)
		}
	}
	for key, want := range map[string]string{
		"VBoxManage.exe showvminfo":   "refused",
		"VBoxManage.exe getextradata": "refused",
	} {
		model := vboxmanagetest.New(installed)
		model.Fail = map[string]vboxmanage.Output{key: refused}
		onModel(t, model)
		if _, err := call(t, "virt.vbox.vm.install", false, newRecorder(), installParams()); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s failing: %v", key, err)
		}
	}
	short := installModel(t, 0)
	short.AvailableMB = 3000
	if _, err := call(t, "virt.vbox.vm.install", true, newRecorder(), installParams()); err == nil || !strings.Contains(err.Error(), "free") {
		t.Errorf("a host without the memory free: %v", err)
	}
}

func TestImportDiskReadingAndRecording(t *testing.T) {
	params := map[string]any{"name": "ws2025-base", "path": vhdx, "os_type": "Windows2025_64"}
	model := vboxmanagetest.New()
	model.Fail = map[string]vboxmanage.Output{"VBoxManage.exe showvminfo": refused}
	onModel(t, model)
	if _, err := call(t, "virt.vbox.vm.import_disk", false, newRecorder(), params); err == nil {
		t.Error("imported with the host's VMs unreadable")
	}
	for _, stat := range []string{statUUID, statFirmware} {
		onModel(t, vboxmanagetest.New(windowsBase()))
		failing := newRecorder()
		failing.failOn = stat
		found := map[string]any{"name": "ws2025-base", "path": vhdx, "os_type": "Windows2025_64"}
		if _, err := call(t, "virt.vbox.vm.import_disk", false, failing, found); err == nil {
			t.Errorf("recording %s for a VM found failed and the task did not", stat)
		}
	}
	onModel(t, vboxmanagetest.New())
	failing := newRecorder()
	failing.failOn = statFirmware
	named := map[string]any{"name": "ws2025-base", "path": vhdx, "os_type": "Windows2025_64", "firmware": "efi"}
	if _, err := call(t, "virt.vbox.vm.import_disk", true, failing, named); err == nil {
		t.Error("a check whose firmware could not be recorded passed")
	}
	if _, err := call(t, "virt.vbox.vm.import_disk", false, newRecorder(), map[string]any{"name": "x", "path": vhdx, "os_type": "Windows2025_64", "timeout": "soon"}); err == nil {
		t.Error("a timeout that is not a number was taken")
	}
}

func TestImportDiskCleanupFailures(t *testing.T) {
	params := map[string]any{"name": "ws2025-base", "path": vhdx, "os_type": "Windows2025_64"}
	disk := defaultHome + `\ws2025-base\ws2025-base.vdi`
	attach := "VBoxManage.exe storageattach ws2025-base --storagectl SATA"
	for name, fail := range map[string]struct {
		keys map[string]int
	}{
		"reading the VM for the cleanup": {map[string]int{"VBoxManage.exe showvminfo ws2025-base": 1}},
		"listing disks for the cleanup":  {map[string]int{attach: 0, "VBoxManage.exe list hdds": 1}},
		"deleting the copied disk":       {map[string]int{attach: 0, "VBoxManage.exe closemedium disk " + disk + " --delete": 0}},
	} {
		model := vboxmanagetest.New()
		model.SetFile(vhdx, diskStart(true))
		model.Fail, model.Skip = map[string]vboxmanage.Output{}, map[string]int{}
		for key, skip := range fail.keys {
			model.Fail[key], model.Skip[key] = refused, skip
		}
		onModel(t, model)
		if _, err := call(t, "virt.vbox.vm.import_disk", false, newRecorder(), params); err == nil || !strings.Contains(err.Error(), "then failed too") {
			t.Errorf("%s failing: %v", name, err)
		}
	}
}

func TestEjectSeedOfAVMInNoFolder(t *testing.T) {
	loose := seededVM(vboxmanage.StateRunning)
	loose.Folder = ""
	onModel(t, vboxmanagetest.New(loose))
	if _, err := call(t, "virt.vbox.vm.eject_seed", false, newRecorder(), map[string]any{"name": "win-lab"}); err == nil || !strings.Contains(err.Error(), "no folder") {
		t.Errorf("a VM whose folder cannot be named: %v", err)
	}
	model := vboxmanagetest.New(seededVM(vboxmanage.StateRunning))
	model.Fail = map[string]vboxmanage.Output{"VBoxManage.exe showvminfo": refused}
	onModel(t, model)
	if _, err := call(t, "virt.vbox.vm.eject_seed", false, newRecorder(), map[string]any{"name": "win-lab"}); err == nil {
		t.Error("ejected from a VM it could not read")
	}
}

func TestInstallEarlyRefusals(t *testing.T) {
	small := installModel(t, 0)
	small.CPUs = 1
	if _, err := call(t, "virt.vbox.vm.install", true, newRecorder(), installParams()); err == nil || !strings.Contains(err.Error(), "CPU") {
		t.Errorf("a medium install on a one-processor host: %v", err)
	}
	failing := installModel(t, 0)
	failing.Fail = map[string]vboxmanage.Output{"VBoxManage.exe createvm": refused}
	if _, err := call(t, "virt.vbox.vm.install", false, newRecorder(), installParams()); err == nil || failing.VM("ws2025-core") != nil {
		t.Errorf("createvm refused: %v", err)
	}
	if _, err := call(t, "virt.vbox.vm.install", false, newRecorder(), map[string]any{"name": "a b", "iso": serverISO, "image": "1", "os_type": "Windows2025_64"}); err == nil {
		t.Error("installed a VM with a bad name")
	}
}

// TestInstallReadsFailing fails every read of the VM from a point on:
// while the DVDs go in, the cleanup cannot read the VM either and says so;
// while the install runs, the task fails and the VM is left, as a started
// install is.
func TestInstallReadsFailing(t *testing.T) {
	const key = "VBoxManage.exe showvminfo ws2025-core"
	for skip, want := range map[int]string{2: "then failed too", 3: "then failed too", 5: "refused"} {
		model := installModel(t, 5)
		model.Fail = map[string]vboxmanage.Output{key: refused}
		model.Skip = map[string]int{key: skip}
		_, err := call(t, "virt.vbox.vm.install", false, newRecorder(), installParams())
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("reads from %d on failing: %v, want one mentioning %q", skip+1, err, want)
		}
		if model.VM("ws2025-core") == nil {
			t.Errorf("reads from %d on failing: the VM was deleted without being read", skip+1)
		}
	}
}

func TestCloneWindowsRecordingItsSize(t *testing.T) {
	onModel(t, vboxmanagetest.New(windowsBase()))
	failing := administrator()
	failing.failOn = statSize
	if _, err := call(t, "virt.vbox.vm.clone", false, failing, winCloneParams()); err == nil {
		t.Error("a clone whose size could not be recorded succeeded")
	}
}

// unfinishedInstall is a host whose install VM is still running with its
// answer file in a drive, as a run that stopped waiting leaves it.
func unfinishedInstall(t *testing.T) *vboxmanagetest.Host {
	t.Helper()
	model := installModel(t, 1)
	model.GuestShutdownReads = map[string]int{"ws2025-core": 1 << 30}
	p := installParams()
	p["timeout"] = 1
	if _, err := call(t, "virt.vbox.vm.install", false, newRecorder(), p); err == nil {
		t.Fatal("the first run finished")
	}
	model.GuestShutdownReads = map[string]int{"ws2025-core": 1}
	return model
}

func TestResumeFailures(t *testing.T) {
	for key, want := range map[string]string{
		"VBoxManage.exe getextradata":    "refused",
		"VBoxManage.exe closemedium dvd": "refused",
	} {
		model := unfinishedInstall(t)
		model.Fail = map[string]vboxmanage.Output{key: refused}
		if _, err := call(t, "virt.vbox.vm.install", false, newRecorder(), installParams()); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s failing on resume: %v", key, err)
		}
	}
	for _, stat := range []string{statSize, statUUID, sdk.StatDiff} {
		unfinishedInstall(t)
		failing := newRecorder()
		failing.failOn = stat
		if _, err := call(t, "virt.vbox.vm.install", false, failing, installParams()); err == nil {
			t.Errorf("recording %s on resume failed and the task did not", stat)
		}
	}
	stuck := unfinishedInstall(t)
	stuck.GuestShutdownReads = map[string]int{"ws2025-core": 1 << 30}
	p := installParams()
	p["timeout"] = 1
	if _, err := call(t, "virt.vbox.vm.install", false, newRecorder(), p); err == nil || !strings.Contains(err.Error(), "had not finished") {
		t.Errorf("a resumed install that still does not finish: %v", err)
	}
	loose := windowsBase()
	loose.Name, loose.Folder = "ws2025-core", ""
	onModel(t, vboxmanagetest.New(loose))
	if _, err := call(t, "virt.vbox.vm.install", false, newRecorder(), installParams()); err == nil || !strings.Contains(err.Error(), "no folder") {
		t.Errorf("a VM in no folder: %v", err)
	}
}

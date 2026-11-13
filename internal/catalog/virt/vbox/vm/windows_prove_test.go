// Tests for a Windows clone's proof that it took its answer file, and for
// its seed, which holds the Administrator's password in clear, never
// outliving a failure. The guest's WinRM login is scripted; the lab test
// TestLab_WindowsClonesTakeTheirOwnAnswerFiles reaches a real one.
package vm

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/vboxmanage"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/vboxmanage/vboxmanagetest"
)

// guest replaces the clone's WinRM login with answer for the test, and
// counts the tries.
func guest(t *testing.T, answer func(address, password string) (string, error)) *int {
	t.Helper()
	tries := 0
	old, oldInterval := proveGuest, proveInterval
	proveGuest = func(_ context.Context, address, password string) (string, error) {
		tries++
		return answer(address, password)
	}
	proveInterval = time.Millisecond
	t.Cleanup(func() { proveGuest, proveInterval = old, oldInterval })
	return &tries
}

// takesTheSeed is a guest whose first boot took its answer file: it lets
// labPass in at the address the clone was given, named win-lab, after a
// few tries while it boots.
func takesTheSeed(booting int) func(address, password string) (string, error) {
	return func(address, password string) (string, error) {
		if booting > 0 {
			booting--
			return "", errors.New("dial tcp 192.168.56.30:5985: connection refused")
		}
		if address != "192.168.56.30" || password != labPass {
			return "", errors.New("http response error: 401 - invalid content type")
		}
		return "WIN-LAB", nil
	}
}

// ignoresTheSeed is a guest whose base was not generalized: it keeps the
// base's own Administrator password, so the seeded one is refused.
func ignoresTheSeed(string, string) (string, error) {
	return "", errors.New("http response error: 401 - invalid content type")
}

// seedLeft reports whether vm's seed is still on the model host, as a file
// or in a drive.
func seedLeft(model *vboxmanagetest.Host, folder string) bool {
	path := folder + `\` + seedFile
	if _, ok := model.Files[path]; ok {
		return true
	}
	if vm := model.VM("win-lab"); vm != nil {
		for _, s := range vm.Slots {
			if s.Medium == path {
				return true
			}
		}
	}
	return false
}

// TestCloneWindows_ProvesTheSeededLogin: a clone whose first boot takes its
// answer file is started, waited for until its seeded Administrator gets in
// at its address with its computer name, then shut down by its power
// button with its seed deleted and no autostart mark left.
func TestCloneWindows_ProvesTheSeededLogin(t *testing.T) {
	model := vboxmanagetest.New(windowsBase())
	onModel(t, model)
	fixNow(t)
	tries := guest(t, takesTheSeed(2))
	if result, err := call(t, "virt.vbox.vm.clone", false, administrator(), winCloneParams()); err != nil || !result.Changed {
		t.Fatalf("%+v, %v", result, err)
	}
	vm := model.VM("win-lab")
	if vm == nil || vm.State != vboxmanage.StatePoweroff || vm.Autostart {
		t.Fatalf("a proven clone is left %+v, want it stopped with no autostart mark", vm)
	}
	if *tries != 3 {
		t.Errorf("tried the login %d times, want 3 (two while it booted)", *tries)
	}
	if seedLeft(model, vm.Folder) {
		t.Error("a proven clone's seed is still on the host")
	}
	pressed := false
	for _, c := range model.Calls() {
		pressed = pressed || strings.HasPrefix(c, "VBoxManage.exe controlvm win-lab acpipowerbutton")
		if strings.HasPrefix(c, "VBoxManage.exe controlvm win-lab poweroff") {
			t.Errorf("a proven clone that answers its power button had its power cut: %s", c)
		}
	}
	if !pressed {
		t.Error("a proven clone was not shut down by its power button")
	}
}

// TestClone_ABaseThatIgnoresTheAnswerFileIsPoweredOff: a base that is not
// generalized boots every clone with its own account, so the seeded login
// never gets in; the clone is powered off and deleted, with its seed, and
// the task fails saying why. A guest that lets the login in under another
// computer name fails the same way, at once.
func TestClone_ABaseThatIgnoresTheAnswerFileIsPoweredOff(t *testing.T) {
	for why, tt := range map[string]struct {
		answer func(string, string) (string, error)
		want   string
	}{
		"the seeded login refused": {ignoresTheSeed, "not generalized"},
		"another computer name":    {func(string, string) (string, error) { return "WIN-BASE", nil }, `names itself "WIN-BASE"`},
	} {
		model := vboxmanagetest.New(windowsBase())
		onModel(t, model)
		fixNow(t)
		guest(t, tt.answer)
		p := winCloneParams()
		p["timeout"] = 1
		_, err := call(t, "virt.vbox.vm.clone", false, administrator(), p)
		if err == nil || !strings.Contains(err.Error(), tt.want) || !strings.Contains(err.Error(), "was deleted") {
			t.Errorf("%s: %v", why, err)
		}
		poweredOff := false
		for _, c := range model.Calls() {
			poweredOff = poweredOff || strings.HasPrefix(c, "VBoxManage.exe controlvm win-lab poweroff")
		}
		if !poweredOff || model.VM("win-lab") != nil {
			t.Errorf("%s: powered off %v, clone left %+v", why, poweredOff, model.VM("win-lab"))
		}
		if seedLeft(model, defaultHome+`\win-lab`) {
			t.Errorf("%s: the seed outlived the failed clone", why)
		}
	}
}

// TestClone_ABaseThatStopsIsNotWaitedFor: a clone whose guest stops on its
// own before the login gets in fails at once rather than at the timeout.
func TestClone_ABaseThatStopsIsNotWaitedFor(t *testing.T) {
	model := vboxmanagetest.New(windowsBase())
	onModel(t, model)
	fixNow(t)
	guest(t, func(string, string) (string, error) {
		model.VM("win-lab").State = vboxmanage.StateAborted
		return "", errors.New("connection refused")
	})
	if _, err := call(t, "virt.vbox.vm.clone", false, administrator(), winCloneParams()); err == nil || !strings.Contains(err.Error(), "stopped (aborted)") || model.VM("win-lab") != nil {
		t.Errorf("%v, clone left %+v", err, model.VM("win-lab"))
	}
}

// TestClone_SeedISORemovedOnFailure: the seed holds a Windows
// Administrator's password in clear, so every failure after it was written
// deletes it, even when the half-made VM cannot be deleted: a failed
// attach, a failed proof, and a VM whose drive cannot be detached.
func TestClone_SeedISORemovedOnFailure(t *testing.T) {
	folder := defaultHome + `\win-lab`
	// Detaching the seed's drive is what deleteVM does first, so failing it
	// fails the whole delete before the seed's own deletion.
	detach := "VBoxManage.exe storageattach win-lab --storagectl SATA --port 1 --device 0 --medium none"
	for why, tt := range map[string]struct {
		fail   map[string]vboxmanage.Output
		answer func(string, string) (string, error)
		want   string
	}{
		"the attach fails": {map[string]vboxmanage.Output{
			"VBoxManage.exe storageattach win-lab --storagectl SATA --port 1 --device 0 --type dvddrive --medium " + folder: {ExitCode: 1, Stderr: "VBoxManage.exe: error: refused\r\n"},
		}, takesTheSeed(0), "was deleted"},
		// Before 2026-11-13 this left the seed: deleteVM stopped at the
		// failed unregister, before it removed the seed's file.
		"the attach fails and the unregister too": {map[string]vboxmanage.Output{
			"VBoxManage.exe storageattach win-lab --storagectl SATA --port 1 --device 0 --type dvddrive --medium " + folder: {ExitCode: 1, Stderr: "VBoxManage.exe: error: refused\r\n"},
			"VBoxManage.exe unregistervm": {ExitCode: 1, Stderr: "VBoxManage.exe: error: refused\r\n"},
		}, takesTheSeed(0), "its seed " + folder + `\seed.iso was deleted`},
		"the proof fails": {nil, ignoresTheSeed, "its seed with it"},
		"the proof fails and the delete too": {map[string]vboxmanage.Output{detach: {ExitCode: 1, Stderr: "VBoxManage.exe: error: refused\r\n"}},
			ignoresTheSeed, "its seed " + folder + `\seed.iso was deleted`},
	} {
		base := windowsBase()
		model := vboxmanagetest.New(base)
		model.Fail = tt.fail
		onModel(t, model)
		fixNow(t)
		guest(t, tt.answer)
		p := winCloneParams()
		p["timeout"] = 1
		_, err := call(t, "virt.vbox.vm.clone", false, administrator(), p)
		if err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: %v, want an error mentioning %q", why, err, tt.want)
		}
		if seedLeft(model, folder) {
			t.Errorf("%s: the seed, holding the Administrator's password, is still on the host", why)
		}
	}
}

// TestClone_ATimeoutIsForAWindowsVMOnly: a Linux clone is never started,
// so a timeout for one is refused rather than read and never used.
func TestClone_ATimeoutIsForAWindowsVMOnly(t *testing.T) {
	model := vboxmanagetest.New(base())
	onModel(t, model)
	p := cloneParams()
	p["timeout"] = 600
	if _, err := call(t, "virt.vbox.vm.clone", false, seeded(), p); err == nil || !strings.Contains(err.Error(), "not started by clone") {
		t.Errorf("a timeout for a Linux clone: %v", err)
	}
	if len(changes(model)) != 0 {
		t.Errorf("a refused clone changed the host: %v", changes(model))
	}
}

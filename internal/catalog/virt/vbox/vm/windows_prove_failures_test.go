// The failure paths of a Windows clone's proof, its shutdown afterwards,
// and the throwing away of a failed clone with its seed, each against the
// model host failing one step.
package vm

import (
	"context"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/vboxmanage"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/vboxmanage/vboxmanagetest"
)

// failedRun is a VBoxManage run that failed.
var failedRun = vboxmanage.Output{ExitCode: 1, Stderr: "VBoxManage.exe: error: refused\r\n"}

// TestCloneWindows_ProofStepsFailing: each step of the proof failing fails
// the clone and throws it away, its seed with it, saying what failed.
func TestCloneWindows_ProofStepsFailing(t *testing.T) {
	for why, tt := range map[string]struct {
		setup func(*vboxmanagetest.Host)
		want  string
	}{
		"the host has no room to start it": {func(m *vboxmanagetest.Host) { m.AvailableMB = 512 }, "MB"},
		"the start is refused": {func(m *vboxmanagetest.Host) {
			m.Fail = map[string]vboxmanage.Output{"sc.exe start": {ExitCode: 1, Stdout: "[SC] StartService FAILED 5:\r\n\r\nAccess is denied.\r\n"}}
		}, "was deleted"},
		"the VM cannot be read while it boots": {func(m *vboxmanagetest.Host) {
			// Reads of the clone before its start succeed; the one after the
			// first failed login does not.
			m.Fail = map[string]vboxmanage.Output{"VBoxManage.exe showvminfo win-lab": failedRun}
			m.Skip = map[string]int{"VBoxManage.exe showvminfo win-lab": 6}
		}, "refused"},
	} {
		model := vboxmanagetest.New(windowsBase())
		tt.setup(model)
		onModel(t, model)
		fixNow(t)
		guest(t, ignoresTheSeed)
		p := winCloneParams()
		p["timeout"] = 1
		if _, err := call(t, "virt.vbox.vm.clone", false, administrator(), p); err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: %v, want an error mentioning %q", why, err, tt.want)
		}
		// The seed's bytes are gone from the host, even where VirtualBox
		// would no longer describe the VM to empty its drive.
		if _, ok := model.Files[defaultHome+`\win-lab\seed.iso`]; ok {
			t.Errorf("%s: the seed outlived the failed clone", why)
		}
		if vm := model.VM("win-lab"); vm != nil && vm.State == vboxmanage.StateRunning {
			t.Errorf("%s: the failed clone is left running", why)
		}
	}
}

// TestSettle_AGuestThatIgnoresItsPowerButton: a proven clone whose guest
// does not shut down when asked has its power cut instead, and the clone
// is still made; a cut that fails, a failed read and a failed autostart
// clear each fail it and throw it away.
func TestSettle_AGuestThatIgnoresItsPowerButton(t *testing.T) {
	old := settleTimeout
	settleTimeout = time.Millisecond
	t.Cleanup(func() { settleTimeout = old })
	for why, tt := range map[string]struct {
		fail map[string]vboxmanage.Output
		skip map[string]int
		want string
	}{
		"its power is cut":    {nil, nil, ""},
		"the cut is refused":  {map[string]vboxmanage.Output{"VBoxManage.exe controlvm win-lab poweroff": failedRun}, nil, "refused"},
		"the autostart stays": {map[string]vboxmanage.Output{"VBoxManage.exe modifyvm win-lab --autostart-enabled": failedRun}, nil, "refused"},
	} {
		model := vboxmanagetest.New(windowsBase())
		model.Fail, model.Skip = tt.fail, tt.skip
		onModel(t, model)
		fixNow(t)
		guest(t, func(string, string) (string, error) {
			model.VM("win-lab").PowerButtonReads = -1
			return "WIN-LAB", nil
		})
		_, err := call(t, "virt.vbox.vm.clone", false, administrator(), winCloneParams())
		switch {
		case tt.want == "" && err != nil:
			t.Errorf("%s: %v", why, err)
		case tt.want != "" && (err == nil || !strings.Contains(err.Error(), tt.want)):
			t.Errorf("%s: %v, want an error mentioning %q", why, err, tt.want)
		}
		if seedLeft(model, defaultHome+`\win-lab`) {
			t.Errorf("%s: the seed is still on the host", why)
		}
	}
}

// TestDiscardClone_Edges: a lock the power off leaves for a moment is
// waited out; a failed power off is said; a seed whose path was never
// known is said not to be deleted; and dropping a seed fails on a drive
// that cannot be emptied or an image that cannot be closed.
func TestDiscardClone_Edges(t *testing.T) {
	ctx := context.Background()
	cause := context.DeadlineExceeded
	seeded := func(model *vboxmanagetest.Host) (vboxmanage.Host, string) {
		t.Helper()
		onModel(t, model)
		fixNow(t)
		if _, err := call(t, "virt.vbox.vm.clone", false, seeded(), cloneParams()); err != nil {
			t.Fatal(err)
		}
		return model.VBoxHost(), defaultHome + `\ubuntu-lab\seed.iso`
	}

	model := vboxmanagetest.New(base())
	h, seed := seeded(model)
	model.Fail = map[string]vboxmanage.Output{"VBoxManage.exe unregistervm": locked("ubuntu-lab")}
	model.Times = map[string]int{"VBoxManage.exe unregistervm": 1}
	if err := discardClone(ctx, h, "ubuntu-lab", seed, cause); err == nil || !strings.Contains(err.Error(), "was deleted") || model.VM("ubuntu-lab") != nil {
		t.Errorf("a moment's lock: %v", err)
	}

	model = vboxmanagetest.New(base())
	h, seed = seeded(model)
	model.VM("ubuntu-lab").State = vboxmanage.StateRunning
	model.Fail = map[string]vboxmanage.Output{"VBoxManage.exe controlvm ubuntu-lab poweroff": failedRun, "VBoxManage.exe unregistervm": failedRun}
	err := discardClone(ctx, h, "ubuntu-lab", "", cause)
	if err == nil || !strings.Contains(err.Error(), "powering it off failed") || !strings.Contains(err.Error(), "failed too") {
		t.Errorf("a failed power off and delete with no seed path: %v", err)
	}
	_ = seed

	for why, fail := range map[string]string{
		"a drive that cannot be emptied": "VBoxManage.exe storageattach ubuntu-lab",
		"an image that cannot be closed": "VBoxManage.exe closemedium dvd",
	} {
		model = vboxmanagetest.New(base())
		h, seed = seeded(model)
		model.Fail = map[string]vboxmanage.Output{fail: failedRun}
		if err := dropSeed(ctx, h, "ubuntu-lab", seed); err == nil {
			t.Errorf("%s: the seed was dropped", why)
		}
	}
	// A VM VirtualBox no longer knows: the file is deleted all the same.
	model = vboxmanagetest.New()
	model.SetFile(`C:\x\seed.iso`, []byte("seed"))
	if err := dropSeed(ctx, model.VBoxHost(), "gone", `C:\x\seed.iso`); err != nil || model.Files[`C:\x\seed.iso`] != nil {
		t.Errorf("a seed of a VM that is gone: %v", err)
	}
}

// locked is VirtualBox's refusal while a session holds name.
func locked(name string) vboxmanage.Output {
	return vboxmanage.Output{ExitCode: 1, Stderr: "VBoxManage.exe: error: The machine '" + name + "' is already locked for a session (or being unlocked)\r\n"}
}

// TestProveGuest_NothingListening: the real login, against an address
// where nothing answers WinRM, is an error the proof waits through rather
// than an answer.
func TestProveGuest_NothingListening(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if name, err := proveGuest(ctx, "127.0.0.1", "Lab-Passw0rd-1"); err == nil {
		t.Errorf("a login where nothing listens answered %q", name)
	}
}

// TestProof_EndsWithItsContext: a cancelled run stops waiting for the
// login and for a lock, rather than running to the timeout; and a clone
// that cannot be read after its shutdown is not left half settled.
func TestProof_EndsWithItsContext(t *testing.T) {
	model := vboxmanagetest.New(windowsBase())
	onModel(t, model)
	guest(t, ignoresTheSeed)
	h := model.VBoxHost()
	if err := h.CloneLinked(context.Background(), "ws2025-base", winSnap, "win-lab", ""); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c := cloneRequest{proveTimeout: time.Hour, hostname: "win-lab"}
	c.address = netip.MustParsePrefix("192.168.56.30/24")
	if err := proveWindowsClone(ctx, h, "win-lab", c, seedLogin{password: labPass}); err != context.Canceled {
		t.Errorf("a cancelled proof: %v", err)
	}
	model.VM("win-lab").State = vboxmanage.StatePoweroff
	model.Fail = map[string]vboxmanage.Output{"VBoxManage.exe unregistervm": locked("win-lab")}
	if err := deleteWhenUnlocked(ctx, h, "win-lab"); err != context.Canceled {
		t.Errorf("a cancelled wait for a lock: %v", err)
	}
	model.Fail = map[string]vboxmanage.Output{"VBoxManage.exe showvminfo win-lab": failedRun}
	if err := settleWindowsClone(context.Background(), h, "win-lab", defaultHome+`\win-lab\seed.iso`); err == nil {
		t.Error("a clone that cannot be read was settled")
	}
}

// TestSeedClone_StepsFailing: each step that gives a fresh clone its
// hardware and seed failing fails the clone, which is deleted with
// whatever seed it had.
func TestSeedClone_StepsFailing(t *testing.T) {
	for why, tt := range map[string]struct {
		fail string
		skip int
	}{
		"the hardware is refused":  {"VBoxManage.exe modifyvm ubuntu-lab", 0},
		"the extradata is refused": {"VBoxManage.exe setextradata ubuntu-lab", 0},
		"the second read fails":    {"VBoxManage.exe showvminfo ubuntu-lab", 2},
	} {
		model := vboxmanagetest.New(base())
		model.Fail = map[string]vboxmanage.Output{tt.fail: failedRun}
		model.Skip = map[string]int{tt.fail: tt.skip}
		onModel(t, model)
		fixNow(t)
		if _, err := call(t, "virt.vbox.vm.clone", false, seeded(), cloneParams()); err == nil || !strings.Contains(err.Error(), "refused") {
			t.Errorf("%s: %v", why, err)
		}
		if _, ok := model.Files[defaultHome+`\ubuntu-lab\seed.iso`]; ok {
			t.Errorf("%s: the seed is left on the host", why)
		}
	}
}

// Tests for virt.vbox.vm.install's freebsd installer against the model
// host: the screen it watches, the keys it types, the guest's word on the
// serial port that it ran them, and what it refuses.
//
// The file is named freebsd_install_test.go, never install_freebsd_test.go:
// Go reads a name ending _freebsd_test.go as a build constraint, and the
// tests would then run only on FreeBSD (FAILURE_PATTERNS 374).
package vm

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/bsdinstall"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/vboxmanage"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/vboxmanage/vboxmanagetest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

const (
	freebsdISO = `G:\iso\FreeBSD-15.1-RELEASE-amd64-dvd1.iso`
	// freebsdConsole is where the install VM's serial console goes: the
	// model's default machine folder, since the test host names none.
	freebsdConsole = `C:\Users\pleiades-gate\VirtualBox VMs\freebsd-base\` + consoleFile
)

// solidScreen is a 720 by 400 screenshot of one color, as VirtualBox
// saves a text-mode screen.
func solidScreen(t *testing.T, c color.RGBA) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 720, 400))
	for y := 0; y < 400; y++ {
		for x := 0; x < 720; x++ {
			img.Set(x, y, c)
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// The screens the installer is told apart by: the installer's blue, and
// the loader's and the kernel's black.
func installerScreen(t *testing.T) []byte { return solidScreen(t, color.RGBA{0, 0, 168, 255}) }
func bootScreen(t *testing.T) []byte      { return solidScreen(t, color.RGBA{0, 0, 0, 255}) }

// answersTheCommand is a guest that, typed the script's command, says so
// on its serial port as the command's echo does.
func answersTheCommand(typed string) []byte {
	if strings.Contains(typed, "bsdinstall script") {
		return []byte(bsdinstall.Started + "\r\n")
	}
	return nil
}

// freebsdParams asks for a small FreeBSD base on a 16 GB disk.
func freebsdParams() map[string]any {
	return map[string]any{"name": "freebsd-base", "installer": "freebsd", "iso": freebsdISO, "os_type": "FreeBSD_64", "size": "small", "disk_gb": 16}
}

// freebsdModel is a host holding the DVD, with another VM running so a
// start goes straight through, whose install VM shows screen, answers the
// typed command as guest does (nil for a guest that never does), and
// powers itself off after reads reads.
func freebsdModel(t *testing.T, reads int, screen []byte, guest func(string) []byte) *vboxmanagetest.Host {
	t.Helper()
	model := vboxmanagetest.New(running())
	model.SetFile(freebsdISO, []byte("a DVD"))
	model.GuestShutdownReads = map[string]int{"freebsd-base": reads}
	model.Screens = map[string][]byte{"freebsd-base": screen}
	if guest != nil {
		model.Guests = map[string]func(string) []byte{"freebsd-base": guest}
	}
	onModel(t, model)
	oldPoll, oldReady, oldStart, oldPause := installPoll, freebsdReadyTimeout, freebsdStartTimeout, freebsdKeyPause
	installPoll, freebsdReadyTimeout, freebsdStartTimeout, freebsdKeyPause = time.Millisecond, 50*time.Millisecond, 50*time.Millisecond, 0
	t.Cleanup(func() {
		installPoll, freebsdReadyTimeout, freebsdStartTimeout, freebsdKeyPause = oldPoll, oldReady, oldStart, oldPause
	})
	return model
}

func TestInstallFreeBSD(t *testing.T) {
	model := freebsdModel(t, 3, installerScreen(t), answersTheCommand)
	fixed := fixNow(t)
	if result, err := call(t, "virt.vbox.vm.install", true, newRecorder(), freebsdParams()); err != nil || !result.Changed || len(changes(model)) != 0 {
		t.Fatalf("check: %+v, %v, %v", result, err, changes(model))
	}
	rc := newRecorder()
	if result, err := call(t, "virt.vbox.vm.install", false, rc, freebsdParams()); err != nil || !result.Changed {
		t.Fatalf("%+v, %v", result, err)
	}
	vm := model.VM("freebsd-base")
	if vm == nil || vm.State != vboxmanage.StatePoweroff || vm.Autostart || vm.Firmware != "BIOS" || vm.MemoryMB != 2048 || vm.CPUs != 1 {
		t.Fatalf("installed %+v", vm)
	}
	if vm.ConsoleLog != freebsdConsole || vm.ConsoleLog != vm.Folder+`\`+consoleFile {
		t.Errorf("the serial console goes to %q, want the VM's own console.log", vm.ConsoleLog)
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
		t.Error("the script's DVD was left on the host")
	}
	if _, kept := model.Files[freebsdISO]; !kept {
		t.Error("the DVD was deleted")
	}

	// The Shell button, then the command, each typed once.
	shell, _ := vboxmanage.Keys(bsdinstall.ShellKeys)
	if len(vm.Typed) < 2 || vm.Typed[0] != strings.Join(shell[0].Scancodes, " ") {
		t.Errorf("the first key was not the Shell button's: %q", vm.Typed)
	}
	if n := strings.Count(strings.Join(vm.Typed, "\n"), "bsdinstall script /tmp/pleiades/"+bsdinstall.FileName); n != 1 {
		t.Errorf("the script's command was typed %d times: %q", n, vm.Typed)
	}
	calls := strings.Join(model.Calls(), "\n")
	if !strings.Contains(calls, "createmedium disk --filename "+vm.Folder+`\freebsd-base.vdi --size 16384`) {
		t.Errorf("no 16 GB disk:\n%s", calls)
	}
	if rc.stats[statUUID] != vm.UUID || rc.stats[statSize] != "small" {
		t.Errorf("stats %v", rc.stats)
	}
}

// TestInstallFreeBSD_TypesNothingBeforeTheInstallerIsUp: keys typed at the
// loader's menu would choose boot options, so with the screen still on the
// loader the install stops at its timeout having typed nothing, keeps the
// VM for its owner to see, and saves a picture of the screen.
func TestInstallFreeBSD_TypesNothingBeforeTheInstallerIsUp(t *testing.T) {
	model := freebsdModel(t, 1<<30, bootScreen(t), answersTheCommand)
	_, err := call(t, "virt.vbox.vm.install", false, newRecorder(), freebsdParams())
	if err == nil || !strings.Contains(err.Error(), "did not show the FreeBSD installer") || !strings.Contains(err.Error(), screenshotFile) {
		t.Fatalf("a boot that never reached the installer: %v", err)
	}
	vm := model.VM("freebsd-base")
	if vm == nil || vm.State != vboxmanage.StateRunning {
		t.Fatalf("the VM was not left running for its owner: %+v", vm)
	}
	if len(vm.Typed) != 0 {
		t.Errorf("keys were typed at the loader: %q", vm.Typed)
	}
}

// TestInstallFreeBSD_AGuestThatNeverAnswers: keys typed that the guest
// never acts on (it said nothing on its serial port) are a failure with a
// picture, not an install left to time out an hour later.
func TestInstallFreeBSD_AGuestThatNeverAnswers(t *testing.T) {
	freebsdModel(t, 1<<30, installerScreen(t), nil)
	_, err := call(t, "virt.vbox.vm.install", false, newRecorder(), freebsdParams())
	if err == nil || !strings.Contains(err.Error(), "sign that the install script started") || !strings.Contains(err.Error(), screenshotFile) {
		t.Fatalf("a guest that never ran the command: %v", err)
	}
}

// TestInstallFreeBSD_AVMThatStopsIsNotWaitedFor: a VM that stops before
// the installer comes up is reported at once, pointing at its log.
func TestInstallFreeBSD_AVMThatStopsIsNotWaitedFor(t *testing.T) {
	model := freebsdModel(t, -1, bootScreen(t), answersTheCommand)
	// Aborted from the first read on.
	_, err := call(t, "virt.vbox.vm.install", false, newRecorder(), freebsdParams())
	if err == nil || !strings.Contains(err.Error(), "aborted before its installer came up") || !strings.Contains(err.Error(), "VBox.log") {
		t.Fatalf("a VM that stopped: %v", err)
	}
	if vm := model.VM("freebsd-base"); len(vm.Typed) != 0 {
		t.Errorf("keys were typed at a stopped VM: %q", vm.Typed)
	}
}

// TestInstallFreeBSD_ResumingNeverTypesTwice: a run that stops waiting
// after the script started is resumed by running the task again, which
// sees the guest's word on the console and waits for the power-off
// without typing into the install under way.
func TestInstallFreeBSD_ResumingNeverTypesTwice(t *testing.T) {
	const key = "VBoxManage.exe showvminfo freebsd-base"
	model := freebsdModel(t, 1<<30, installerScreen(t), answersTheCommand)
	model.Fail = map[string]vboxmanage.Output{key: refused}
	model.Skip = map[string]int{key: 12}
	if _, err := call(t, "virt.vbox.vm.install", false, newRecorder(), freebsdParams()); err == nil {
		t.Fatal("the first run's failed read did not stop it")
	}
	typed := len(model.VM("freebsd-base").Typed)
	if !strings.Contains(strings.Join(model.VM("freebsd-base").Typed, "\n"), "bsdinstall script") {
		t.Fatalf("the first run stopped before typing, so this tests nothing: %q", model.VM("freebsd-base").Typed)
	}
	model.Fail = nil
	model.GuestShutdownReads = map[string]int{"freebsd-base": 2}
	if result, err := call(t, "virt.vbox.vm.install", false, newRecorder(), freebsdParams()); err != nil || !result.Changed {
		t.Fatalf("the second run: %+v, %v", result, err)
	}
	vm := model.VM("freebsd-base")
	if len(vm.Typed) != typed {
		t.Errorf("the resumed run typed again: %q", vm.Typed[typed:])
	}
	if vm.State != vboxmanage.StatePoweroff || vm.Extra[vboxmanage.ExtraInstalled] == "" {
		t.Errorf("resumed to %+v", vm)
	}
}

func TestInstall_RefusesWhatItsInstallerDoesNotTake(t *testing.T) {
	freebsdModel(t, 3, installerScreen(t), answersTheCommand)
	for name, tc := range map[string]struct {
		change func(map[string]any)
		want   string
	}{
		"no installer":             {func(p map[string]any) { delete(p, "installer") }, "installer is required"},
		"an unknown installer":     {func(p map[string]any) { p["installer"] = "openbsd" }, `installer "openbsd" is not one of`},
		"freebsd given an image":   {func(p map[string]any) { p["image"] = "1" }, "image is for the windows installer"},
		"freebsd given a language": {func(p map[string]any) { p["language"] = "en-US" }, "language is for the windows installer"},
		"freebsd on a tiny disk":   {func(p map[string]any) { p["disk_gb"] = 4 }, "FreeBSD needs at least 8 GB"},
		"windows with no image": {func(p map[string]any) {
			p["installer"] = "windows"
			p["disk_gb"] = 64
		}, "image"},
		"windows on a FreeBSD-sized disk": {func(p map[string]any) {
			p["installer"] = "windows"
			p["image"] = "1"
		}, "Windows Server needs at least 32 GB"},
	} {
		params := freebsdParams()
		tc.change(params)
		if _, err := call(t, "virt.vbox.vm.install", false, newRecorder(), params); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want an error mentioning %q", name, err, tc.want)
		}
	}
}

// TestCloneFreeBSD_SeedsAShellTheGuestHas clones a FreeBSD base with a
// login other than root: its NoCloud seed, which nuageinit reads, must
// give the login /bin/sh, since the base system has no bash and a login
// whose shell does not exist cannot log in.
func TestCloneFreeBSD_SeedsAShellTheGuestHas(t *testing.T) {
	fbsd := base()
	fbsd.Name, fbsd.UUID, fbsd.Folder, fbsd.OSType = "freebsd-base", "3175ab2f-44ec-4e33-aec8-d9f46f6eaba5", `G:\PleiadesLab\freebsd-base`, "FreeBSD (64-bit)"
	model := vboxmanagetest.New(fbsd)
	onModel(t, model)
	fixNow(t)
	rc := seeded()
	rc.secrets[wire.SecretSeedUsername] = "pleiades"
	params := map[string]any{"name": "bsd-lab", "from": "freebsd-base", "snapshot": "base", "login": "bsd-lab", "address": "192.168.56.40/24"}
	if result, err := call(t, "virt.vbox.vm.clone", false, rc, params); err != nil || !result.Changed {
		t.Fatalf("%+v, %v", result, err)
	}
	vm := model.VM("bsd-lab")
	seed := model.Files[vm.Folder+`\`+seedFile]
	if !bytes.Contains(seed, []byte("shell: /bin/sh\n")) || bytes.Contains(seed, []byte("/bin/bash")) {
		t.Errorf("the FreeBSD clone's seed does not give its login /bin/sh:\n%s", seed)
	}
	if vm.ConsoleLog != vm.Folder+`\`+consoleFile {
		t.Errorf("the clone's console goes to %q, where virt.vbox.vm.host_keys would not read it", vm.ConsoleLog)
	}
}

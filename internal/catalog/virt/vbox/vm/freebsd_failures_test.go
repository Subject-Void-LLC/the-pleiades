// Tests for each step the freebsd installer adds to an install failing on
// the model host: what the task says, what it typed, and what it leaves.
// (Named with freebsd first for the reason freebsd_install_test.go gives:
// FAILURE_PATTERNS 374.)
package vm

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/vboxmanage"
)

// The calls the freebsd installer makes that a case below fails.
const (
	freebsdVM         = "freebsd-base"
	freebsdRead       = "VBoxManage.exe showvminfo " + freebsdVM
	freebsdScreenshot = "VBoxManage.exe controlvm " + freebsdVM + " screenshotpng"
	freebsdKeys       = "VBoxManage.exe controlvm " + freebsdVM + " keyboardputscancode"
)

// denied is what a failing PowerShell read of a host file answers.
var denied = vboxmanage.Output{ExitCode: 1, Stderr: "Access denied\r\n"}

// TestInstallFreeBSDStepsFailing fails each host call the freebsd
// installer adds, one at a time. A failure before the VM starts deletes
// it; any later one leaves it for its owner, and one before the installer
// is up has typed nothing. Every one reports what the host said.
func TestInstallFreeBSDStepsFailing(t *testing.T) {
	// A clean install first, to count the VM reads that come before a
	// given call, so a case fails the next read wherever the install's own
	// steps put it. Typing makes no reads, so the first read after the
	// keys is the console watch's.
	clean := freebsdModel(t, 3, installerScreen(t), answersTheCommand)
	if _, err := call(t, "virt.vbox.vm.install", false, newRecorder(), freebsdParams()); err != nil {
		t.Fatalf("the clean install every case departs from: %v", err)
	}
	readsBefore := func(mark string) int {
		n := 0
		for _, c := range clean.Calls() {
			if strings.HasPrefix(c, mark) {
				return n
			}
			if strings.HasPrefix(c, freebsdRead) {
				n++
			}
		}
		t.Fatalf("a clean install made no call starting %q", mark)
		return 0
	}
	probe := defaultHome + `\` + freebsdVM + `\` + screenProbe
	notAPicture := vboxmanage.Output{Stdout: base64.StdEncoding.EncodeToString([]byte("not a picture")) + "\r\n"}

	for name, tt := range map[string]struct {
		key         string
		skip        int
		out         vboxmanage.Output
		want        string
		kept, typed bool
	}{
		"pointing the serial console at a file":       {key: "VBoxManage.exe modifyvm " + freebsdVM + " --uart1", out: refused, want: "refused"},
		"reading the console before typing":           {key: "powershell read " + freebsdConsole, out: denied, want: "Access denied", kept: true},
		"reading the VM while the installer comes up": {key: freebsdRead, skip: readsBefore(freebsdScreenshot) - 1, out: refused, want: "refused", kept: true},
		"a picture of the screen":                     {key: freebsdScreenshot, out: refused, want: "refused", kept: true},
		"reading the picture back":                    {key: "powershell read " + probe, out: denied, want: "Access denied", kept: true},
		"a picture that is not one":                   {key: "powershell read " + probe, out: notAPicture, want: "not a PNG", kept: true},
		"typing":                                      {key: freebsdKeys, out: refused, want: "refused", kept: true},
		"reading the VM while the script starts":      {key: freebsdRead, skip: readsBefore(freebsdKeys), out: refused, want: "refused", kept: true, typed: true},
	} {
		t.Run(name, func(t *testing.T) {
			// A guest that never answers, so the console watch after the
			// keys reads the VM rather than finding the guest's word.
			model := freebsdModel(t, 1<<30, installerScreen(t), nil)
			model.Fail = map[string]vboxmanage.Output{tt.key: tt.out}
			model.Skip = map[string]int{tt.key: tt.skip}
			_, err := call(t, "virt.vbox.vm.install", false, newRecorder(), freebsdParams())
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("%v, want an error mentioning %q", err, tt.want)
			}
			vm := model.VM(freebsdVM)
			if (vm != nil) != tt.kept {
				t.Fatalf("the VM kept is %v, want %v", vm != nil, tt.kept)
			}
			if vm != nil && (len(vm.Typed) > 0) != tt.typed {
				t.Errorf("typed %q, want keys typed %v", vm.Typed, tt.typed)
			}
		})
	}
}

// TestInstallFreeBSD_AResumeThatCannotReadTheConsoleTypesNothing: a run
// again after one that stopped reads the console first, to learn whether
// the script started, and a console it cannot read stops that run rather
// than typing into an install that may be under way.
func TestInstallFreeBSD_AResumeThatCannotReadTheConsoleTypesNothing(t *testing.T) {
	model := freebsdModel(t, 1<<30, installerScreen(t), answersTheCommand)
	model.Fail = map[string]vboxmanage.Output{freebsdScreenshot: refused}
	if _, err := call(t, "virt.vbox.vm.install", false, newRecorder(), freebsdParams()); err == nil {
		t.Fatal("the first run's refused screenshot did not stop it")
	}
	model.Fail = map[string]vboxmanage.Output{"powershell read " + freebsdConsole: denied}
	_, err := call(t, "virt.vbox.vm.install", false, newRecorder(), freebsdParams())
	if err == nil || !strings.Contains(err.Error(), "Access denied") {
		t.Fatalf("resuming with the console unreadable: %v", err)
	}
	if vm := model.VM(freebsdVM); vm == nil || len(vm.Typed) != 0 {
		t.Errorf("the resumed install was not left untouched: %+v", vm)
	}
}

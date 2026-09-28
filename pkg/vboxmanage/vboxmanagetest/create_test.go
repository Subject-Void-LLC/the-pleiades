// Tests for the model's machines made from nothing (create.go): what it
// answers, what it refuses, and a guest that powers itself off.
package vboxmanagetest

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/vboxmanage"
)

const image = `G:\iso\server.vhdx`

func TestCreateVerbs(t *testing.T) {
	h := New()
	h.Files[image] = make([]byte, 64)
	if out := run(t, h, "createvm", "--name", "win", "--ostype", "Windows2025_64", "--register"); out.ExitCode != 0 || !strings.Contains(out.Stdout, "is created and registered") {
		t.Fatalf("createvm: %+v", out)
	}
	if vm := h.VM("win"); vm.Folder != defaultFolder+`\win` || vm.OSType != "Windows Server 2025 (64-bit)" {
		t.Errorf("made %+v", vm)
	}
	if out := run(t, h, "storagectl", "win", "--name", "SATA", "--add", "sata", "--controller", "IntelAhci", "--portcount", "2"); out.ExitCode != 0 || len(h.VM("win").Slots) != 2 {
		t.Errorf("a two-port SATA controller: %+v, %+v", out, h.VM("win").Slots)
	}
	disk := `G:\PleiadesLab\win\win.vdi`
	if out := run(t, h, "clonemedium", "disk", image, disk, "--format", "VDI"); out.ExitCode != 0 {
		t.Fatalf("clonemedium: %+v", out)
	}
	if out := run(t, h, "storageattach", "win", "--storagectl", "SATA", "--port", "0", "--device", "0", "--type", "hdd", "--medium", disk); out.ExitCode != 0 {
		t.Fatalf("attach: %+v", out)
	}
	if out := run(t, h, "list", "hdds"); !strings.Contains(out.Stdout, "Location:       "+disk+"\r\n") {
		t.Errorf("list hdds: %q", out.Stdout)
	}
	if out := run(t, h, "closemedium", "disk", disk); out.ExitCode == 0 {
		t.Error("closed a disk a machine holds")
	}
	if out := run(t, h, "closemedium", "disk", image, "--delete"); out.ExitCode != 0 || h.Files[image] != nil {
		t.Errorf("close and delete: %+v", out)
	}

	for name, tt := range map[string]struct {
		args []string
		want string
	}{
		"createvm with a stray argument":  {[]string{"createvm", "stray"}, "unexpected"},
		"createvm unregistered":           {[]string{"createvm", "--name", "x", "--ostype", "Other_64"}, "registered machines"},
		"createvm of an unknown type":     {[]string{"createvm", "--name", "x", "--ostype", "Plan9", "--register"}, "is invalid"},
		"createvm over a machine":         {[]string{"createvm", "--name", "win", "--ostype", "Other_64", "--register"}, "already exists"},
		"storagectl of no machine":        {[]string{"storagectl", "none", "--name", "IDE", "--add", "ide"}, "Could not find"},
		"storagectl with a stray flag":    {[]string{"storagectl", "win", "stray"}, "unexpected"},
		"a second controller of the name": {[]string{"storagectl", "win", "--name", "SATA", "--add", "sata"}, "already exists"},
		"a SCSI controller":               {[]string{"storagectl", "win", "--name", "SCSI", "--add", "scsi"}, "sata and ide"},
		"createmedium of a DVD":           {[]string{"createmedium", "dvd", "--filename", `G:\a.iso`}, "disks only"},
		"createmedium with a stray flag":  {[]string{"createmedium", "disk", "stray"}, "unexpected"},
		"createmedium over a file":        {[]string{"createmedium", "disk", "--filename", disk, "--size", "1"}, "file exists"},
		"clonemedium of a DVD":            {[]string{"clonemedium", "dvd", "a", "b"}, "disks only"},
		"clonemedium of nothing":          {[]string{"clonemedium", "disk", `G:\missing.vhdx`, `G:\b.vdi`}, "Could not find file"},
		"clonemedium over a disk":         {[]string{"clonemedium", "disk", disk, disk}, "already exists"},
		"mediumio of no disk":             {[]string{"mediumio", `--disk=G:\missing.vhdx`, "cat", "--hex"}, "a disk it holds"},
		"mediumio without hex":            {[]string{"mediumio", "--disk=" + disk, "cat"}, "a disk it holds"},
		"a screenshot of no machine":      {[]string{"controlvm", "none", "screenshotpng", `G:\s.png`}, "Could not find"},
		"a screenshot of a machine off":   {[]string{"controlvm", "win", "screenshotpng", `G:\s.png`}, "not currently running"},
	} {
		if out := run(t, h, tt.args...); out.ExitCode == 0 || !strings.Contains(out.Stderr, tt.want) {
			t.Errorf("%s: %+v, want a refusal mentioning %q", name, out, tt.want)
		}
	}
}

func TestCreatemediumAndIDE(t *testing.T) {
	h := New()
	run(t, h, "createvm", "--name", "win", "--ostype", "Other_64", "--register", "--basefolder", `G:\PleiadesLab`)
	if out := run(t, h, "storagectl", "win", "--name", "IDE", "--add", "ide", "--controller", "PIIX4"); out.ExitCode != 0 || len(h.VM("win").Slots) != 4 {
		t.Errorf("an IDE controller: %+v, %+v", out, h.VM("win").Slots)
	}
	if out := run(t, h, "createmedium", "disk", "--filename", `G:\PleiadesLab\win\win.vdi`, "--size", "65536"); out.ExitCode != 0 || h.Files[`G:\PleiadesLab\win\win.vdi`] == nil {
		t.Errorf("createmedium: %+v", out)
	}
}

func TestAGuestThatPowersItselfOff(t *testing.T) {
	h := New(&VM{Name: "installer", UUID: "00000000-0000-4000-8000-000000000001", State: vboxmanage.StateRunning},
		&VM{Name: "crasher", UUID: "00000000-0000-4000-8000-000000000002", State: vboxmanage.StateRunning})
	h.GuestShutdownReads = map[string]int{"installer": 1, "crasher": -1}
	state := func(name string) string {
		out := run(t, h, "showvminfo", name, "--machinereadable")
		for _, line := range strings.Split(out.Stdout, "\r\n") {
			if v, ok := strings.CutPrefix(line, "VMState="); ok {
				return strings.Trim(v, `"`)
			}
		}
		return ""
	}
	if got := state("installer"); got != vboxmanage.StateRunning {
		t.Errorf("first read: %s", got)
	}
	if got := state("installer"); got != vboxmanage.StatePoweroff {
		t.Errorf("second read: %s", got)
	}
	if got := state("crasher"); got != vboxmanage.StateAborted {
		t.Errorf("a guest that crashes: %s", got)
	}
	if got := state("installer"); got != vboxmanage.StatePoweroff {
		t.Errorf("a machine off stays off: %s", got)
	}
}

func TestMediumioDumpsAsTheHostDid(t *testing.T) {
	want, err := os.ReadFile("../testdata/mediumio-cat-gpt.stdout")
	if err != nil {
		t.Fatal(err)
	}
	disk, err := vboxmanage.ParseHexDump(string(want))
	if err != nil {
		t.Fatal(err)
	}
	h := New()
	h.SetFile(image, disk)
	if out := run(t, h, "mediumio", "--disk="+image, "cat", "--hex", "--offset=0", "--size=1024"); out.Stdout != string(want) {
		t.Errorf("the model wrote\n%s\nthe host wrote\n%s", out.Stdout, want)
	}
	// A dump past the disk's end stops at it.
	if out := run(t, h, "mediumio", "--disk="+image, "cat", "--hex", "--offset=1008", "--size=64"); strings.Count(out.Stdout, "\r\n") != 1 {
		t.Errorf("a dump past the end: %q", out.Stdout)
	}
	if out := run(t, h, "list", "hdds"); !strings.Contains(out.Stdout, image) {
		t.Error("a disk mediumio read was not registered")
	}
}

func TestScreenshotOfARunningMachine(t *testing.T) {
	h := New(&VM{Name: "win", UUID: "00000000-0000-4000-8000-000000000003", State: vboxmanage.StateRunning})
	if out := run(t, h, "controlvm", "win", "screenshotpng", `G:\s.png`); out.ExitCode != 0 || !strings.HasPrefix(string(h.Files[`G:\s.png`]), "\x89PNG") {
		t.Errorf("screenshot: %+v", out)
	}
}

func TestAFailureThatLetsGo(t *testing.T) {
	h := New(&VM{Name: "win", UUID: "00000000-0000-4000-8000-000000000004", State: vboxmanage.StatePoweroff})
	h.Fail = map[string]vboxmanage.Output{"VBoxManage.exe showvminfo win": {ExitCode: 1, Stderr: "VBoxManage.exe: error: locked\r\n"}}
	h.Times = map[string]int{"VBoxManage.exe showvminfo win": 2}
	for i, want := range []int{1, 1, 0, 0} {
		if out := run(t, h, "showvminfo", "win", "--machinereadable"); out.ExitCode != want {
			t.Errorf("read %d exited %d, want %d", i+1, out.ExitCode, want)
		}
	}
}

func TestLeasesAndTyping(t *testing.T) {
	ctx := context.Background()
	h := New(&VM{Name: "win", UUID: "00000000-0000-4000-8000-000000000005", State: vboxmanage.StateRunning},
		&VM{Name: "off", UUID: "00000000-0000-4000-8000-000000000006", State: vboxmanage.StatePoweroff})
	script := "# vboxmanage: leases\n$adapter = 'VirtualBox Host-Only Ethernet Adapter'\n"
	if out, err := h.PowerShell(ctx, script, ""); err != nil || out.ExitCode != 3 {
		t.Errorf("no leases file: %+v, %v", out, err)
	}
	h.Leases = map[string]string{"VirtualBox Host-Only Ethernet Adapter": "<Leases/>"}
	if out, _ := h.PowerShell(ctx, script, ""); out.ExitCode != 0 || strings.TrimSpace(out.Stdout) != "PExlYXNlcy8+" {
		t.Errorf("a leases file: %+v", out)
	}
	if out := run(t, h, "controlvm", "win", "keyboardputstring", "hello"); out.ExitCode != 0 || h.VM("win").Typed[0] != "hello" {
		t.Errorf("typing: %+v, %q", out, h.VM("win").Typed)
	}
	for name, args := range map[string][]string{
		"typing into no machine":      {"controlvm", "none", "keyboardputscancode", "1c", "9c"},
		"typing into one that is off": {"controlvm", "off", "keyboardputscancode", "1c", "9c"},
	} {
		if out := run(t, h, args...); out.ExitCode == 0 {
			t.Errorf("%s: answered", name)
		}
	}
}

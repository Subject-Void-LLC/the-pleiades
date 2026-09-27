// Tests for the model's answers to raw VBoxManage arguments and scripts,
// refusals included: the contract pkg/vboxmanage's calls are held to.
package vboxmanagetest

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/vboxmanage"
)

// run answers args as VBoxManage, failing the test on a Go error.
func run(t *testing.T, h *Host, args ...string) vboxmanage.Output {
	t.Helper()
	out, err := h.Run(context.Background(), Path, args)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestVerbs(t *testing.T) {
	h := New()
	h.Appliances[ova] = ubuntuAppliance()
	if out := run(t, h, "import", ova, "--vsys", "0", "--vmname", "base"); out.ExitCode != 0 || h.VM("base").Folder != `C:\Users\pleiades-gate\VirtualBox VMs\base` {
		t.Fatalf("import to the default folder: %+v", out)
	}
	if out := run(t, h, "import", ova, "--vsys"); out.ExitCode == 0 {
		t.Error("a flag with no value was accepted")
	}
	if out := run(t, h, "import", ova, "stray"); out.ExitCode == 0 {
		t.Error("an argument that is no flag was accepted")
	}
	snap := strings.TrimSpace(strings.TrimPrefix(run(t, h, "snapshot", "base", "take", "clean").Stdout, "Snapshot taken. UUID: "))

	for name, tt := range map[string]struct {
		args []string
		want string
	}{
		"clone a machine that is not there": {[]string{"clonevm", "nothing", "--snapshot", snap, "--options", "link", "--name", "x", "--register"}, "Could not find a registered machine"},
		"a full clone":                      {[]string{"clonevm", "base", "--snapshot", snap, "--name", "x", "--register"}, "linked clones only"},
		"clone a stray flag":                {[]string{"clonevm", "base", "stray"}, "unexpected"},
		"modify a stray flag":               {[]string{"modifyvm", "base", "stray"}, "unexpected"},
		"modify an unknown setting":         {[]string{"modifyvm", "base", "--vram", "16"}, "does not answer"},
		"modify a machine not there":        {[]string{"modifyvm", "nothing", "--cpus", "1"}, "Could not find"},
		"attach to a machine not there":     {[]string{"storageattach", "nothing", "--storagectl", "IDE", "--port", "0", "--device", "0", "--medium", "none"}, "Could not find"},
		"attach to no such slot":            {[]string{"storageattach", "base", "--storagectl", "SATA", "--port", "0", "--device", "0", "--medium", "none"}, "No storage device"},
		"attach a stray flag":               {[]string{"storageattach", "base", "stray"}, "unexpected"},
		"unregister a machine not there":    {[]string{"unregistervm", "nothing", "--delete"}, "Could not find"},
		"an unknown command":                {[]string{"export", "base"}, "does not answer"},
	} {
		if out := run(t, h, tt.args...); out.ExitCode == 0 || !strings.Contains(out.Stderr, tt.want) {
			t.Errorf("%s: %+v, want a refusal mentioning %q", name, out, tt.want)
		}
	}
	if out := run(t, h, "clonevm", "base", "--snapshot", snap, "--options", "link", "--name", "base", "--register"); out.ExitCode == 0 {
		t.Error("cloned over an existing machine")
	}
	if out := run(t, h, "clonevm", "base", "--snapshot", snap, "--options", "link", "--name", "lab", "--register"); out.ExitCode != 0 || h.VM("lab").LinkedFrom != "base" {
		t.Fatalf("a linked clone to the default folder: %+v", out)
	}
	run(t, h, "modifyvm", "lab", "--nic1", "nat", "--nic2", "hostonly", "--hostonlyadapter2", "VirtualBox Host-Only Ethernet Adapter", "--uart1", "0x3F8", "4", "--uart-mode1", "file", `G:\lab\console.log`, "--autostart-delay", "0")
	if lab := h.VM("lab"); lab.ConsoleLog != `G:\lab\console.log` || lab.NICs[2].HostOnlyAdapter == "" {
		t.Errorf("modified %+v", lab)
	}
	if out := run(t, h, "modifyvm", "lab", "--uart-mode1", "file"); out.ExitCode == 0 {
		t.Error("a two-value flag with one value was accepted")
	}
	h.VM("lab").State = vboxmanage.StateRunning
	for _, args := range [][]string{{"modifyvm", "lab", "--cpus", "2"}, {"unregistervm", "lab", "--delete"}} {
		if out := run(t, h, args...); out.ExitCode == 0 || !strings.Contains(out.Stderr, "already locked") {
			t.Errorf("%v on a running machine: %+v", args, out)
		}
	}
	h.VM("lab").State = vboxmanage.StatePoweroff
	h.Files[`G:\seed.iso`] = []byte("iso")
	run(t, h, "storageattach", "lab", "--storagectl", "IDE", "--port", "0", "--device", "0", "--type", "dvddrive", "--medium", `G:\seed.iso`)
	if out := run(t, h, "closemedium", "dvd", `G:\seed.iso`); out.ExitCode == 0 {
		t.Error("closed an image a machine holds")
	}
	run(t, h, "storageattach", "lab", "--storagectl", "IDE", "--port", "0", "--device", "0", "--medium", "none")
	if out := run(t, h, "closemedium", "dvd", `G:\seed.iso`); out.ExitCode != 0 || h.Files[`G:\seed.iso`] == nil {
		t.Errorf("closing without --delete removed the file, or failed: %+v", out)
	}
	if out := run(t, h, "unregistervm", "base", "--delete"); out.ExitCode == 0 || !strings.Contains(out.Stderr, "linked") {
		t.Errorf("deleting a machine a clone descends from: %+v", out)
	}
	if out := run(t, h, "unregistervm", "lab", "--delete"); out.ExitCode != 0 || h.VM("lab") != nil {
		t.Errorf("deleting the clone: %+v", out)
	}
	if out := run(t, h, "controlvm", "base", "reset"); out.ExitCode == 0 {
		t.Error("controlvm answered an action the model does not know")
	}
	h.VM("base").State = vboxmanage.StateRunning
	if out := run(t, h, "controlvm", "base", "reset"); out.ExitCode == 0 || !strings.Contains(out.Stderr, "does not answer") {
		t.Errorf("an unknown action on a running machine: %+v", out)
	}
	h.Account = `lab\someone`
	if out, _ := h.Run(context.Background(), `C:\Windows\System32\whoami.exe`, nil); out.Stdout != "lab\\someone\r\n" {
		t.Errorf("whoami = %q", out.Stdout)
	}
}

func TestPowerShellScripts(t *testing.T) {
	ctx := context.Background()
	h := New()
	upload := "# vboxmanage: upload\n$path = 'G:\\a.iso'\n$want = '2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824'\n"
	if out, _ := h.PowerShell(ctx, upload, "not base64!"); out.ExitCode == 0 {
		t.Error("stdin that is not base64 was written")
	}
	if out, _ := h.PowerShell(ctx, upload, base64.StdEncoding.EncodeToString([]byte("hello"))); out.ExitCode != 0 || string(h.Files[`G:\a.iso`]) != "hello" {
		t.Errorf("upload: %+v", out)
	}
	if out, _ := h.PowerShell(ctx, upload, base64.StdEncoding.EncodeToString([]byte("other"))); out.ExitCode != 21 {
		t.Errorf("a digest mismatch: %+v", out)
	}
	read := "# vboxmanage: read\n$path = 'G:\\a.iso'\n$max = 3\n"
	if out, _ := h.PowerShell(ctx, read, ""); strings.TrimSpace(out.Stdout) != base64.StdEncoding.EncodeToString([]byte("llo")) {
		t.Errorf("read: %+v", out)
	}
	if assignment("no assignments", "path") != "" {
		t.Error("an assignment was read from nothing")
	}
	h.Fail = map[string]vboxmanage.Output{"powershell read": {ExitCode: 5}}
	if out, _ := h.PowerShell(ctx, read, ""); out.ExitCode != 5 {
		t.Errorf("a scripted failure: %+v", out)
	}
}

func TestSkip(t *testing.T) {
	h := New(probe())
	h.Fail = map[string]vboxmanage.Output{"VBoxManage.exe showvminfo": {ExitCode: 1, Stderr: "later\r\n"}}
	h.Skip = map[string]int{"VBoxManage.exe showvminfo": 1}
	if _, err := h.VBoxHost().Machine(context.Background(), "pleiades-probe"); err != nil {
		t.Fatalf("the first read: %v", err)
	}
	if _, err := h.VBoxHost().Machine(context.Background(), "pleiades-probe"); err == nil {
		t.Error("the second read did not fail")
	}
}

func TestExtraData(t *testing.T) {
	ctx := context.Background()
	h := New(probe())
	host := h.VBoxHost()
	if err := host.SetExtraData(ctx, "pleiades-probe", "pleiades/address", "192.168.56.10"); err != nil {
		t.Fatal(err)
	}
	if err := host.SetExtraData(ctx, "pleiades-probe", "pleiades/device", "probe"); err != nil {
		t.Fatal(err)
	}
	data, err := host.ExtraData(ctx, "pleiades-probe")
	if err != nil || data["pleiades/address"] != "192.168.56.10" || data["pleiades/device"] != "probe" {
		t.Errorf("%v, %v", data, err)
	}
	if err := host.SetExtraData(ctx, "pleiades-probe", "pleiades/device", ""); err != nil {
		t.Fatal(err)
	}
	if data, _ := host.ExtraData(ctx, "pleiades-probe"); len(data) != 1 {
		t.Errorf("after deleting a key: %v", data)
	}
	for _, args := range [][]string{{"getextradata", "gone", "enumerate"}, {"setextradata", "gone", "k", "v"}} {
		if out := run(t, h, args...); out.ExitCode == 0 {
			t.Errorf("%v on a machine not there", args)
		}
	}
}

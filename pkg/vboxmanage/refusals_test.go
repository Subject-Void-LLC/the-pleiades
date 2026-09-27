// Tests for what the lifecycle verbs and file scripts refuse before
// anything reaches the host, and how each reports a host that fails.
package vboxmanage

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/winrmexec"
)

// never is a runner a refused call must not reach.
type never struct{ t *testing.T }

func (n never) Run(_ context.Context, program string, args []string) (Output, error) {
	n.t.Errorf("a refused call ran %s %v", program, args)
	return Output{}, nil
}

func (n never) PowerShell(_ context.Context, script, _ string) (Output, error) {
	n.t.Errorf("a refused call ran PowerShell %q", strings.SplitN(script, "\n", 2)[0])
	return Output{}, nil
}

func TestPathAndAdapterChecks(t *testing.T) {
	for _, good := range []string{`G:\PleiadesLab\seed.iso`, `C:\Users\pleiades-gate\VirtualBox VMs\lab\console.log`} {
		if err := CheckPath("file", good); err != nil {
			t.Errorf("%s: %v", good, err)
		}
	}
	for _, bad := range []string{"", `seed.iso`, `\\server\share\x`, `G:\a'b`, `G:\a"b`, `G:\*.iso`, `G:\lab\`, `G:\lab\..\Windows\x`, `G:\lab\..`, "G:\\a\nb", `G:\` + strings.Repeat("a", 240)} {
		if err := CheckPath("file", bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if err := CheckAdapter("VirtualBox Host-Only Ethernet Adapter #2"); err != nil {
		t.Error(err)
	}
	for _, bad := range []string{"", " lead", `a"b`, "a;b", "a\nb"} {
		if err := CheckAdapter(bad); err == nil {
			t.Errorf("adapter %q accepted", bad)
		}
	}
}

func TestRefusedBeforeTheHost(t *testing.T) {
	ctx := context.Background()
	h := Host{Runner: never{t}, Path: vbox}
	hw := Hardware{MemoryMB: 1024, CPUs: 1, HostOnlyAdapter: "VirtualBox Host-Only Ethernet Adapter", ConsoleLog: `G:\PleiadesLab\lab\console.log`}
	slot := Slot{Controller: "IDE", Port: 0, Device: 0}
	uuid := "5732a952-0b85-4e3a-820a-1a568f31166b"
	for name, err := range map[string]error{
		"import a bad file":        h.Import(ctx, `x.ova`, "lab", ""),
		"import a bad name":        h.Import(ctx, `G:\x.ova`, "-lab", ""),
		"import into a bad folder": h.Import(ctx, `G:\x.ova`, "lab", `G:\a*b`),
		"configure a bad name":     h.Configure(ctx, "a b", hw),
		"configure no memory":      h.Configure(ctx, "lab", Hardware{CPUs: 1, HostOnlyAdapter: hw.HostOnlyAdapter, ConsoleLog: hw.ConsoleLog}),
		"configure no CPU":         h.Configure(ctx, "lab", Hardware{MemoryMB: 64, HostOnlyAdapter: hw.HostOnlyAdapter, ConsoleLog: hw.ConsoleLog}),
		"configure a bad adapter":  h.Configure(ctx, "lab", Hardware{MemoryMB: 64, CPUs: 1, HostOnlyAdapter: `a"b`, ConsoleLog: hw.ConsoleLog}),
		"configure a bad log":      h.Configure(ctx, "lab", Hardware{MemoryMB: 64, CPUs: 1, HostOnlyAdapter: hw.HostOnlyAdapter, ConsoleLog: "log"}),
		"clone a bad source":       h.CloneLinked(ctx, "a b", uuid, "lab", ""),
		"clone to a bad name":      h.CloneLinked(ctx, "base", uuid, "a b", ""),
		"clone a bad snapshot":     h.CloneLinked(ctx, "base", "clean", "lab", ""),
		"clone into a bad folder":  h.CloneLinked(ctx, "base", uuid, "lab", "folder"),
		"attach to a bad name":     h.AttachDVD(ctx, "a b", slot, `G:\seed.iso`),
		"attach a bad image":       h.AttachDVD(ctx, "lab", slot, `seed.iso`),
		"attach a bad controller":  h.AttachDVD(ctx, "lab", Slot{Controller: `a"b`}, "emptydrive"),
		"detach from a bad name":   h.Detach(ctx, "a b", slot),
		"detach a bad controller":  h.Detach(ctx, "lab", Slot{Controller: ""}),
		"close a bad image":        h.CloseDVD(ctx, "seed.iso", true),
		"unregister a bad name":    h.Unregister(ctx, "a b"),
		"upload to a bad path":     h.Upload(ctx, "seed.iso", []byte("x")),
		"upload too much":          h.Upload(ctx, `G:\seed.iso`, make([]byte, maxUpload+1)),
		"remove a bad path":        h.Remove(ctx, "seed.iso", false),
	} {
		if err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := h.ReadTail(ctx, "log", 10); err == nil {
		t.Error("read a bad path")
	}
	if _, err := h.ReadTail(ctx, `G:\log`, 0); err == nil {
		t.Error("read with no limit")
	}
}

func TestHostFailuresReachTheCaller(t *testing.T) {
	ctx := context.Background()
	down := Host{Runner: failing{}, Path: vbox}
	hw := Hardware{MemoryMB: 1024, CPUs: 1, HostOnlyAdapter: "VirtualBox Host-Only Ethernet Adapter", ConsoleLog: `G:\PleiadesLab\lab\console.log`}
	slot := Slot{Controller: "IDE"}
	for name, err := range map[string]error{
		"import":     down.Import(ctx, `G:\x.ova`, "lab", `G:\PleiadesLab`),
		"configure":  down.Configure(ctx, "lab", hw),
		"clone":      down.CloneLinked(ctx, "base", "5732a952-0b85-4e3a-820a-1a568f31166b", "lab", `G:\PleiadesLab`),
		"attach":     down.AttachDVD(ctx, "lab", slot, "emptydrive"),
		"detach":     down.Detach(ctx, "lab", slot),
		"close":      down.CloseDVD(ctx, `G:\seed.iso`, false),
		"unregister": down.Unregister(ctx, "lab"),
		"upload":     down.Upload(ctx, `G:\seed.iso`, []byte("x")),
		"remove":     down.Remove(ctx, `G:\seed.iso`, true),
	} {
		if err == nil || !strings.Contains(err.Error(), "connection refused") {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := down.ReadTail(ctx, `G:\log`, 10); err == nil {
		t.Error("read from a host that is down")
	}
	// The import succeeds and taking its adapter away fails.
	half := Host{Runner: &sequence{outs: []Output{{}, {ExitCode: 1, Stderr: "VBoxManage.exe: error: locked\r\n"}}}, Path: vbox}
	if err := half.Import(ctx, `G:\x.ova`, "lab", ""); err == nil || !strings.Contains(err.Error(), "locked") {
		t.Errorf("the adapter left in place: %v", err)
	}
	for name, out := range map[string]Output{
		"a script that failed":  {ExitCode: 1, Stderr: "Access to the path is denied.\r\n"},
		"a count that is wrong": {Stdout: "written=0\r\n"},
		"no answer to the read": {Stdout: "not base64!\r\n"},
	} {
		host := Host{Runner: answering{out}, Path: vbox}
		if err := host.Upload(ctx, `G:\seed.iso`, []byte("x")); err == nil {
			t.Errorf("upload, %s: accepted", name)
		}
		if _, err := host.ReadTail(ctx, `G:\log`, 10); err == nil && out.ExitCode != 0 {
			t.Errorf("read, %s: accepted", name)
		}
		if err := host.Remove(ctx, `G:\log`, false); err == nil && out.ExitCode != 0 {
			t.Errorf("remove, %s: accepted", name)
		}
	}
	if _, err := (Host{Runner: answering{Output{Stdout: "not base64!"}}, Path: vbox}).ReadTail(ctx, `G:\log`, 10); err == nil {
		t.Error("a read answer that is not base64 was accepted")
	}
	if _, err := (Host{}).ReadTail(ctx, `G:\log`, 10); err == nil {
		t.Error("a host with no runner read")
	}
}

// sequence answers each Run with the next of outs.
type sequence struct{ outs []Output }

func (s *sequence) Run(context.Context, string, []string) (Output, error) {
	out := s.outs[0]
	s.outs = s.outs[1:]
	return out, nil
}

func (s *sequence) PowerShell(context.Context, string, string) (Output, error) {
	return Output{}, errors.New("not scripted")
}

func TestHardwareReading(t *testing.T) {
	m, err := machineFrom(Values{
		{Key: "name", Value: "lab"}, {Key: "UUID", Value: "5732a952-0b85-4e3a-820a-1a568f31166b"}, {Key: "VMState", Value: "poweroff"},
		{Key: "storagecontrollername0", Value: "SATA"}, {Key: "storagecontrollertype0", Value: "IntelAhci"},
		{Key: "SATA-0-0", Value: `G:\lab\disk.vdi`}, {Key: "SATA-ImageUUID-0-0", Value: "x"},
		{Key: "Other-0-0", Value: "none"},
		{Key: "uartmode1", Value: "disconnected"}, {Key: "uartmode2", Value: `file,G:\lab\two.log`},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Slots) != 1 || m.ConsoleLog != "" {
		t.Errorf("slots %+v, console %q", m.Slots, m.ConsoleLog)
	}
	if _, ok := m.FreeIDESlot(); ok {
		t.Error("a SATA-only machine has a free IDE slot")
	}
}

func TestWinRMRunnerPowerShellRefusesAnUnusableCredential(t *testing.T) {
	r := WinRMRunner{Target: winrmexec.Target{Host: "192.0.2.1", Port: 5986}}
	if _, err := r.PowerShell(context.Background(), "'x'", "input"); err == nil {
		t.Error("PowerShell ran with no credential")
	}
}

// TestCloneAsTheHostReportsIt reads a VM virt.vbox.vm.clone made, as the
// host reported it: its NICs, its seed in an IDE slot, and the console
// log, whose path VBoxManage writes with its backslashes unescaped.
func TestCloneAsTheHostReportsIt(t *testing.T) {
	values, err := ParseMachineReadable(fixture(t, "showvminfo-clone.stdout"))
	if err != nil {
		t.Fatal(err)
	}
	m, err := machineFrom(values)
	if err != nil {
		t.Fatal(err)
	}
	if m.ConsoleLog != `G:\PleiadesLab\ubuntu-lab\console.log` || m.ConfigFile != `G:\PleiadesLab\ubuntu-lab\ubuntu-lab.vbox` {
		t.Errorf("console %q, settings %q", m.ConsoleLog, m.ConfigFile)
	}
	if m.NICs[1] != (NIC{Kind: "nat", MAC: "08002754B6F3"}) || m.NICs[2] != (NIC{Kind: "hostonly", MAC: "080027811634", HostOnlyAdapter: "VirtualBox Host-Only Ethernet Adapter"}) {
		t.Errorf("NICs %+v", m.NICs)
	}
	held := m.SlotsHolding(`G:\PleiadesLab\ubuntu-lab`)
	if len(held) != 2 || held[0].Medium != `G:\PleiadesLab\ubuntu-lab\seed.iso` {
		t.Errorf("slots in the VM's folder: %+v", held)
	}
	if free, ok := m.FreeIDESlot(); !ok || free.Port != 0 || free.Device != 1 {
		t.Errorf("free IDE slot %+v", free)
	}
}

func TestExtraData(t *testing.T) {
	ctx := context.Background()
	global := Host{Runner: answering{Output{Stdout: fixture(t, "getextradata-global.stdout")}}, Path: vbox}
	data, err := global.ExtraData(ctx, "global")
	if err != nil {
		t.Fatal(err)
	}
	if data["HostOnly/{e7db4aea-6fc2-45df-99b8-0138ac2ec7b0}/Name"] != "VirtualBox Host-Only Ethernet Adapter" || len(data) != 5 {
		t.Errorf("extradata %v", data)
	}
	if data, err := (Host{Runner: answering{Output{}}, Path: vbox}).ExtraData(ctx, "lab"); err != nil || len(data) != 0 {
		t.Errorf("a machine with none: %v, %v", data, err)
	}
	if _, err := (Host{Runner: answering{Output{Stdout: "Value: x\r\n"}}, Path: vbox}).ExtraData(ctx, "lab"); err == nil {
		t.Error("a line that is no key and value was read")
	}
	if _, err := (Host{Runner: failing{}, Path: vbox}).ExtraData(ctx, "lab"); err == nil {
		t.Error("a host that is down answered")
	}
	r := &recorded{t: t}
	h := Host{Runner: r, Path: vbox}
	if err := h.SetExtraData(ctx, "lab", "pleiades/address", "192.168.56.10"); err != nil {
		t.Fatal(err)
	}
	if err := h.SetExtraData(ctx, "lab", "pleiades/address", ""); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(r.calls[0][1:], " ") + " | " + strings.Join(r.calls[1][1:], " "); got != "setextradata lab pleiades/address 192.168.56.10 | setextradata lab pleiades/address" {
		t.Errorf("calls %q", got)
	}
	never := Host{Runner: never{t}, Path: vbox}
	for name, err := range map[string]error{
		"a bad VM":             never.SetExtraData(ctx, "a b", "k", "v"),
		"a bad key":            never.SetExtraData(ctx, "lab", "a key", "v"),
		"a quote in the value": never.SetExtraData(ctx, "lab", "k", `a"b`),
		"a long value":         never.SetExtraData(ctx, "lab", "k", strings.Repeat("a", 257)),
		"a newline":            never.SetExtraData(ctx, "lab", "k", "a\nb"),
	} {
		if err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := never.ExtraData(ctx, "a b"); err == nil {
		t.Error("read a bad VM's extradata")
	}
}

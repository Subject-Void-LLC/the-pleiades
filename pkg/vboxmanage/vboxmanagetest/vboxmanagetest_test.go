// Tests that hold the model to what a real host answered: each compares
// the model's answer with one captured into pkg/vboxmanage/testdata from
// VirtualBox 7.2.20 on Windows 11, read through the same parser.
package vboxmanagetest

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/vboxmanage"
)

// captured returns a file of the captured answers.
func captured(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// capturedMachine parses a captured showvminfo answer.
func capturedMachine(t *testing.T, name string) vboxmanage.Machine {
	t.Helper()
	values, err := vboxmanage.ParseMachineReadable(captured(t, name))
	if err != nil {
		t.Fatal(err)
	}
	m, err := vboxmanage.Host{Runner: fixed{captured(t, name)}, Path: Path}.Machine(context.Background(), "pleiades-probe")
	if err != nil {
		t.Fatal(err)
	}
	m.Values = values
	return m
}

// fixed answers every call with one showvminfo answer.
type fixed struct{ stdout string }

func (f fixed) Run(context.Context, string, []string) (vboxmanage.Output, error) {
	return vboxmanage.Output{Stdout: f.stdout}, nil
}

func (f fixed) PowerShell(context.Context, string, string) (vboxmanage.Output, error) {
	return vboxmanage.Output{}, nil
}

// probe is the captured machine, as the model holds it.
func probe() *VM {
	return &VM{Name: "pleiades-probe", UUID: "9883f6c3-b17d-4077-aa52-d9c49a6912a5", State: vboxmanage.StatePoweroff, MemoryMB: 64, CPUs: 1,
		NICs: map[int]vboxmanage.NIC{1: {Kind: "nat", MAC: "080027101678"}}}
}

// modeled is the part of a Machine the model answers for.
func modeled(m vboxmanage.Machine) vboxmanage.Machine {
	m.Values = nil
	m.ConfigFile = ""
	return m
}

func TestShowvminfoMatchesTheCapturedMachine(t *testing.T) {
	for name, vm := range map[string]*VM{
		"showvminfo-poweroff.stdout": probe(),
		"showvminfo-with-snapshot.stdout": func() *VM {
			vm := probe()
			vm.Snapshots = []Snapshot{{Name: "fixture-snap", UUID: "5732a952-0b85-4e3a-820a-1a568f31166b", Description: "fixture"}}
			vm.Current = vm.Snapshots[0].UUID
			return vm
		}(),
		// Captured after the autostart service started it, so marked.
		"showvminfo-running.stdout": func() *VM {
			vm := probe()
			vm.State = vboxmanage.StateRunning
			vm.Autostart = true
			return vm
		}(),
	} {
		got, err := New(vm).VBoxHost().Machine(context.Background(), "pleiades-probe")
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		want := capturedMachine(t, name)
		if !reflect.DeepEqual(modeled(got), modeled(want)) {
			t.Errorf("%s: the model gave\n%+v\nthe host gave\n%+v", name, modeled(got), modeled(want))
		}
		// The lines the model writes are the host's own lines.
		for _, line := range strings.Split(strings.TrimSpace(vm.showvminfo()), "\r\n") {
			if !strings.Contains(captured(t, name), line+"\r\n") && !strings.Contains(captured(t, name), line+"\n") {
				t.Errorf("%s: the model wrote %q, which the host did not", name, line)
			}
		}
	}
}

func TestFailuresMatchTheCapturedOnes(t *testing.T) {
	h := New(probe())
	out, _ := h.Run(context.Background(), Path, []string{"showvminfo", "no-such-vm", "--machinereadable"})
	if out.Stderr != strings.ReplaceAll(captured(t, "showvminfo-missing.stderr"), "\n", "\r\n") &&
		out.Stderr != captured(t, "showvminfo-missing.stderr") {
		t.Errorf("missing machine: the model wrote\n%q\nthe host wrote\n%q", out.Stderr, captured(t, "showvminfo-missing.stderr"))
	}
	out, _ = h.Run(context.Background(), Path, []string{"controlvm", "pleiades-probe", "poweroff"})
	if strings.TrimSpace(out.Stderr) != strings.TrimSpace(captured(t, "controlvm-poweroff-not-running.stderr")) {
		t.Errorf("not running: the model wrote %q", out.Stderr)
	}
	if err := h.VBoxHost().PowerOff(context.Background(), "pleiades-probe"); !errors.Is(err, vboxmanage.ErrNotRunning) {
		t.Errorf("PowerOff of a stopped machine = %v, want ErrNotRunning", err)
	}
	if _, err := h.VBoxHost().Machine(context.Background(), "no-such-vm"); !errors.Is(err, vboxmanage.ErrNotFound) {
		t.Errorf("Machine of a missing one = %v, want ErrNotFound", err)
	}
}

func TestStartsOnlyTheWayTheHostDoes(t *testing.T) {
	ctx := context.Background()
	h := New(probe(), &VM{Name: "other", UUID: "11111111-1111-1111-1111-111111111111", State: vboxmanage.StatePoweroff, MemoryMB: 64, CPUs: 1})
	host := h.VBoxHost()
	if err := host.Start(ctx, "pleiades-probe"); err == nil || !strings.Contains(err.Error(), "VERR_NEM_INIT_FAILED") {
		t.Fatalf("a plain start with nothing running = %v, want the host's refusal", err)
	}
	via, err := vboxmanage.WindowsAutostart{Host: host}.Start(ctx, "pleiades-probe")
	if err != nil || !via {
		t.Fatalf("an autostart start = %v, %v", via, err)
	}
	if err := host.Start(ctx, "other"); err != nil {
		t.Fatalf("a plain start while one runs = %v", err)
	}
	if h.VM("other").State != vboxmanage.StateRunning {
		t.Errorf("other is %s", h.VM("other").State)
	}
	if err := host.SetAutostart(ctx, "pleiades-probe", false); err == nil {
		t.Error("changing a running machine's settings was allowed")
	}
	h.NoAutostartService = true
	if _, err := h.Run(ctx, `C:\Windows\System32\sc.exe`, []string{"start", "VBoxAutostartSvcvengeancepleiades-gate"}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Run(ctx, `C:\Windows\System32\cmd.exe`, nil); err == nil {
		t.Error("the model ran a program it does not model")
	}
}

func TestPowerButton(t *testing.T) {
	ctx := context.Background()
	vm := probe()
	vm.State = vboxmanage.StateRunning
	vm.PowerButtonReads = 2
	host := New(vm).VBoxHost()
	if err := host.ACPIShutdown(ctx, "pleiades-probe"); err != nil {
		t.Fatal(err)
	}
	var states []string
	for i := 0; i < 4; i++ {
		m, err := host.Machine(ctx, "pleiades-probe")
		if err != nil {
			t.Fatal(err)
		}
		states = append(states, m.State)
	}
	if got := strings.Join(states, " "); got != "running running poweroff poweroff" {
		t.Errorf("states after the press = %s", got)
	}
	vm.State, vm.PowerButtonReads = vboxmanage.StateRunning, -1
	if err := host.ACPIShutdown(ctx, "pleiades-probe"); err != nil {
		t.Fatal(err)
	}
	if m, _ := host.Machine(ctx, "pleiades-probe"); m.State != vboxmanage.StateRunning {
		t.Errorf("a guest that ignores the button is %s", m.State)
	}
}

func TestSnapshotTreeIsNumberedAsVBoxManageNumbersIt(t *testing.T) {
	ctx := context.Background()
	h := New(probe())
	host := h.VBoxHost()
	a, err := host.TakeSnapshot(ctx, "pleiades-probe", "a", "first")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := host.TakeSnapshot(ctx, "pleiades-probe", "b", ""); err != nil {
		t.Fatal(err)
	}
	if err := host.RestoreSnapshot(ctx, "pleiades-probe", a); err != nil {
		t.Fatal(err)
	}
	if _, err := host.TakeSnapshot(ctx, "pleiades-probe", "c", ""); err != nil {
		t.Fatal(err)
	}
	m, err := host.Machine(ctx, "pleiades-probe")
	if err != nil {
		t.Fatal(err)
	}
	var tree []string
	for _, s := range m.Snapshots {
		tree = append(tree, s.Name+s.Path)
	}
	if got := strings.Join(tree, " "); got != "a b-1 c-2" {
		t.Errorf("tree = %s, want a b-1 c-2", got)
	}
	if node, _ := m.Values.Get("CurrentSnapshotNode"); node != "SnapshotName-2" {
		t.Errorf("CurrentSnapshotNode = %q", node)
	}
	if err := host.DeleteSnapshot(ctx, "pleiades-probe", a); err == nil {
		t.Error("deleted a snapshot with two children, which VirtualBox refuses")
	}
	if err := host.DeleteSnapshot(ctx, "pleiades-probe", m.Snapshots[1].UUID); err != nil {
		t.Fatal(err)
	}
	if err := host.DeleteSnapshot(ctx, "pleiades-probe", m.Snapshots[1].UUID); !errors.Is(err, vboxmanage.ErrNotFound) {
		t.Errorf("deleting it again = %v, want ErrNotFound", err)
	}
	m, _ = host.Machine(ctx, "pleiades-probe")
	if len(m.Snapshots) != 2 || m.Snapshots[1].Name != "c" || m.Snapshots[1].Path != "-1" {
		t.Errorf("after deleting b: %+v", m.Snapshots)
	}
	h.VM("pleiades-probe").State = vboxmanage.StateRunning
	if err := host.RestoreSnapshot(ctx, "pleiades-probe", a); err == nil {
		t.Error("restored a running machine")
	}
	if !strings.HasPrefix(h.Calls()[0], "VBoxManage.exe snapshot pleiades-probe take a --description first") {
		t.Errorf("first call = %q", h.Calls()[0])
	}
}

func TestFail(t *testing.T) {
	h := New(probe())
	h.Fail = map[string]vboxmanage.Output{
		"VBoxManage.exe showvminfo":                {ExitCode: 1, Stderr: "broad\r\n"},
		"VBoxManage.exe showvminfo pleiades-probe": {ExitCode: 1, Stderr: "narrow\r\n"},
	}
	_, err := h.VBoxHost().Machine(context.Background(), "pleiades-probe")
	if err == nil || !strings.Contains(err.Error(), "narrow") {
		t.Errorf("err = %v, want the narrower failure", err)
	}
}

// TestHostinfoMatchesTheCapture proves the model's list hostinfo is the
// host's own answer byte for byte, and that the numbers a test sets are
// the ones read back.
func TestHostinfoMatchesTheCapture(t *testing.T) {
	out, err := New().Run(context.Background(), Path, []string{"list", "hostinfo"})
	if err != nil || out.Stdout != captured(t, "list-hostinfo.stdout") {
		t.Fatalf("the model wrote\n%q\nthe host wrote\n%q (%v)", out.Stdout, captured(t, "list-hostinfo.stdout"), err)
	}
	h := New()
	h.CPUs, h.MemoryMB, h.AvailableMB = 4, 8192, 2048
	info, err := h.VBoxHost().HostInfo(context.Background())
	if err != nil || info != (vboxmanage.HostInfo{CPUs: 4, MemoryMB: 8192, AvailableMB: 2048}) {
		t.Errorf("read %+v, %v", info, err)
	}
}

// TestResizeRefusedWhileSaved proves the model refuses a saved machine's
// memory or CPUs, as VirtualBox does, and changes a stopped one's.
func TestResizeRefusedWhileSaved(t *testing.T) {
	vm := probe()
	h := New(vm)
	if err := h.VBoxHost().Resize(context.Background(), vm.Name, 2048, 2); err != nil || vm.MemoryMB != 2048 || vm.CPUs != 2 {
		t.Fatalf("%v; %d MB, %d CPUs", err, vm.MemoryMB, vm.CPUs)
	}
	vm.State = vboxmanage.StateSaved
	if err := h.VBoxHost().Resize(context.Background(), vm.Name, 1024, 1); err == nil || !strings.Contains(err.Error(), "Saved state") {
		t.Errorf("a saved machine: %v", err)
	}
}

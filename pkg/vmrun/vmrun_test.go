// Tests of the host layer: the readers against verbatim captures of a real
// Fusion (testdata/), and each operation's command line against a recording
// runner.
package vmrun

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func capture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestParseCounted_RealCaptures(t *testing.T) {
	for name, tc := range map[string]struct {
		file, header string
		want         []string
	}{
		"no running VMs":                  {"list_none.txt", "Total running VMs:", nil},
		"one running VM":                  {"list_one.txt", "Total running VMs:", []string{"/private/tmp/pleiades-fusion-sample.enWDKX/s2.vmx"}},
		"after the product's diagnostics": {"start_noise.txt", "Total running VMs:", []string{"/private/tmp/pleiades-fusion-sample.enWDKX/s2.vmx"}},
		"no snapshots":                    {"snapshots_none.txt", "Total snapshots:", nil},
		"one snapshot":                    {"snapshots_one.txt", "Total snapshots:", []string{"base"}},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := parseCounted(capture(t, tc.file), tc.header)
			if err != nil || !reflect.DeepEqual(got, tc.want) && !(len(got) == 0 && len(tc.want) == 0) {
				t.Fatalf("parseCounted = %v, %v; want %v", got, err, tc.want)
			}
		})
	}
}

// A count that disagrees with the lines is a truncated or padded answer, and
// must not read as a shorter or longer list.
func TestParseCounted_RefusesWhatDoesNotAddUp(t *testing.T) {
	for name, s := range map[string]string{
		"fewer lines than counted": "Total running VMs: 2\n/a.vmx\n",
		"more lines than counted":  "Total running VMs: 1\n/a.vmx\n/b.vmx\n",
		"no header":                "/a.vmx\n",
		"empty":                    "",
		"a count that is not one":  "Total running VMs: many\n",
		"a negative count":         "Total running VMs: -1\n",
	} {
		t.Run(name, func(t *testing.T) {
			if got, err := parseCounted(s, "Total running VMs:"); err == nil {
				t.Fatalf("accepted %q as %v", s, got)
			}
		})
	}
}

func TestParseKeyValues_RealConfigAndPowerCaptures(t *testing.T) {
	cfg := parseKeyValues(capture(t, "configparams.txt"))
	for k, want := range map[string]string{
		"config.version": "8", "ethernet0.virtualDev": "e1000e", "firmware": "efi",
		"guestOS": "arm-ubuntu-64", "memsize": "768", "nvram": "s2.nvram", "virtualHW.version": "22",
	} {
		if cfg[k] != want {
			t.Errorf("config %q = %q, want %q", k, cfg[k], want)
		}
	}
	if got := parseKeyValues(capture(t, "power_on.txt"))["PowerState"]; got != "on" {
		t.Errorf("power on reads %q", got)
	}
	if got := parseKeyValues(capture(t, "power_off.txt"))["PowerState"]; got != "off" {
		t.Errorf("power off reads %q", got)
	}
}

func TestParseNetworks_RealCapture(t *testing.T) {
	got, err := parseNetworks(capture(t, "hostnetworks.txt"))
	want := []Network{
		{Index: 0, Name: "vmnet0", Type: "bridged", DHCP: false, Subnet: "empty", Mask: "empty"},
		{Index: 1, Name: "vmnet1", Type: "hostOnly", DHCP: true, Subnet: "192.168.56.0", Mask: "255.255.255.0"},
		{Index: 8, Name: "vmnet8", Type: "nat", DHCP: true, Subnet: "192.168.111.0", Mask: "255.255.255.0"},
	}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("parseNetworks = %+v, %v; want %+v", got, err, want)
	}
	if _, err := parseNetworks("Total host networks: 2\nINDEX NAME TYPE DHCP SUBNET MASK\n0 vmnet0 bridged false empty empty\n"); err == nil {
		t.Error("a table with fewer rows than counted was accepted")
	}
}

func TestParseCreated_RealCapture(t *testing.T) {
	if got := parseCreated(capture(t, "create.txt")); got != "/tmp/pleiades-fusion-sample.QkHAJ5/sample.vmx" {
		t.Errorf("parseCreated = %q", got)
	}
	if got := parseCreated("nothing useful\n"); got != "" {
		t.Errorf("parseCreated found %q in output that names no file", got)
	}
}

func TestFailure_ClassifiesEveryRealMessage(t *testing.T) {
	want := []error{ErrExists, ErrNotFound, ErrNotRunning, ErrNotFound, ErrNoTools}
	lines := strings.Split(strings.TrimSpace(capture(t, "vmrun-errors.txt")), "\n")
	if len(lines) != len(want) {
		t.Fatalf("%d captured errors, %d expectations", len(lines), len(want))
	}
	for i, line := range lines {
		e := failure("op", Output{Stdout: line + "\n", ExitCode: 255})
		if want[i] == nil {
			if errors.Is(e, ErrNotFound) || errors.Is(e, ErrNotRunning) || errors.Is(e, ErrExists) {
				t.Errorf("%q was classified as a sentinel", line)
			}
		} else if !errors.Is(e, want[i]) {
			t.Errorf("%q: not %v", line, want[i])
		}
		if strings.HasPrefix(e.Message, "Error:") || e.ExitCode != 255 {
			t.Errorf("%q: message %q exit %d", line, e.Message, e.ExitCode)
		}
	}
}

func TestChecks(t *testing.T) {
	for _, bad := range []string{"", "relative.vmx", "/a/b", "/a/b.vmx\n", "/a/b\x00.vmx", "/a/b" + string(rune(0x202e)) + ".vmx", "/" + strings.Repeat("a", 1100) + ".vmx"} {
		if CheckVMX(bad) == nil {
			t.Errorf("CheckVMX accepted %q", bad)
		}
	}
	for _, ok := range []string{"/Users/me/Virtual Machines.localized/win 11.vmwarevm/win 11.vmx", "/tmp/a.vmx"} {
		if err := CheckVMX(ok); err != nil {
			t.Errorf("CheckVMX(%q): %v", ok, err)
		}
	}
	for _, bad := range []string{"", "-rf", ".hidden", "a b", "a/b", "a;b", strings.Repeat("a", 64)} {
		if CheckName("vm name", bad) == nil {
			t.Errorf("CheckName accepted %q", bad)
		}
	}
	for _, bad := range []string{"", "-x", "a\nb", strings.Repeat("a", 129)} {
		if CheckSnapshotName(bad) == nil {
			t.Errorf("CheckSnapshotName accepted %q", bad)
		}
	}
	if err := CheckSnapshotName("before upgrade 1"); err != nil {
		t.Errorf("a snapshot name with spaces was refused: %v", err)
	}
	for _, bad := range []string{"", "-x", "a b", "a;b", "a=b"} {
		if CheckConfigKey(bad) == nil {
			t.Errorf("CheckConfigKey accepted %q", bad)
		}
	}
	if err := CheckConfigKey("ethernet0.virtualDev"); err != nil {
		t.Errorf("a real vmx key was refused: %v", err)
	}
}

// recorder answers each command from a table keyed by its first vmrun or
// vmcli verb and records every command line it was asked to run.
type recorder struct {
	argv   [][]string
	answer func(argv []string) Output
}

func (r *recorder) Run(_ context.Context, argv []string) (Output, error) {
	r.argv = append(r.argv, append([]string(nil), argv...))
	return r.answer(argv), nil
}

func fusion(r Runner) Host {
	return Host{Runner: r, Product: Fusion, VMRun: FusionVMRun, VMCLI: FusionVMCLI}
}

const vmx = "/Users/me/Virtual Machines.localized/win 11.vmwarevm/win 11.vmx"

func TestHost_CommandLines(t *testing.T) {
	ok := &recorder{answer: func([]string) Output { return Output{} }}
	h := fusion(ok)
	ctx := context.Background()
	for name, tc := range map[string]struct {
		do   func() error
		want []string
	}{
		"start":                    {func() error { return h.Start(ctx, vmx, false) }, []string{"/bin/sh", "-c", detachScript, "sh", FusionVMRun, "-T", "fusion", "start", vmx, "nogui"}},
		"start gui":                {func() error { return h.Start(ctx, vmx, true) }, []string{"/bin/sh", "-c", detachScript, "sh", FusionVMRun, "-T", "fusion", "start", vmx, "gui"}},
		"stop soft":                {func() error { return h.Stop(ctx, vmx, false) }, []string{FusionVMRun, "-T", "fusion", "stop", vmx, "soft"}},
		"stop hard":                {func() error { return h.Stop(ctx, vmx, true) }, []string{FusionVMRun, "-T", "fusion", "stop", vmx, "hard"}},
		"suspend":                  {func() error { return h.Suspend(ctx, vmx, false) }, []string{FusionVMRun, "-T", "fusion", "suspend", vmx, "soft"}},
		"reset":                    {func() error { return h.Reset(ctx, vmx, true) }, []string{"/bin/sh", "-c", detachScript, "sh", FusionVMRun, "-T", "fusion", "reset", vmx, "hard"}},
		"delete":                   {func() error { return h.DeleteVM(ctx, vmx) }, []string{FusionVMRun, "-T", "fusion", "deleteVM", vmx}},
		"snapshot":                 {func() error { return h.TakeSnapshot(ctx, vmx, "before upgrade") }, []string{FusionVMRun, "-T", "fusion", "snapshot", vmx, "before upgrade"}},
		"revert":                   {func() error { return h.RevertToSnapshot(ctx, vmx, "base") }, []string{FusionVMRun, "-T", "fusion", "revertToSnapshot", vmx, "base"}},
		"delete snapshot":          {func() error { return h.DeleteSnapshot(ctx, vmx, "base", false) }, []string{FusionVMRun, "-T", "fusion", "deleteSnapshot", vmx, "base"}},
		"delete snapshot children": {func() error { return h.DeleteSnapshot(ctx, vmx, "base", true) }, []string{FusionVMRun, "-T", "fusion", "deleteSnapshot", vmx, "base", "andDeleteChildren"}},
		"set config":               {func() error { return h.SetConfig(ctx, vmx, "firmware", "efi") }, []string{FusionVMCLI, vmx, "ConfigParams", "SetEntry", "firmware", "efi"}},
		"memory":                   {func() error { return h.SetMemory(ctx, vmx, 4096) }, []string{FusionVMCLI, vmx, "Chipset", "SetMemSize", "4096"}},
		"cpus":                     {func() error { return h.SetCPUs(ctx, vmx, 4) }, []string{FusionVMCLI, vmx, "Chipset", "SetVCpuCount", "4"}},
		"keys":                     {func() error { return h.SendKeys(ctx, vmx, "a") }, []string{FusionVMCLI, vmx, "MKS", "sendKeySequence", "a"}},
	} {
		t.Run(name, func(t *testing.T) {
			ok.argv = nil
			if err := tc.do(); err != nil {
				t.Fatal(err)
			}
			if len(ok.argv) != 1 || !reflect.DeepEqual(ok.argv[0], tc.want) {
				t.Fatalf("ran %q, want %q", ok.argv, tc.want)
			}
		})
	}
}

func TestHost_WorkstationUsesItsOwnProductFlag(t *testing.T) {
	ok := &recorder{answer: func([]string) Output { return Output{Stdout: "Total running VMs: 0\n"} }}
	h := Host{Runner: ok, Product: Workstation, VMRun: "/usr/bin/vmrun"}
	if _, err := h.Running(context.Background()); err != nil {
		t.Fatal(err)
	}
	if want := []string{"/usr/bin/vmrun", "-T", "ws", "list"}; !reflect.DeepEqual(ok.argv[0], want) {
		t.Fatalf("ran %q, want %q", ok.argv[0], want)
	}
}

func TestHost_RefusesWhatItCannotRunSafely(t *testing.T) {
	never := &recorder{answer: func([]string) Output { t.Fatal("a command ran"); return Output{} }}
	ctx := context.Background()
	h := fusion(never)
	for name, err := range map[string]error{
		"a relative vm path":            h.Start(ctx, "vm.vmx", false),
		"a vm path with newline":        h.Stop(ctx, "/a/b.vmx\n/etc/passwd", false),
		"a snapshot named as an option": h.TakeSnapshot(ctx, vmx, "-all"),
		"a config key with spaces":      h.SetConfig(ctx, vmx, "a b", "c"),
		"too little memory":             h.SetMemory(ctx, vmx, 1),
		"no cpus":                       h.SetCPUs(ctx, vmx, 0),
		"keys that look like an option": h.SendKeys(ctx, vmx, "-x"),
	} {
		if err == nil {
			t.Errorf("%s was run", name)
		}
	}
	if _, err := (Host{Runner: never, Product: "vbox", VMRun: FusionVMRun}).Running(ctx); err == nil {
		t.Error("a product vmrun does not drive was accepted")
	}
	if _, err := (Host{Runner: never, Product: Fusion, VMRun: "vmrun"}).Running(ctx); err == nil {
		t.Error("a tool path that is not absolute was accepted")
	}
}

func TestHost_ErrorsCarryTheToolsOwnWords(t *testing.T) {
	r := &recorder{answer: func(argv []string) Output {
		return Output{Stdout: "Error: A snapshot with the name already exists\n", ExitCode: 255}
	}}
	err := fusion(r).TakeSnapshot(context.Background(), vmx, "base")
	var e *Error
	if !errors.As(err, &e) || !errors.Is(err, ErrExists) || e.Message != "A snapshot with the name already exists" || e.ExitCode != 255 {
		t.Fatalf("err = %#v", err)
	}
}

func TestHost_ReadsRealOutputThroughTheCommands(t *testing.T) {
	r := &recorder{answer: func(argv []string) Output {
		switch {
		case argv[len(argv)-1] == "list":
			return Output{Stdout: capture(t, "start_noise.txt")}
		case argv[len(argv)-1] == "query" && argv[len(argv)-2] == "Power":
			return Output{Stdout: capture(t, "power_on.txt")}
		case argv[len(argv)-1] == "listHostNetworks":
			return Output{Stdout: capture(t, "hostnetworks.txt")}
		case argv[len(argv)-2] == "VM":
			return Output{}
		}
		return Output{Stdout: capture(t, "create.txt")}
	}}
	h := fusion(r)
	ctx := context.Background()
	if got, err := h.Running(ctx); err != nil || len(got) != 1 {
		t.Errorf("Running = %v, %v", got, err)
	}
	if got, err := h.PowerState(ctx, vmx); err != nil || got != "on" {
		t.Errorf("PowerState = %q, %v", got, err)
	}
	if got, err := h.Networks(ctx); err != nil || len(got) != 3 {
		t.Errorf("Networks = %v, %v", got, err)
	}
}

// TestGuestIP_DoesNotTrustAPlainNoToolsAnswer is what a real Fusion did with a
// Windows guest resumed from suspend: getGuestIPAddress said the tools were not
// running, checkToolsState said running, and -wait gave the address.
func TestGuestIP_DoesNotTrustAPlainNoToolsAnswer(t *testing.T) {
	noTools := Output{Stdout: "Error: The VMware Tools are not running in the virtual machine: " + vmx + "\n", ExitCode: 255}
	for name, tc := range map[string]struct {
		toolsState string
		wantIP     string
		wantErr    error
	}{
		"tools running: asked again with -wait": {"running", "10.0.0.155", nil},
		"tools only installed: no address":      {"installed", "", ErrNoTools},
	} {
		t.Run(name, func(t *testing.T) {
			r := &recorder{answer: func(argv []string) Output {
				switch {
				case argv[3] == "checkToolsState":
					return Output{Stdout: tc.toolsState + "\n"}
				case argv[len(argv)-1] == "-wait":
					return Output{Stdout: "10.0.0.155\n"}
				}
				return noTools
			}}
			ip, err := fusion(r).GuestIP(context.Background(), vmx)
			if ip != tc.wantIP || (tc.wantErr == nil) != (err == nil) || (tc.wantErr != nil && !errors.Is(err, tc.wantErr)) {
				t.Fatalf("GuestIP = %q, %v; want %q, %v (ran %q)", ip, err, tc.wantIP, tc.wantErr, r.argv)
			}
		})
	}
}

// TestHost_TheRestOfItsOperations drives each remaining operation against
// answers in the shapes the real captures hold, and each refusal before a
// command would run.
func TestHost_TheRestOfItsOperations(t *testing.T) {
	ctx := context.Background()
	r := &recorder{answer: func(argv []string) Output {
		switch {
		case argv[0] == "/bin/test":
			return Output{ExitCode: 1}
		case argv[len(argv)-1] == "query" && argv[len(argv)-2] == "ConfigParams":
			return Output{Stdout: capture(t, "configparams.txt")}
		case argv[len(argv)-1] == "query" && argv[len(argv)-2] == "Power":
			return Output{Stdout: capture(t, "power_off.txt")}
		case len(argv) > 3 && argv[3] == "listSnapshots":
			return Output{Stdout: capture(t, "snapshots_one.txt")}
		case len(argv) > 3 && argv[3] == "checkToolsState":
			return Output{Stdout: "running\n"}
		case len(argv) > 3 && argv[3] == "getGuestIPAddress":
			return Output{Stdout: "10.0.0.155\n"}
		case argv[0] == FusionVMCLI && argv[1] == "VM":
			return Output{Stdout: capture(t, "create.txt")}
		case argv[0] == "/bin/sh":
			return Output{Stdout: "4242\n"}
		}
		return Output{}
	}}
	h := fusion(r)
	if ok, err := h.VMXExists(ctx, vmx); ok || err != nil {
		t.Errorf("VMXExists = %v, %v", ok, err)
	}
	if cfg, err := h.Config(ctx, vmx); err != nil || cfg["firmware"] != "efi" {
		t.Errorf("Config = %v, %v", cfg, err)
	}
	if s, err := h.PowerState(ctx, vmx); err != nil || s != "off" {
		t.Errorf("PowerState = %q, %v", s, err)
	}
	if s, err := h.Snapshots(ctx, vmx); err != nil || len(s) != 1 || s[0] != "base" {
		t.Errorf("Snapshots = %v, %v", s, err)
	}
	if s, err := h.ToolsState(ctx, vmx); err != nil || s != "running" {
		t.Errorf("ToolsState = %q, %v", s, err)
	}
	if ip, err := h.GuestIP(ctx, vmx); err != nil || ip != "10.0.0.155" {
		t.Errorf("GuestIP = %q, %v", ip, err)
	}
	if got, err := h.CreateVM(ctx, "win11-arm", "/Users/me/vms", "arm-windows11-64"); err != nil || got != "/tmp/pleiades-fusion-sample.QkHAJ5/sample.vmx" {
		t.Errorf("CreateVM = %q, %v", got, err)
	}
	if pid, err := h.KeepAwake(ctx, vmx); err != nil || pid != 4242 {
		t.Errorf("KeepAwake = %d, %v", pid, err)
	}
	if err := h.Suspend(ctx, vmx, true); err != nil {
		t.Errorf("Suspend: %v", err)
	}
	for name, err := range map[string]error{
		"a bad vm name":             func() error { _, e := h.CreateVM(ctx, "a b", "/v", "arm-ubuntu-64"); return e }(),
		"a relative vm directory":   func() error { _, e := h.CreateVM(ctx, "a", "v", "arm-ubuntu-64"); return e }(),
		"a guest type with a space": func() error { _, e := h.CreateVM(ctx, "a", "/v", "arm ubuntu"); return e }(),
		"a value with a newline":    h.SetConfig(ctx, vmx, "firmware", "efi\nx"),
		"keeping a Workstation host awake": func() error {
			_, e := (Host{Runner: r, Product: Workstation, VMRun: "/usr/bin/vmrun"}).KeepAwake(ctx, vmx)
			return e
		}(),
		"a vmx that is not one": func() error { _, e := h.VMXExists(ctx, "/a/b"); return e }(),
	} {
		if err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	if _, err := (Host{Product: Fusion, VMRun: FusionVMRun}).Running(ctx); err == nil {
		t.Error("a host with no runner ran a command")
	}
	var nilConn SSHRunner
	if _, err := nilConn.Run(ctx, []string{"x"}); err == nil {
		t.Error("an SSH runner with no connection ran a command")
	}
}

// TestHost_UnreadableAnswersAreErrors: a tool that answers in a shape the
// readers do not know is an error, never an empty answer.
func TestHost_UnreadableAnswersAreErrors(t *testing.T) {
	ctx := context.Background()
	h := fusion(&recorder{answer: func([]string) Output { return Output{Stdout: "something else\n"} }})
	if _, err := h.PowerState(ctx, vmx); err == nil {
		t.Error("a power query with no PowerState was accepted")
	}
	if _, err := h.CreateVM(ctx, "a", "/v", "arm-ubuntu-64"); err == nil {
		t.Error("a create that named no vmx was accepted")
	}
	if _, err := h.KeepAwake(ctx, vmx); err == nil {
		t.Error("keeping awake with no process ID was accepted")
	}
	if _, err := h.Networks(ctx); err == nil {
		t.Error("a network list in no known shape was accepted")
	}
}

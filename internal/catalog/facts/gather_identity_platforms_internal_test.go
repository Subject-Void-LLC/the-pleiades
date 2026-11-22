// Tests of the identity facts on FreeBSD, macOS and Windows (Phase 41, base),
// each parser held to what a real lab guest printed on 2026-11-22: FreeBSD
// 15.1 and Windows Server 2025 on VirtualBox, and an Apple silicon Mac
// (whose serial and UUID are replaced here by made-up values of the same
// shape). The Linux reader's VirtualBox answer is held here too, since a lab
// guest showed it read as a cloud instance.
package facts

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport/need"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/winrmexec"
)

func TestFreeBSDVirtualization_ReadsTheJailAndTheGuest(t *testing.T) {
	cases := map[string]string{
		"0\nvbox\n":      "vm:oracle", // measured: bsd-lab on VirtualBox
		"0\nhv\n":        "vm:microsoft",
		"0\nbhyve\n":     "vm:bhyve",
		"0\nnone\n":      "physical",
		"0\ngeneric\n":   "vm:other",
		"0\nnvmm\n":      "vm:other",
		"1\nvbox\n":      "container:jail",
		"1\nnone\n":      "container:jail",
		"0\n":            "", // one line: which of the two it is cannot be told
		"vbox\n":         "",
		"":               "",
		"2\nnone\n":      "",
		"0\n\n":          "",
		"0\nvbox\nxen\n": "",
	}
	for in, want := range cases {
		got := valuesOf(parseFreeBSDVirtualization(in))
		if want == "" {
			if len(got) != 0 {
				t.Errorf("%q made %v, want no fact", in, got)
			}
			continue
		}
		if got[gatherFactVirtualization] != want || len(got) != 1 {
			t.Errorf("%q made %v, want %s", in, got, want)
		}
	}
}

// TestFreeBSDIdentityReads_SkipAJail holds each FreeBSD identity read to its
// guard, which reads only where FreeBSD says this is no jail, and keeps
// Linux's machine id read (whose guard cannot see a jail) off FreeBSD.
func TestFreeBSDIdentityReads_SkipAJail(t *testing.T) {
	for _, c := range []string{freebsdMachineIDCommand, freebsdSystemUUIDCommand, freebsdSerialCommand} {
		if !strings.HasPrefix(c, freebsdNotJailed) {
			t.Errorf("%q reads an identity without skipping a jail", c)
		}
	}
	// Each platform declares each of its identity keys exactly once, and runs
	// none of Linux's identity reads, whose container guard cannot see a jail.
	linux := map[string]bool{gatherMachineIDCommand: true, gatherSystemUUIDCommand: true, gatherSerialCommand: true, gatherVirtualizationCommand: true}
	for platform, want := range map[string]map[string]int{
		platformFreeBSD: {gatherFactMachineID: 1, gatherFactSystemUUID: 1, gatherFactSerial: 1, gatherFactVirtualization: 1},
		platformDarwin:  {gatherFactSystemUUID: 1, gatherFactSerial: 1, gatherFactVirtualization: 1},
	} {
		keys := map[string]int{}
		for _, p := range bsdHostProbes(platform) {
			if linux[p.command] {
				t.Errorf("%s runs Linux's identity read %q", platform, p.command)
			}
			for _, k := range []string{gatherFactMachineID, gatherFactSystemUUID, gatherFactSerial, gatherFactVirtualization} {
				if gatherWanted(p.keys, k) {
					keys[k]++
				}
			}
		}
		if !reflect.DeepEqual(keys, want) {
			t.Errorf("%s's identity probes declare %v, want %v", platform, keys, want)
		}
	}
	// What bsd-lab printed: kenv's UUID, the machine id its rc wrote from it,
	// and VirtualBox's serial; a planar serial of 0 is a placeholder.
	if got := valuesOf(gatherIdentityValue(gatherFactSystemUUID)("ccdbf245-fdcf-c248-982f-aaa8856becb0\n")); got[gatherFactSystemUUID] != "ccdbf245-fdcf-c248-982f-aaa8856becb0" {
		t.Errorf("uuid = %v", got)
	}
	if got := valuesOf(gatherMachineID("ccdbf245fdcfc248982faaa8856becb0\n")); got[gatherFactMachineID] != "ccdbf245fdcfc248982faaa8856becb0" {
		t.Errorf("machine id = %v", got)
	}
	if got := gatherIdentityValue(gatherFactSerial)("0\n"); got != nil {
		t.Errorf("serial 0 stored %v", got)
	}
}

// TestFreeBSDJailGuard_FailsClosed runs the guard itself through /bin/sh with
// a stand-in sysctl: it reads only when the answer is exactly 0, and reads
// nothing for a jail, an empty answer, a failed sysctl or none at all.
func TestFreeBSDJailGuard_FailsClosed(t *testing.T) {
	need.POSIXShell(t)
	cmd := freebsdNotJailed + "echo read; true"
	for name, c := range map[string]struct {
		script string // the stand-in sysctl's body, or "" for no sysctl
		want   string
	}{
		"not a jail":   {"echo 0", "read"},
		"a jail":       {"echo 1", ""},
		"empty answer": {"true", ""},
		"failed":       {"echo 0 >&2; exit 1", ""},
		"other answer": {"echo 00", ""},
		"no sysctl":    {"", ""},
	} {
		dir := t.TempDir()
		if c.script != "" {
			if err := os.WriteFile(filepath.Join(dir, "sysctl"), []byte("#!/bin/sh\n"+c.script+"\n"), 0o700); err != nil {
				t.Fatal(err)
			}
		}
		run := exec.Command(need.ShellPath, "-c", cmd)
		// Only the stand-in on PATH: the shell's own test and echo answer the rest.
		run.Env = []string{"PATH=" + dir}
		out, err := run.Output()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got := strings.TrimSpace(string(out)); got != c.want {
			t.Errorf("%s: the guard printed %q, want %q", name, got, c.want)
		}
	}
}

func TestDarwinIdentity_ReadsIORegAndTheHypervisorFlag(t *testing.T) {
	// ioreg -rd1 -c IOPlatformExpertDevice, as an Apple silicon Mac prints
	// it, the serial and UUID replaced by made-up values of their shape.
	out := "+-o J713AP  <class IOPlatformExpertDevice, id 0x100000201, registered, matched, active, busy 0 (2 ms), retain 39>\n" +
		"    {\n" +
		"      \"IOPolledInterface\" = \"AppleARMWatchdogTimerHibernateHandler is not serializable\"\n" +
		"      \"target-type\" = <\"J713\">\n" +
		"      \"manufacturer\" = <\"Apple Inc.\">\n" +
		"      \"IOPlatformSerialNumber\" = \"C0FFEE12AB\"\n" +
		"      \"model\" = <\"Mac16,12\">\n" +
		"      \"IOPlatformUUID\" = \"12AB345C-6789-0123-4567-D890E123F4A5\"\n" +
		"      \"IOPlatformUUID\" = \"00000000-0000-0000-0000-000000000001\"\n" +
		"    }\n"
	want := map[string]any{gatherFactSerial: "C0FFEE12AB", gatherFactSystemUUID: "12ab345c-6789-0123-4567-d890e123f4a5"}
	if got := valuesOf(parseIOPlatform(out)); !reflect.DeepEqual(got, want) {
		t.Errorf("ioreg = %v, want %v", got, want)
	}
	for _, bad := range []string{
		"", "\"IOPlatformUUID\" = <\"x\">\n", "\"IOPlatformSerialNumber\" = \"\"\n",
		"\"IOPlatformSerialNumber\" = \"Not Specified\"\n", "\"IOPlatformUUID\" = \"a\"b\"\n",
	} {
		if got := parseIOPlatform(bad); got != nil {
			t.Errorf("%q read as %v", bad, got)
		}
	}
	cases := map[string]string{
		"0\nMac16,12\n":      "physical", // measured
		"1\nVirtualMac2,1\n": "vm:apple",
		"1\nVMware7,1\n":     "vm:vmware",
		"1\nSomething1,1\n":  "vm:other",
		"Mac16,12\n":         "",
		"":                   "",
		"2\nMac16,12\n":      "",
	}
	for in, want := range cases {
		got := valuesOf(parseDarwinVirtualization(in))
		if (want == "" && len(got) != 0) || (want != "" && got[gatherFactVirtualization] != want) {
			t.Errorf("%q made %v, want %q", in, got, want)
		}
	}
	for _, p := range bsdHostProbes(platformDarwin) {
		if gatherWanted(p.keys, gatherFactMachineID) {
			t.Errorf("a Mac's probe %q claims a machine id, which macOS does not have", p.command)
		}
	}
}

func TestWindowsIdentity_ReadsTheGuidTheProductAndTheModel(t *testing.T) {
	for in, want := range map[string]any{
		"c69e0132-de7c-4d77-84e4-80398ef0f423":   "c69e0132de7c4d7784e480398ef0f423", // measured: win-lab
		"{C69E0132-DE7C-4D77-84E4-80398EF0F423}": "c69e0132de7c4d7784e480398ef0f423",
		"c69e0132de7c4d7784e480398ef0f423":       nil,
		"not a guid":                             nil,
		"c69e0132-de7c-4d77-84e4-80398ef0f42z":   nil,
		"":                                       nil,
	} {
		got := valuesOf(windowsMachineID(in))
		if v, ok := got[gatherFactMachineID]; (want == nil && ok) || (want != nil && v != want) {
			t.Errorf("MachineGuid %q made %v, want %v", in, got, want)
		}
	}
	min := valuesOf(parseWindowsMin(`{"os.kernel.name":"windows","os.machine_id":"c69e0132-de7c-4d77-84e4-80398ef0f423"}`))
	if min[gatherFactMachineID] != "c69e0132de7c4d7784e480398ef0f423" {
		t.Errorf("min = %v", min)
	}

	// What win-lab, a Windows Server 2025 guest on VirtualBox, printed.
	hw := valuesOf(parseWindowsHardware(`{"hardware.memory_mb":2047,"hardware.system_uuid":"EF335D38-E91D-4A7F-866B-54B0C9465672",` +
		`"hardware.serial":"VirtualBox-ef335d38-e91d-4a7f-866b-54b0c9465672","computer_manufacturer":"innotek GmbH","computer_model":"VirtualBox","hypervisor_present":true}`))
	want := map[string]any{gatherFactMemoryMB: 2047, gatherFactSystemUUID: "ef335d38-e91d-4a7f-866b-54b0c9465672",
		gatherFactSerial: "VirtualBox-ef335d38-e91d-4a7f-866b-54b0c9465672", gatherFactVirtualization: "vm:oracle"}
	if !reflect.DeepEqual(hw, want) {
		t.Errorf("hardware = %v\nwant %v", hw, want)
	}
	virt := func(manufacturer, model, present string) any {
		obj := `{"computer_manufacturer":"` + manufacturer + `","computer_model":"` + model + `"`
		if present != "" {
			obj += `,"hypervisor_present":` + present
		}
		return valuesOf(parseWindowsHardware(obj + "}"))[gatherFactVirtualization]
	}
	for _, c := range []struct {
		manufacturer, model, present string
		want                         any
	}{
		{"Microsoft Corporation", "Virtual Machine", "true", "vm:microsoft"},
		{"VMware, Inc.", "VMware20,1", "true", "vm:vmware"},
		// A guest whose hypervisor hides its CPUID bit is still known by its
		// firmware's vendor, never read as physical.
		{"QEMU", "Standard PC (Q35 + ICH9, 2009)", "false", "vm:qemu"},
		{"Xen", "HVM domU", "false", "vm:xen"},
		{"Amazon EC2", "m5.large", "true", "cloud:amazon"},
		{"Dell Inc.", "OptiPlex 7090", "false", "physical"},
		// A physical host running virtualization-based security reports a
		// hypervisor too, so an unknown model with one makes no fact.
		{"Dell Inc.", "OptiPlex 7090", "true", nil},
		{"Dell Inc.", "OptiPlex 7090", "", nil},
		{"Microsoft Corporation", "Surface Pro", "true", nil},
	} {
		if got := virt(c.manufacturer, c.model, c.present); got != c.want {
			t.Errorf("%s %s hypervisor %q = %v, want %v", c.manufacturer, c.model, c.present, got, c.want)
		}
	}
	// Placeholders are left out, and the raw values never become facts.
	odd := valuesOf(parseWindowsHardware(`{"hardware.system_uuid":"00000000-0000-0000-0000-000000000000","hardware.serial":"To be filled by O.E.M.","computer_model":"x","hypervisor_present":false}`))
	if !reflect.DeepEqual(odd, map[string]any{gatherFactVirtualization: "physical"}) {
		t.Errorf("placeholders = %v", odd)
	}
	// A Windows container is container:windows and keeps no identity, even
	// one that came all the same.
	ctr := valuesOf(parseWindowsHardware(`{"hardware.memory_mb":512,"hardware.system_uuid":"EF335D38-E91D-4A7F-866B-54B0C9465672","hardware.serial":"S1",` +
		`"computer_manufacturer":"Dell Inc.","computer_model":"PowerEdge R650","hypervisor_present":false,"in_container":true}`))
	if !reflect.DeepEqual(ctr, map[string]any{gatherFactMemoryMB: 512, gatherFactVirtualization: "container:windows"}) {
		t.Errorf("container = %v", ctr)
	}
	// The scripts skip every identity read in a container, by the one marker.
	for _, script := range []string{windowsMinScript, windowsHardwareScript} {
		if !strings.Contains(script, "Get-Service -Name cexecsvc -ErrorAction SilentlyContinue") {
			t.Errorf("a Windows script reads an identity without checking for a container:\n%s", script)
		}
	}
}

// TestGather_AWindowsHostReportsItsIdentity drives Gather over the WinRM host
// model with what win-lab printed, so the scripts' identity lines reach the
// facts through their parsers: the MachineGuid as a machine id, the UUID
// lowercased, and VirtualBox as vm:oracle, with no raw value kept.
func TestGather_AWindowsHostReportsItsIdentity(t *testing.T) {
	answerWindows(t, map[string]winrmexec.Result{
		gatherSubsetMin: {Stdout: `{"host.nodename":"win-lab","os.kernel.name":"windows","os.machine_id":"c69e0132-de7c-4d77-84e4-80398ef0f423","processor_architecture":"AMD64"}`},
		gatherSubsetHardware: {Stdout: `{"hardware.memory_mb":2047,"hardware.system_uuid":"EF335D38-E91D-4A7F-866B-54B0C9465672","hardware.serial":"VirtualBox-ef335d38-e91d-4a7f-866b-54b0c9465672",` +
			`"computer_manufacturer":"innotek GmbH","computer_model":"VirtualBox","hypervisor_present":true,"in_container":false}`},
	})
	rc := &winTestContext{facts: map[string]any{}}
	if _, err := Gather(context.Background(), rc, newWinTestHost(), map[string]any{}); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		gatherFactMachineID:      "c69e0132de7c4d7784e480398ef0f423",
		gatherFactSystemUUID:     "ef335d38-e91d-4a7f-866b-54b0c9465672",
		gatherFactSerial:         "VirtualBox-ef335d38-e91d-4a7f-866b-54b0c9465672",
		gatherFactVirtualization: "vm:oracle",
	}
	for k, v := range want {
		if rc.facts[k] != v {
			t.Errorf("%s = %v, want %v", k, rc.facts[k], v)
		}
	}
	for k := range windowsRawKeys {
		if _, ok := rc.facts[k]; ok {
			t.Errorf("the raw value %s became a fact: %v", k, rc.facts)
		}
	}
}

func TestLinuxVirtualization_VirtualBoxIsAVirtualMachine(t *testing.T) {
	// systemd-detect-virt names VirtualBox oracle (measured on ubuntu-lab,
	// systemd 255), which read as cloud:oracle until 2026-11-22.
	for in, want := range map[string]string{
		"oracle\n|\nnone\n":   "vm:oracle",
		"vm-other\n|\nnone\n": "vm:other",
		"google\n|\nnone\n":   "cloud:google",
	} {
		if got := valuesOf(gatherVirtualization(in)); got[gatherFactVirtualization] != want {
			t.Errorf("%q = %v, want %s", in, got, want)
		}
	}
	// One UUID reads alike from every platform: lowercase.
	if got := valuesOf(gatherIdentityValue(gatherFactSystemUUID)("A2713A49-AA27-F541-A436-4647FB3CBADA\n")); got[gatherFactSystemUUID] != "a2713a49-aa27-f541-a436-4647fb3cbada" {
		t.Errorf("uuid = %v", got)
	}
	// A serial keeps its case.
	if got := valuesOf(gatherIdentityValue(gatherFactSerial)("AbC123\n")); got[gatherFactSerial] != "AbC123" {
		t.Errorf("serial = %v", got)
	}
}

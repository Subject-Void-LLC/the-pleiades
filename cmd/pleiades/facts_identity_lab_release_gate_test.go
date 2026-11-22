// facts.gather's identity facts (Phase 41, base) through the real binary
// against the lab's FreeBSD, macOS and Windows machines, each held to what
// the machine's own tools print: kenv and /etc/machine-id on FreeBSD, ioreg
// and sysctl on a Mac, and CIM and the registry on Windows. Each test skips
// unless its machine is configured (PLEIADES_BSD_PROJECT, PLEIADES_MAC_PROJECT,
// PLEIADES_WINRM_PROJECT and their devices).
package main_test

import (
	"os"
	"strings"
	"testing"
)

// labIdentity is the four identity facts from stats, each as a string or ""
// when absent.
func labIdentity(stats map[string]any) (machineID, uuid, serial, virt string) {
	get := func(k string) string { s, _ := stats[k].(string); return s }
	return get("os.machine_id"), get("hardware.system_uuid"), get("hardware.serial"), get("hardware.virtualization")
}

// labFirmwarePlaceholders are the firmware placeholders facts.gather leaves
// out, lowercased, which this gate expects absent rather than equal.
var labFirmwarePlaceholders = map[string]bool{
	"": true, "0": true, "none": true, "na": true, "not specified": true, "default string": true,
	"to be filled by o.e.m.": true, "system serial number": true, "system product name": true,
	"00000000-0000-0000-0000-000000000000": true, "ffffffff-ffff-ffff-ffff-ffffffffffff": true,
}

// labExpected is what the gather stores for a value the device's own tool
// printed: the value itself, or nothing for a placeholder.
func labExpected(raw string) string {
	if labFirmwarePlaceholders[strings.ToLower(strings.TrimSpace(raw))] {
		return ""
	}
	return raw
}

func TestCLI_FactsGatherOnFreeBSDReportsItsIdentity(t *testing.T) {
	m := labMachine{os.Getenv(envBSDProject), os.Getenv(envBSDDevice)}
	if m.dir == "" || m.host == "" {
		t.Skipf("needs a FreeBSD host onboarded as generic_ssh: set %s and %s", envBSDProject, envBSDDevice)
	}
	if labShell(t, m, "sysctl -n security.jail.jailed") != "0" {
		t.Skipf("%s is a jail; this test reads a machine", m.host)
	}
	machineID, uuid, serial, virt := labIdentity(labGatherAll(t, m))
	if want := labShell(t, m, "cat /etc/machine-id"); machineID != want || len(want) != 32 {
		t.Errorf("os.machine_id = %q, /etc/machine-id holds %q", machineID, want)
	}
	if want := labExpected(strings.ToLower(labShell(t, m, "kenv -q smbios.system.uuid"))); uuid != want {
		t.Errorf("hardware.system_uuid = %q, kenv says %q", uuid, want)
	}
	if want := labExpected(labShell(t, m, "kenv -q smbios.system.serial")); serial != want {
		t.Errorf("hardware.serial = %q, kenv says %q", serial, want)
	}
	switch guest := labShell(t, m, "sysctl -n kern.vm_guest"); {
	case guest == "none" && virt != "physical", guest == "vbox" && virt != "vm:oracle", guest != "none" && !strings.HasPrefix(virt, "vm:"):
		t.Errorf("hardware.virtualization = %q, kern.vm_guest says %q", virt, guest)
	}
}

func TestCLI_FactsGatherOnMacOSReportsItsIdentity(t *testing.T) {
	m := labMachine{os.Getenv(envMacProject), os.Getenv(envMacDevice)}
	if m.dir == "" || m.host == "" {
		t.Skipf("needs a Mac onboarded as generic_ssh: set %s and %s", envMacProject, envMacDevice)
	}
	stats := labGatherAll(t, m)
	machineID, uuid, serial, virt := labIdentity(stats)
	if machineID != "" {
		t.Errorf("os.machine_id = %q on a Mac, which has none", machineID)
	}
	property := func(name string) string {
		return labShell(t, m, `ioreg -rd1 -c IOPlatformExpertDevice | awk -F'"' '$2 == "`+name+`" { print $4 }'`)
	}
	if want := labExpected(strings.ToLower(property("IOPlatformUUID"))); uuid != want {
		t.Errorf("hardware.system_uuid = %q, ioreg says %q", uuid, want)
	}
	if want := labExpected(property("IOPlatformSerialNumber")); serial != want {
		t.Errorf("hardware.serial = %q, ioreg says %q", serial, want)
	}
	if present := labShell(t, m, "sysctl -n kern.hv_vmm_present"); (present == "0") != (virt == "physical") || (present == "1" && !strings.HasPrefix(virt, "vm:")) {
		t.Errorf("hardware.virtualization = %q, kern.hv_vmm_present says %q", virt, present)
	}
}

func TestCLI_FactsGatherOnWindowsReportsItsIdentity(t *testing.T) {
	m := labMachine{os.Getenv(envWinRMProject), os.Getenv(envWinRMDevice)}
	if m.dir == "" || m.host == "" {
		t.Skipf("needs a Windows host reached over WinRM: set %s and %s", envWinRMProject, envWinRMDevice)
	}
	machineID, uuid, serial, virt := labIdentity(labGather(t, m))
	guid := labPowerShell(t, m, `(Get-ItemProperty -LiteralPath 'HKLM:\SOFTWARE\Microsoft\Cryptography' -Name MachineGuid).MachineGuid`)
	if want := strings.ReplaceAll(strings.ToLower(guid), "-", ""); machineID != want || len(want) != 32 {
		t.Errorf("os.machine_id = %q, MachineGuid is %q", machineID, guid)
	}
	if want := labExpected(strings.ToLower(labPowerShell(t, m, `(Get-CimInstance Win32_ComputerSystemProduct).UUID`))); uuid != want {
		t.Errorf("hardware.system_uuid = %q, CIM says %q", uuid, want)
	}
	if want := labExpected(labPowerShell(t, m, `(Get-CimInstance Win32_ComputerSystemProduct).IdentifyingNumber`)); serial != want {
		t.Errorf("hardware.serial = %q, CIM says %q", serial, want)
	}
	model := labPowerShell(t, m, `$c = Get-CimInstance Win32_ComputerSystem; [string]$c.Model + '|' + [string]$c.HypervisorPresent`)
	switch {
	case strings.HasPrefix(model, "VirtualBox|") && virt != "vm:oracle", strings.HasSuffix(model, "|False") && virt != "physical":
		t.Errorf("hardware.virtualization = %q, the model and hypervisor flag are %q", virt, model)
	}
}

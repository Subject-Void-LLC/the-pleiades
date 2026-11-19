// Package facts: the identity facts (Phase 41, base). A device's machine id, the
// hardware UUID and serial its firmware reports, and whether it is physical, a
// virtual machine, a cloud instance or a container. A lifecycle decision (an RMA
// on metal, a routine event on a VM) reads these, so each is declared stable
// and read as the device reports it. Absent is never stored: a value the read
// cannot make (root-only DMI on a login without root, a tool the host does not
// ship) is left out, and so is every placeholder firmware writes in place of a
// real serial or UUID.
package facts

import "strings"

const (
	gatherFactMachineID      = "os.machine_id"
	gatherFactSystemUUID     = "hardware.system_uuid"
	gatherFactSerial         = "hardware.serial"
	gatherFactVirtualization = "hardware.virtualization"
)

// identityPlaceholders are the values firmware and OEM tools write where a
// real serial or UUID belongs. Compared lowercased and trimmed. Measured per OS
// is the owner's rule; this is the set seen on Linux DMI tables.
var identityPlaceholders = map[string]bool{
	"to be filled by o.e.m.":               true,
	"0":                                    true,
	"not specified":                        true,
	"none":                                 true,
	"default string":                       true,
	"na":                                   true,
	"system serial number":                 true,
	"system product name":                  true,
	"00000000-0000-0000-0000-000000000000": true,
	"ffffffff-ffff-ffff-ffff-ffffffffffff": true,
}

// gatherNotInContainer guards an identity read so it runs only outside a
// container: a container sees its host's DMI values and its image's machine id,
// so storing them would make every container of one image read as one machine
// and every reschedule as a replacement (the identity item's rule). Docker's and
// Podman's marker files and systemd-detect-virt each say "container"; any of
// them skips the read, which then emits nothing. WSL reads as a container too.
const gatherNotInContainer = "[ -e /.dockerenv ] || [ -e /run/.containerenv ] || systemd-detect-virt --container -q 2>/dev/null || "

// The identity reads, each guarded by gatherNotInContainer.
const (
	gatherMachineIDCommand  = gatherNotInContainer + "cat /etc/machine-id 2>/dev/null; true"
	gatherSystemUUIDCommand = gatherNotInContainer + "cat /sys/class/dmi/id/product_uuid 2>/dev/null; true"
	gatherSerialCommand     = gatherNotInContainer + "cat /sys/class/dmi/id/product_serial 2>/dev/null; true"
)

// gatherIdentityValue reads one line of firmware text as a stable identity
// fact under key, or nothing when the text is empty or a placeholder.
func gatherIdentityValue(key string) func(string) []gatherFact {
	return func(stdout string) []gatherFact {
		value := strings.TrimSpace(stdout)
		if value == "" || identityPlaceholders[strings.ToLower(value)] {
			return nil
		}
		return []gatherFact{{key: key, value: value}}
	}
}

// gatherMachineID reads /etc/machine-id: 32 lowercase hexadecimal digits. Any
// other text is not a machine id and is left out, never stored as one.
func gatherMachineID(stdout string) []gatherFact {
	value := strings.TrimSpace(stdout)
	if len(value) != 32 {
		return nil
	}
	for _, r := range value {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return nil
		}
	}
	return []gatherFact{{key: gatherFactMachineID, value: value}}
}

// gatherVirtualizationCommand asks systemd-detect-virt for the virtual machine
// and the container separately, joined by a line holding "|", so one read
// answers both. Either may be absent on a host without the tool: the command
// still succeeds, and the parser leaves the fact out.
const gatherVirtualizationCommand = "systemd-detect-virt --vm 2>/dev/null; echo '|'; systemd-detect-virt --container 2>/dev/null; true"

// cloudProviders are the hypervisor names systemd-detect-virt reports for a
// cloud instance: the instance's provider, not a hypervisor the owner runs.
// microsoft is left out on purpose: an Azure instance and a Hyper-V host both
// report it, and systemd-detect-virt cannot tell them apart, so it reads as a VM.
var cloudProviders = map[string]bool{"amazon": true, "google": true, "oracle": true, "alibaba": true}

// gatherVirtualization maps the two answers to one native value: container:<engine>
// for a container, cloud:<provider> for a cloud instance, vm:<hypervisor> for a
// virtual machine of the owner's own, and physical for a device that is neither.
// Neither answer present means the tool was absent, and no fact is made.
func gatherVirtualization(stdout string) []gatherFact {
	vm, container, found := strings.Cut(stdout, "|")
	if !found {
		return nil
	}
	vm = strings.TrimSpace(vm)
	container = strings.TrimSpace(container)
	if vm == "" && container == "" {
		return nil
	}
	var value string
	switch {
	case container != "" && container != "none":
		value = "container:" + container
	case vm == "" || vm == "none":
		value = "physical"
	case cloudProviders[vm]:
		value = "cloud:" + vm
	default:
		value = "vm:" + vm
	}
	return []gatherFact{{key: gatherFactVirtualization, value: value}}
}

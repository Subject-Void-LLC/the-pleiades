// Package facts: the identity facts of FreeBSD and macOS (Phase 41, base),
// the same four keys Linux reports (gather_identity.go), read from each
// system's own sources as a lab guest of each printed them. FreeBSD keeps a
// machine id in /etc/machine-id (its rc writes the host UUID there without
// dashes) and gives the firmware's UUID and serial through kenv, which any
// login may read; a jail sees its host's firmware and its image's machine id,
// so none of the three is read inside one, as none is in a Linux container.
// macOS gives the UUID and serial through ioreg, to any login, and has no
// machine id at all, so it reports none.
package facts

import "strings"

// freebsdNotJailed runs the command after it only when FreeBSD says this is
// no jail. It fails closed: a host whose answer is missing or anything but 0
// reads nothing, since a jail read as a machine is the failure this exists
// to prevent.
const freebsdNotJailed = `[ "$(sysctl -n security.jail.jailed 2>/dev/null)" = 0 ] && `

// FreeBSD's identity reads, each guarded by freebsdNotJailed, and the
// virtualization read, which answers in a jail too (as container:jail).
const (
	freebsdMachineIDCommand      = freebsdNotJailed + "cat /etc/machine-id 2>/dev/null; true"
	freebsdSystemUUIDCommand     = freebsdNotJailed + "kenv -q smbios.system.uuid 2>/dev/null; true"
	freebsdSerialCommand         = freebsdNotJailed + "kenv -q smbios.system.serial 2>/dev/null; true"
	freebsdVirtualizationCommand = "sysctl -n security.jail.jailed kern.vm_guest 2>/dev/null; true"
)

// freebsdGuests maps FreeBSD's kern.vm_guest names onto systemd-detect-virt's,
// so one hypervisor reads alike from every platform. none is no hypervisor;
// generic is one FreeBSD cannot name, and so is any name not listed here.
var freebsdGuests = map[string]string{
	"vbox":      "oracle",
	"hv":        "microsoft",
	"vmware":    "vmware",
	"kvm":       "kvm",
	"xen":       "xen",
	"bhyve":     "bhyve",
	"parallels": "parallels",
}

// parseFreeBSDVirtualization reads security.jail.jailed and kern.vm_guest, one
// line each: container:jail in a jail, physical where there is no hypervisor,
// and vm:<hypervisor> otherwise. An answer missing either line makes no fact,
// since one line cannot say which of the two it is.
func parseFreeBSDVirtualization(out string) []gatherFact {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 {
		return nil
	}
	jailed, guest := strings.TrimSpace(lines[0]), strings.TrimSpace(lines[1])
	switch {
	case jailed == "1":
		return []gatherFact{{key: gatherFactVirtualization, value: "container:jail"}}
	case jailed != "0" || guest == "":
		return nil
	case guest == "none":
		return []gatherFact{{key: gatherFactVirtualization, value: "physical"}}
	}
	if name, ok := freebsdGuests[guest]; ok {
		return vmFact(name)
	}
	return vmFact(hypervisorOther)
}

// darwinPlatformCommand prints the platform expert's properties, among them
// IOPlatformUUID and IOPlatformSerialNumber, which any login may read.
const darwinPlatformCommand = "ioreg -rd1 -c IOPlatformExpertDevice"

// darwinVirtualizationCommand asks whether a hypervisor is present (1 or 0)
// and the model name, one line each.
const darwinVirtualizationCommand = "sysctl -n kern.hv_vmm_present hw.model"

// darwinProperties are the ioreg properties read, by the fact each is.
var darwinProperties = map[string]string{
	`"IOPlatformUUID"`:         gatherFactSystemUUID,
	`"IOPlatformSerialNumber"`: gatherFactSerial,
}

// parseIOPlatform reads ioreg's `"IOPlatformUUID" = "..."` and
// `"IOPlatformSerialNumber" = "..."` lines into the UUID and serial, each
// left out when it is missing, empty or a placeholder. Only the first of
// each counts.
func parseIOPlatform(out string) []gatherFact {
	var facts []gatherFact
	seen := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		name, rest, ok := strings.Cut(strings.TrimLeft(line, " \t|"), " = ")
		key := darwinProperties[name]
		if !ok || key == "" || seen[key] {
			continue
		}
		seen[key] = true
		// The value is one quoted string; anything else is not one.
		value, ok := strings.CutPrefix(strings.TrimSpace(rest), `"`)
		if value, ok = strings.CutSuffix(value, `"`); !ok || strings.Contains(value, `"`) {
			continue
		}
		if fact, ok := identityFact(key, value); ok {
			facts = append(facts, fact)
		}
	}
	return facts
}

// darwinGuests maps a virtual Mac's model name prefix onto
// systemd-detect-virt's hypervisor names: VirtualMac is Apple's
// Virtualization framework, and VMware's guests name VMware.
var darwinGuests = []struct{ prefix, hypervisor string }{
	{"VirtualMac", "apple"},
	{"VMware", "vmware"},
}

// parseDarwinVirtualization reads kern.hv_vmm_present and hw.model: physical
// when no hypervisor is present, and vm:<hypervisor> by the model's name when
// one is (vm:other for a model not listed). A macOS too old to have
// kern.hv_vmm_present answers one line, and makes no fact.
func parseDarwinVirtualization(out string) []gatherFact {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 {
		return nil
	}
	switch present, model := strings.TrimSpace(lines[0]), strings.TrimSpace(lines[1]); present {
	case "0":
		return []gatherFact{{key: gatherFactVirtualization, value: "physical"}}
	case "1":
		for _, g := range darwinGuests {
			if strings.HasPrefix(model, g.prefix) {
				return vmFact(g.hypervisor)
			}
		}
		return vmFact(hypervisorOther)
	}
	return nil
}

// bsdIdentityProbes are platform's (FreeBSD's or Darwin's) identity probes:
// the machine id in min, as on Linux, and the rest in hardware.
func bsdIdentityProbes(platform string) []gatherProbe {
	if platform == platformDarwin {
		return []gatherProbe{
			{subset: gatherSubsetHardware, keys: []string{gatherFactSystemUUID, gatherFactSerial}, command: darwinPlatformCommand, parse: parseIOPlatform},
			{subset: gatherSubsetHardware, keys: []string{gatherFactVirtualization}, command: darwinVirtualizationCommand, parse: parseDarwinVirtualization},
		}
	}
	return []gatherProbe{
		{subset: gatherSubsetMin, keys: []string{gatherFactMachineID}, command: freebsdMachineIDCommand, parse: gatherMachineID},
		{subset: gatherSubsetHardware, keys: []string{gatherFactSystemUUID}, command: freebsdSystemUUIDCommand, parse: gatherIdentityValue(gatherFactSystemUUID)},
		{subset: gatherSubsetHardware, keys: []string{gatherFactSerial}, command: freebsdSerialCommand, parse: gatherIdentityValue(gatherFactSerial)},
		{subset: gatherSubsetHardware, keys: []string{gatherFactVirtualization}, command: freebsdVirtualizationCommand, parse: parseFreeBSDVirtualization},
	}
}

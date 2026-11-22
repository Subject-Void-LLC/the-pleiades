// Package facts: the identity facts of a Windows host (Phase 41, base), the
// same four keys Linux reports (gather_identity.go), as a lab guest printed
// them. The machine id is the install's MachineGuid in the registry, as 32
// lowercase hexadecimal digits so it reads in the shape every other
// platform's does; the UUID and serial are the firmware's, from
// Win32_ComputerSystemProduct (SMBIOS's system information); and
// virtualization is read from the computer's manufacturer and model, with
// whether a hypervisor is present. A clone made without generalizing shares
// its source's MachineGuid, as a Linux clone shares /etc/machine-id. Inside a
// Windows container (one running the container execution agent, cexecsvc)
// none of the three identities is read: a process-isolated container sees its
// host's firmware and its image's registry, as a Linux container does.
package facts

import "strings"

// The min and hardware scripts' keys that are not facts: the raw values the
// identity parsers read and never emit.
const (
	windowsManufacturerKey      = "computer_manufacturer"
	windowsModelKey             = "computer_model"
	windowsHypervisorPresentKey = "hypervisor_present"
	windowsInContainerKey       = "in_container"
)

// windowsRawKeys are every key the min and hardware scripts print that is
// read by a parser rather than emitted.
var windowsRawKeys = map[string]bool{
	windowsArchitectureKey:      true,
	windowsManufacturerKey:      true,
	windowsModelKey:             true,
	windowsHypervisorPresentKey: true,
	windowsInContainerKey:       true,
}

// windowsMachineID is a MachineGuid (a GUID, braces and case as Windows
// writes it) as a machine id: 32 lowercase hexadecimal digits, or nothing for
// text that is not one.
func windowsMachineID(guid string) []gatherFact {
	id := strings.ToLower(strings.Trim(strings.TrimSpace(guid), "{}"))
	if len(id) != 36 || strings.Count(id, "-") != 4 {
		return nil
	}
	return gatherMachineID(strings.ReplaceAll(id, "-", ""))
}

// windowsVendors are the manufacturer and model prefixes a virtual machine's
// firmware names, each with systemd-detect-virt's name for its hypervisor:
// the table systemd itself reads SMBIOS's vendor strings against, so a
// Windows guest whose hypervisor hides its CPUID bit is still known by its
// firmware, as a Linux guest is. Only VirtualBox's was measured in the lab;
// the rest are as systemd lists them.
var windowsVendors = []struct{ prefix, hypervisor string }{
	{"innotek GmbH", "oracle"},
	{"VirtualBox", "oracle"},
	{"Oracle Corporation", "oracle"},
	{"VMware", "vmware"},
	{"VMW", "vmware"},
	{"QEMU", "qemu"},
	{"KVM", "kvm"},
	{"Xen", "xen"},
	{"Bochs", "bochs"},
	{"Parallels", "parallels"},
	{"BHYVE", "bhyve"},
	{"Amazon EC2", "amazon"},
	{"Google Compute Engine", "google"},
	{"Apple Virtualization", "apple"},
}

// windowsGuest is the hypervisor a Windows guest's manufacturer or model
// names, by systemd-detect-virt's names, or "" for one not recognized.
// Hyper-V names itself as a model, Virtual Machine by Microsoft Corporation.
func windowsGuest(manufacturer, model string) string {
	if manufacturer == "Microsoft Corporation" && model == "Virtual Machine" {
		return "microsoft"
	}
	for _, v := range windowsVendors {
		if strings.HasPrefix(manufacturer, v.prefix) || strings.HasPrefix(model, v.prefix) {
			return v.hypervisor
		}
	}
	return ""
}

// parseWindowsHardware reads the hardware script's object: each fact as
// parseWindowsObject reads it, the UUID lowercased and a placeholder UUID or
// serial left out, and hardware.virtualization from the raw values, which
// are not themselves kept. A container is container:windows; a recognized
// guest is vm:<hypervisor>, or cloud:<provider> for a cloud's; a host with
// no hypervisor present and no virtual firmware is physical; and a
// hypervisor present under firmware not recognized makes no fact, since a
// physical host running Hyper-V or virtualization-based security reports a
// hypervisor too.
func parseWindowsHardware(out string) []gatherFact {
	var kept []gatherFact
	var manufacturer, model string
	present, presentKnown, container := false, false, false
	for _, f := range parseWindowsObject(out) {
		s, _ := f.value.(string)
		switch f.key {
		case gatherFactSystemUUID, gatherFactSerial:
			if fact, ok := identityFact(f.key, s); ok {
				kept = append(kept, fact)
			}
			continue
		case windowsManufacturerKey:
			manufacturer = s
			continue
		case windowsModelKey:
			model = s
			continue
		case windowsHypervisorPresentKey:
			present, presentKnown = f.value.(bool)
			continue
		case windowsInContainerKey:
			container, _ = f.value.(bool)
			continue
		}
		kept = append(kept, f)
	}
	switch guest := windowsGuest(manufacturer, model); {
	case container:
		// The script reads no identity in a container; drop any that came
		// all the same, so a container never stores its host's.
		return append(withoutIdentity(kept), gatherFact{key: gatherFactVirtualization, value: "container:windows"})
	case cloudProviders[guest]:
		return append(kept, gatherFact{key: gatherFactVirtualization, value: "cloud:" + guest})
	case guest != "":
		return append(kept, vmFact(guest)...)
	case presentKnown && !present:
		return append(kept, gatherFact{key: gatherFactVirtualization, value: "physical"})
	}
	return kept
}

// withoutIdentity is facts with the hardware UUID and serial left out.
func withoutIdentity(facts []gatherFact) []gatherFact {
	var kept []gatherFact
	for _, f := range facts {
		if f.key != gatherFactSystemUUID && f.key != gatherFactSerial {
			kept = append(kept, f)
		}
	}
	return kept
}

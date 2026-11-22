// Package facts: the identity facts of a Windows host (Phase 41, base), the
// same four keys Linux reports (gather_identity.go), as a lab guest printed
// them. The machine id is the install's MachineGuid in the registry, as 32
// lowercase hexadecimal digits so it reads in the shape every other
// platform's does; the UUID and serial are the firmware's, from
// Win32_ComputerSystemProduct (SMBIOS's system information); and
// virtualization is read from the computer's manufacturer and model, with
// whether a hypervisor is present. A clone made without generalizing shares
// its source's MachineGuid, as a Linux clone shares /etc/machine-id.
package facts

import "strings"

// The min and hardware scripts' keys that are not facts: the raw values the
// identity parsers read and never emit.
const (
	windowsManufacturerKey      = "computer_manufacturer"
	windowsModelKey             = "computer_model"
	windowsHypervisorPresentKey = "hypervisor_present"
)

// windowsRawKeys are every key the min and hardware scripts print that is
// read by a parser rather than emitted.
var windowsRawKeys = map[string]bool{
	windowsArchitectureKey:      true,
	windowsManufacturerKey:      true,
	windowsModelKey:             true,
	windowsHypervisorPresentKey: true,
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

// windowsGuest is the hypervisor a Windows guest's manufacturer and model
// name, by systemd-detect-virt's names, or "" for one not recognized.
// VirtualBox was measured in the lab; Hyper-V and VMware name themselves as
// their documentation says.
func windowsGuest(manufacturer, model string) string {
	switch {
	case model == "VirtualBox" || manufacturer == "innotek GmbH":
		return "oracle"
	case manufacturer == "Microsoft Corporation" && model == "Virtual Machine":
		return "microsoft"
	case strings.HasPrefix(manufacturer, "VMware") || strings.HasPrefix(model, "VMware"):
		return "vmware"
	}
	return ""
}

// parseWindowsHardware reads the hardware script's object: each fact as
// parseWindowsObject reads it, the UUID lowercased and a placeholder UUID or
// serial left out, and hardware.virtualization from the raw manufacturer,
// model and hypervisor flag, which are not themselves kept. A recognized
// guest is vm:<hypervisor>; a host with no hypervisor present is physical;
// and a hypervisor present under a model not recognized makes no fact, since
// a physical host running Hyper-V or virtualization-based security reports a
// hypervisor too.
func parseWindowsHardware(out string) []gatherFact {
	var kept []gatherFact
	var manufacturer, model string
	present, presentKnown := false, false
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
		}
		kept = append(kept, f)
	}
	if guest := windowsGuest(manufacturer, model); guest != "" {
		return append(kept, vmFact(guest)...)
	}
	if presentKnown && !present {
		return append(kept, gatherFact{key: gatherFactVirtualization, value: "physical"})
	}
	return kept
}

// This file holds the win.cpu.* Collection: a Windows host's processors,
// reached over WinRM.
package catalogdata

import (
	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/collectionscaffold"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

var winCPUCollections = []collectionscaffold.Config{
	{
		Name:          "win.cpu.topology",
		Capabilities:  []capability.Name{capability.NameWindowsShell},
		Transports:    []string{"winrm"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary:     "Reports a Windows host's logical processors: which are performance cores, which efficiency cores, and how they group.",
			Description: "Reads the host's CPU sets as its scheduler sees them, through the Windows API GetSystemCpuSetInformation, and changes nothing. Each logical processor is reported with its processor group, core, NUMA node, last-level cache and efficiency class. On a hybrid processor, such as Intel's with P-cores and E-cores, a higher efficiency class is a faster, less power-efficient core: the logical processors with the highest class are reported as performance processors and the rest as efficiency processors, with the affinity mask that selects the performance ones. On a processor whose cores are all alike, every logical processor is a performance one. No WMI class reports efficiency classes, so the host compiles a small call to the API with PowerShell's Add-Type; a host whose PowerShell runs in Constrained Language Mode refuses that, and the task fails saying so. A check is the same read.",
			Params: []collection.Param{
				{Name: "timeout", Type: "int", Default: "120", Description: "How many seconds the read may take."},
			},
			Returns: []collection.ReturnField{
				{Name: "processors", Type: "list", Returned: "always", Description: "Each logical processor as index, group, core, numa_node, last_level_cache, efficiency_class and parked, in the order Windows lists them."},
				{Name: "processor_count", Type: "int", Returned: "always", Description: "How many logical processors the host has."},
				{Name: "core_count", Type: "int", Returned: "always", Description: "How many cores they are on."},
				{Name: "hybrid", Type: "bool", Returned: "always", Description: "Whether the cores are of more than one efficiency class."},
				{Name: "performance_processors", Type: "list", Returned: "always", Description: "The indexes of the logical processors of the highest efficiency class: every one, when the host is not hybrid."},
				{Name: "efficiency_processors", Type: "list", Returned: "always", Description: "The indexes of the rest, empty when the host is not hybrid."},
				{Name: "performance_affinity_mask", Type: "string", Returned: "when the host has one processor group", Description: "The affinity mask selecting the performance processors, as hexadecimal, such as 0xC03C3: what Windows' start /affinity and a process's ProcessorAffinity take."},
			},
			Examples: []collection.Example{
				{Name: "Find a host's performance cores", RunbookYAML: "- name: Which of the lab host's cores are P-cores\n  win.cpu.topology: {}\n  register: cpus\n"},
			},
			SeeAlso: []string{"facts.gather", "virt.vbox.vm.start"},
		},
	},
}

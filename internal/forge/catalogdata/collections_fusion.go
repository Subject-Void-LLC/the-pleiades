// This file holds the virt.fusion.* Collection: VMs on a Mac that runs VMware
// Fusion, reached over SSH, the way the Mac already is.
package catalogdata

import (
	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/collectionscaffold"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

var fusionCollections = []collectionscaffold.Config{
	{
		Name:          "virt.fusion.vm.info",
		Capabilities:  []capability.Name{capability.NameVMwareFusion},
		Transports:    []string{"ssh"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary:     "Reports a VMware Fusion VM's power state and its guest's address.",
			Description: "Reads a VM on a Mac running VMware Fusion and changes nothing: its power state (vmcli's power query), the identifiers and hardware its vmx keys name (vmcli's ConfigParams query, an allowlist, never a key's raw value, since a vmx can carry a secret), its snapshots (vmrun listSnapshots), and when it is on VMware Tools' state and the guest's address (vmrun getGuestIPAddress). A read past the power state that fails is named under warnings and the rest is returned. A VM whose vmx file is not there is not a failure: exists is false and nothing else is reported, so a later task can decide what to do. The task's target is the Mac (a device onboarding found VMware Fusion answering on), not the VM, which is a resource on it, named by the path of its vmx file. A check is the same read.",
			Params: []collection.Param{
				{Name: "vmx", Type: "string", Required: true, Description: "The absolute path of the VM's .vmx file on the Mac, such as /Users/me/pleiades-vms/win11-arm/win11-arm.vmx. Spaces are allowed; a path that is not absolute, does not end in .vmx, or holds a control character is refused."},
			},
			Returns: []collection.ReturnField{
				{Name: "exists", Type: "bool", Returned: "always", Description: "Whether the VM's vmx file is on the Mac."},
				{Name: "state", Type: "string", Returned: "when exists", Description: "Fusion's power state for it: on, off, suspended or paused."},
				{Name: "guest_ip", Type: "string", Returned: "when the VM is on and VMware Tools in its guest answers", Description: "The guest's IP address, as VMware Tools reports it."},
				{Name: "display_name", Type: "string", Returned: "when exists and the vmx sets it", Description: "The name Fusion shows for the VM (displayName)."},
				{Name: "guest_os", Type: "string", Returned: "when exists and the vmx sets it", Description: "The guest type the vmx declares (guestOS), such as arm-windows11-64."},
				{Name: "memory_mb", Type: "int", Returned: "when exists and the vmx sets it", Description: "Its memory in megabytes (memsize)."},
				{Name: "cpus", Type: "int", Returned: "when exists and the vmx sets it", Description: "Its virtual CPU count (numvcpus); a vmx that leaves it out runs one."},
				{Name: "firmware", Type: "string", Returned: "when exists and the vmx sets it", Description: "What it boots with: efi or bios."},
				{Name: "hardware_version", Type: "int", Returned: "when exists and the vmx sets it", Description: "Its virtual hardware version (virtualHW.version)."},
				{Name: "nics", Type: "list of dict", Returned: "when exists and the configuration was read", Description: "Each Ethernet adapter the vmx names and does not mark absent, up to 10: number, and connection_type, virtual_dev, address_type and mac when the vmx sets them."},
				{Name: "disks", Type: "list of dict", Returned: "when exists and the configuration was read", Description: "Each disk or drive file the vmx names, up to 32, sorted by slot: slot (nvme0:0), file, and device_type when set."},
				{Name: "snapshots", Type: "list of string", Returned: "when exists and the snapshots were read", Description: "The VM's snapshot names, oldest first."},
				{Name: "tools_state", Type: "string", Returned: "when the VM is on", Description: "VMware Tools in the guest as vmrun names it: running, installed or unknown."},
				{Name: "warnings", Type: "list of string", Returned: "when a read beyond the power state failed", Description: "Which read failed (the configuration, the snapshots or VMware Tools' state) and the tool's words; the rest is still returned."},
			},
			Examples: []collection.Example{
				{Name: "Read a VM", RunbookYAML: "- name: Read the Windows VM\n  virt.fusion.vm.info:\n    vmx: /Users/me/pleiades-vms/win11-arm/win11-arm.vmx\n  register: vm\n"},
			},
			SeeAlso: []string{"virt.fusion.vm.start", "virt.fusion.vm.stop"},
		},
	},
	{
		Name:          "virt.fusion.vm.start",
		Capabilities:  []capability.Name{capability.NameVMwareFusion},
		Transports:    []string{"ssh"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary:     "Starts a VMware Fusion VM with no window, and holds the Mac awake while it runs.",
			Description: "Makes sure a VM is running, started headless with vmrun. A VM that is already on reports no change. A suspended VM resumes where it was. A paused VM is refused, since starting is not resuming. Unless keep_awake is false, the Mac is held awake for as long as the VM's process runs (caffeinate -w on that process): macOS throttles a VM started from an SSH session as background work, and a Windows guest started that way ran about five times slower than its own clock until it was held awake. vmrun's output is read from a file rather than a pipe, because the VM process it starts keeps that output open for its whole life. The task's target is the Mac (a device onboarding found VMware Fusion answering on), not the VM, which is a resource on it, named by the path of its vmx file. A check reads the VM and sends nothing.",
			Params: []collection.Param{
				{Name: "vmx", Type: "string", Required: true, Description: "The absolute path of the VM's .vmx file on the Mac, such as /Users/me/pleiades-vms/win11-arm/win11-arm.vmx. Spaces are allowed; a path that is not absolute, does not end in .vmx, or holds a control character is refused."},
				{Name: "keep_awake", Type: "bool", Default: "true", Description: "Hold the Mac awake while the VM runs. Set it false only for a Mac that is kept awake some other way: a throttled VM runs, slowly, and can stall its own guest's work."},
			},
			Returns: []collection.ReturnField{
				{Name: "state", Type: "string", Returned: "always", Description: "The VM's power state after this task, read back from the Mac."},
				{Name: "kept_awake", Type: "bool", Returned: "always", Description: "Whether this task started holding the Mac awake for the VM. False when nothing was started."},
				{Name: "vmx_pid", Type: "int", Returned: "when kept_awake", Description: "The process ID of the VM's vmware-vmx, which the hold on the Mac lasts as long as."},
				{Name: "diff", Type: "dict", Returned: "always", Description: "The VM's power state before this task and after it."},
			},
			Examples: []collection.Example{
				{Name: "Start a VM", RunbookYAML: "- name: Start the Windows VM\n  virt.fusion.vm.start:\n    vmx: /Users/me/pleiades-vms/win11-arm/win11-arm.vmx\n"},
			},
			SeeAlso: []string{"virt.fusion.vm.stop", "virt.fusion.vm.info"},
		},
	},
	{
		Name:          "virt.fusion.vm.stop",
		Capabilities:  []capability.Name{capability.NameVMwareFusion},
		Transports:    []string{"ssh"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary:     "Stops or suspends a VMware Fusion VM.",
			Description: "Makes sure a VM is no longer running. Mode soft asks the guest to shut down, and needs VMware Tools running in the guest: vmrun refuses it without them. Mode hard powers it off at once, as pulling the plug would. Mode suspend writes its memory to disk, and virt.fusion.vm.start resumes it where it was. A VM that is already off reports no change, and so does a suspended one when the mode is suspend. A suspended VM asked for soft or hard is refused, since it is not running, and so is a paused one. The task's target is the Mac (a device onboarding found VMware Fusion answering on), not the VM, which is a resource on it, named by the path of its vmx file. A check reads the VM and sends nothing.",
			Params: []collection.Param{
				{Name: "vmx", Type: "string", Required: true, Description: "The absolute path of the VM's .vmx file on the Mac, such as /Users/me/pleiades-vms/win11-arm/win11-arm.vmx. Spaces are allowed; a path that is not absolute, does not end in .vmx, or holds a control character is refused."},
				{Name: "mode", Type: "string", Default: "soft", Choices: []string{"soft", "hard", "suspend"}, Description: "How to stop it: soft (the guest shuts down), hard (powered off at once) or suspend (its memory written to disk)."},
			},
			Returns: []collection.ReturnField{
				{Name: "state", Type: "string", Returned: "always", Description: "The VM's power state after this task, read back from the Mac."},
				{Name: "diff", Type: "dict", Returned: "always", Description: "The VM's power state before this task and after it."},
			},
			Examples: []collection.Example{
				{Name: "Suspend a VM", RunbookYAML: "- name: Put the Windows VM away\n  virt.fusion.vm.stop:\n    vmx: /Users/me/pleiades-vms/win11-arm/win11-arm.vmx\n    mode: suspend\n"},
			},
			SeeAlso: []string{"virt.fusion.vm.start", "virt.fusion.vm.info"},
		},
	},
}

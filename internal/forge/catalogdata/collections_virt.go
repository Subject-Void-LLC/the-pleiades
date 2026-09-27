// This file holds the virt.vbox.* Collection: VMs and snapshots on a host
// that runs Oracle VirtualBox, reached the way the host already is.
package catalogdata

import (
	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/collectionscaffold"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// vboxHostParamNote is what every virt.vbox method says about its target.
const vboxHostParamNote = " The task's target is the VirtualBox host (a device with virtualbox: true), not the VM, which is a resource on it."

// vmNameParam is the VM name parameter every VM method takes.
var vmNameParam = collection.Param{Name: "name", Type: "string", Required: true,
	Description: "The VM's name on the host. It must start with a letter or digit and hold only letters, digits, '.', '_' and '-', at most 63 characters; any other name is refused rather than quoted."}

// snapshotVMParam is the VM a snapshot method acts on.
var snapshotVMParam = collection.Param{Name: "vm", Type: "string", Required: true,
	Description: "The VM whose snapshot this is, named as for virt.vbox.vm.start."}

// snapshotNameParam is a snapshot's name.
var snapshotNameParam = collection.Param{Name: "name", Type: "string", Required: true,
	Description: "The snapshot's name, under the same rule as a VM's. VirtualBox lets two snapshots share a name; virt.vbox.snapshot.take never makes a second, and the methods that find a snapshot by name refuse one that matches more than one unless uuid says which."}

// snapshotUUIDParam picks one of several snapshots sharing a name.
var snapshotUUIDParam = collection.Param{Name: "uuid", Type: "string",
	Description: "The snapshot's UUID, needed only when more than one snapshot of the VM has this name (which only VirtualBox itself, or a tool other than this one, makes). It must be one of them."}

var virtCollections = []collectionscaffold.Config{
	{
		Name:          "virt.vbox.vm.info",
		Capabilities:  []capability.Name{capability.NameVirtualBox},
		Transports:    []string{"winrm"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary: "Reports a VirtualBox VM's state, hardware and snapshots.",
			Description: "Reads a VM on a VirtualBox host with VBoxManage showvminfo and changes nothing. A VM that does not exist is not a failure: exists is false and nothing else is reported, so a later task can decide what to do." +
				vboxHostParamNote + " A check is the same read.",
			Params: []collection.Param{vmNameParam},
			Returns: []collection.ReturnField{
				{Name: "exists", Type: "bool", Returned: "always", Description: "Whether a VM with this name is registered on the host."},
				{Name: "uuid", Type: "string", Returned: "when exists", Description: "The VM's UUID."},
				{Name: "state", Type: "string", Returned: "when exists", Description: "VirtualBox's state for it: poweroff, running, saved, paused, aborted, or another VirtualBox reports."},
				{Name: "memory_mb", Type: "int", Returned: "when exists", Description: "Its memory, in megabytes."},
				{Name: "cpus", Type: "int", Returned: "when exists", Description: "Its virtual CPU count."},
				{Name: "autostart_enabled", Type: "bool", Returned: "when exists", Description: "Whether it is marked to start with the host account's autostart service. virt.vbox.vm.start sets this on a Windows host and virt.vbox.vm.stop clears it."},
				{Name: "snapshots", Type: "list", Returned: "when exists", Description: "Each snapshot as name, uuid and description, a parent before its children."},
				{Name: "current_snapshot_uuid", Type: "string", Returned: "when exists", Description: "The snapshot the VM's state descends from, or empty when it has none."},
			},
			Examples: []collection.Example{
				{Name: "Read a VM", RunbookYAML: "- name: Read the lab VM\n  virt.vbox.vm.info:\n    name: ubuntu-lab\n  register: vm\n"},
			},
			SeeAlso: []string{"virt.vbox.vm.start", "virt.vbox.snapshot.take"},
		},
	},
	{
		Name:          "virt.vbox.vm.start",
		Capabilities:  []capability.Name{capability.NameVirtualBox},
		Transports:    []string{"winrm"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary: "Starts a VirtualBox VM with no window.",
			Description: "Makes sure a VM is running, started headless. A VM that is already running reports no change. A paused VM is refused, since starting is not resuming." +
				vboxHostParamNote +
				" On a Windows host, a VM cannot start from the WinRM logon this task uses: Windows' catalog signature check fails for a non-administrator logon that is not interactive, and VirtualBox's hardening then refuses the hypervisor (VirtualBox ticket 20341). So when none of the account's VMs is running, the VM is marked for autostart and started by the account's VirtualBox autostart service, which runs under a service logon; examples/windows_lab/winrm-cert-setup.ps1 -VirtualBoxAutostart installs it. While any of the account's VMs runs, a plain start works, because every client is then handed the VirtualBox server that VM keeps alive. The autostart mark stays on while the VM runs, since VirtualBox refuses to change a running VM's settings, so a host restart in that time starts it again; virt.vbox.vm.stop clears it." +
				" A check reads the VM and sends nothing.",
			Params: []collection.Param{vmNameParam},
			Returns: []collection.ReturnField{
				{Name: "state", Type: "string", Returned: "always", Description: "The VM's state after this task, read back from the host."},
				{Name: "via_autostart_service", Type: "bool", Returned: "always", Description: "Whether the start went through the account's autostart service. False when nothing was started."},
				{Name: "diff", Type: "dict", Returned: "always", Description: "The VM's state before this task and after it."},
			},
			Examples: []collection.Example{
				{Name: "Start a VM", RunbookYAML: "- name: Start the lab VM\n  virt.vbox.vm.start:\n    name: ubuntu-lab\n"},
			},
			SeeAlso: []string{"virt.vbox.vm.stop", "virt.vbox.vm.info"},
		},
	},
	{
		Name:          "virt.vbox.vm.stop",
		Capabilities:  []capability.Name{capability.NameVirtualBox},
		Transports:    []string{"winrm"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary: "Stops a VirtualBox VM, by its power button or by cutting its power.",
			Description: "Makes sure a VM is not running. A VM that is already off, saved or aborted reports no change. mode acpi presses the VM's power button and waits for the guest to shut itself down, failing, rather than cutting the power, if it has not within timeout; mode poweroff stops it at once, as pulling its plug would, and loses whatever the guest had not written. Once the VM is off, its autostart mark is cleared, which virt.vbox.vm.start sets on a Windows host." +
				vboxHostParamNote + " A check reads the VM and sends nothing.",
			Params: []collection.Param{
				vmNameParam,
				{Name: "mode", Type: "string", Default: "acpi", Choices: []string{"acpi", "poweroff"}, Description: "acpi asks the guest to shut down; poweroff cuts its power."},
				{Name: "timeout", Type: "int", Default: "120", Description: "With mode acpi, how many seconds to wait for the guest to shut down before failing."},
			},
			Returns: []collection.ReturnField{
				{Name: "state", Type: "string", Returned: "always", Description: "The VM's state after this task, read back from the host."},
				{Name: "diff", Type: "dict", Returned: "always", Description: "The VM's state before this task and after it."},
			},
			Examples: []collection.Example{
				{Name: "Shut a VM down", RunbookYAML: "- name: Shut the lab VM down\n  virt.vbox.vm.stop:\n    name: ubuntu-lab\n"},
				{Name: "Cut a VM's power", RunbookYAML: "- name: Power the lab VM off now\n  virt.vbox.vm.stop:\n    name: ubuntu-lab\n    mode: poweroff\n"},
			},
			SeeAlso: []string{"virt.vbox.vm.start"},
		},
	},
	{
		Name:          "virt.vbox.snapshot.take",
		Capabilities:  []capability.Name{capability.NameVirtualBox},
		Transports:    []string{"winrm"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary: "Takes a snapshot of a VirtualBox VM under a name no other snapshot of it has.",
			Description: "Makes sure a VM has a snapshot with this name. A snapshot already under the name reports no change and takes no second one, although VirtualBox itself would. A running VM can be snapshotted; a snapshot of a running VM holds its memory too." +
				vboxHostParamNote + " A check reads the VM and sends nothing.",
			Params: []collection.Param{
				snapshotVMParam, snapshotNameParam,
				{Name: "description", Type: "string", Description: "Text stored with the snapshot, at most 200 characters on one line."},
			},
			Returns: []collection.ReturnField{
				{Name: "uuid", Type: "string", Returned: "always", Description: "The snapshot's UUID: the one taken, or the one already under the name."},
				{Name: "diff", Type: "dict", Returned: "always", Description: "Whether a snapshot under the name existed before this task and after it."},
			},
			Examples: []collection.Example{
				{Name: "Snapshot a clean install", RunbookYAML: "- name: Keep the clean install\n  virt.vbox.snapshot.take:\n    vm: ubuntu-lab\n    name: clean\n    description: fresh install, before any test\n"},
			},
			SeeAlso: []string{"virt.vbox.snapshot.restore", "virt.vbox.snapshot.delete"},
		},
	},
	{
		Name:          "virt.vbox.snapshot.restore",
		Capabilities:  []capability.Name{capability.NameVirtualBox},
		Transports:    []string{"winrm"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary: "Puts a VirtualBox VM back to a snapshot, discarding its current state.",
			Description: "Restores a VM to a snapshot. Everything the VM did since that snapshot is discarded, so this always reports a change and cannot be undone. A running VM is refused: stop it first with virt.vbox.vm.stop, so that discarding a running machine is a decision a runbook states rather than a side effect. A snapshot that does not exist is refused." +
				vboxHostParamNote + " A check reads the VM and sends nothing.",
			Params: []collection.Param{snapshotVMParam, snapshotNameParam, snapshotUUIDParam},
			Returns: []collection.ReturnField{
				{Name: "uuid", Type: "string", Returned: "always", Description: "The snapshot restored."},
			},
			Examples: []collection.Example{
				{Name: "Reset to a clean install", RunbookYAML: "- name: Stop the lab VM\n  virt.vbox.vm.stop:\n    name: ubuntu-lab\n    mode: poweroff\n\n- name: Back to the clean install\n  virt.vbox.snapshot.restore:\n    vm: ubuntu-lab\n    name: clean\n\n- name: Start it again\n  virt.vbox.vm.start:\n    name: ubuntu-lab\n"},
			},
			SeeAlso: []string{"virt.vbox.snapshot.take", "virt.vbox.vm.stop"},
		},
	},
	{
		Name:          "virt.vbox.snapshot.delete",
		Capabilities:  []capability.Name{capability.NameVirtualBox},
		Transports:    []string{"winrm"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary: "Deletes a snapshot of a VirtualBox VM.",
			Description: "Makes sure a VM has no snapshot with this name. None under the name reports no change. The snapshot's saved state is merged away and cannot be brought back, so this cannot be undone." +
				vboxHostParamNote + " A check reads the VM and sends nothing.",
			Params: []collection.Param{snapshotVMParam, snapshotNameParam, snapshotUUIDParam},
			Returns: []collection.ReturnField{
				{Name: "uuid", Type: "string", Returned: "when a snapshot was deleted", Description: "The snapshot deleted."},
				{Name: "diff", Type: "dict", Returned: "always", Description: "Whether a snapshot under the name existed before this task and after it."},
			},
			Examples: []collection.Example{
				{Name: "Drop a snapshot", RunbookYAML: "- name: Drop the old baseline\n  virt.vbox.snapshot.delete:\n    vm: ubuntu-lab\n    name: clean\n"},
			},
			SeeAlso: []string{"virt.vbox.snapshot.take"},
		},
	},
}

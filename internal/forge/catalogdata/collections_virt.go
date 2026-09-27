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
	}, {
		Name:          "virt.vbox.vm.import_ova",
		Capabilities:  []capability.Name{capability.NameVirtualBox},
		Transports:    []string{"winrm"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary: "Imports an OVA appliance on a VirtualBox host as a VM, with no network adapter.",
			Description: "Makes sure a VM of this name exists, importing it from an OVA file already on the host (win.file.download fetches one) into the host's vm_folder. A VM already under the name reports no change, and is not compared with the file. The imported VM is left with no network adapter, whatever the appliance asked for: Ubuntu's cloud image asks for a bridged one, which would put the VM on the host's own network. A VM made from it (virt.vbox.vm.clone) is given the networks it is meant to have. The VM is not started, and is meant as a base to snapshot and clone rather than to boot." +
				vboxHostParamNote + " A check reads the host's VMs and sends nothing.",
			Params: []collection.Param{
				vmNameParam,
				{Name: "path", Type: "string", Required: true, Description: "The OVA file's absolute path on the host. It may not hold a quote, a wildcard or a control character."},
			},
			Returns: []collection.ReturnField{
				{Name: "uuid", Type: "string", Returned: "when not a check that would import", Description: "The VM's UUID."},
				{Name: "diff", Type: "dict", Returned: "always", Description: "Whether a VM of the name existed before this task and after it."},
			},
			Examples: []collection.Example{
				{Name: "Import the Ubuntu cloud image", RunbookYAML: "- name: Import the Ubuntu 24.04 cloud image as a base\n  virt.vbox.vm.import_ova:\n    name: ubuntu-2404-base\n    path: G:\\PleiadesLab\\ubuntu-24.04-server-cloudimg-amd64.ova\n"},
			},
			SeeAlso: []string{"win.file.download", "virt.vbox.snapshot.take", "virt.vbox.vm.clone"},
		},
	},
	{
		Name:          "virt.vbox.vm.clone",
		Capabilities:  []capability.Name{capability.NameVirtualBox},
		Transports:    []string{"winrm"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary: "Makes a VM as a linked clone of another's snapshot, seeded by cloud-init with a device's login.",
			Description: "Makes sure a VM of this name exists, creating it as a linked clone of a snapshot of another VM, so it takes little space and starts from that snapshot's disk. A VM already under the name reports no change and is not reconfigured or reseeded. The new VM gets the memory and CPUs asked for, a NAT adapter for the internet, a host-only adapter at a fixed address, a serial console written to console.log in its folder (virt.vbox.vm.host_keys reads its SSH host keys from there), and a cloud-init NoCloud seed on its DVD drive." +
				" The seed is built by Pleiades, not on the host, and reaches the host on the command's standard input, never on a command line. It carries login's user name, the public half of its key and a salted hash of its password, taken from the vault by pleiades add-credential login --generate; never the key or the password. SSH then admits the key only; the password is for the VM's console. A login of root may log in by key; any other user gets passwordless sudo. The seed ISO stays in the VM's folder, holding that hash, until virt.vbox.vm.delete removes both. The address and login are recorded on the VM as VirtualBox extradata (pleiades/address, pleiades/device), which virt.vbox.vm.list reports, since VirtualBox cannot know a guest's address without its Guest Additions. The VM is not started." +
				vboxHostParamNote + " A check reads the host's VMs and the snapshot, and sends nothing.",
			Params: []collection.Param{
				vmNameParam,
				{Name: "from", Type: "string", Required: true, Description: "The VM to clone, named as name is."},
				{Name: "snapshot", Type: "string", Required: true, Description: "The snapshot of from to clone. Exactly one of from's snapshots may have this name."},
				{Name: "login", Type: "string", Required: true, Description: "The inventory device whose stored login the VM is seeded with: its user name, its key's public half and a hash of its password. Usually the device that stands for this VM."},
				{Name: "address", Type: "string", Required: true, Description: "The host-only adapter's address with its prefix, as 192.168.56.10/24. Keep it out of the host-only DHCP server's range."},
				{Name: "hostname", Type: "string", Description: "The VM's host name. Defaults to name, which must then be a valid host name."},
				{Name: "memory_mb", Type: "int", Default: "1024", Description: "Its memory, in megabytes."},
				{Name: "cpus", Type: "int", Default: "1", Description: "Its virtual CPU count."},
				{Name: "host_only_adapter", Type: "string", Default: "VirtualBox Host-Only Ethernet Adapter", Description: "The host's host-only adapter, as VBoxManage list hostonlyifs names it."},
			},
			Returns: []collection.ReturnField{
				{Name: "uuid", Type: "string", Returned: "when not a check that would clone", Description: "The VM's UUID, which is also its cloud-init instance ID."},
				{Name: "address", Type: "string", Returned: "always", Description: "The host-only address the VM was given, without its prefix."},
				{Name: "diff", Type: "dict", Returned: "always", Description: "Whether a VM of the name existed before this task and after it."},
			},
			Examples: []collection.Example{
				{Name: "Clone and start a lab VM", RunbookYAML: "- name: Make the lab VM from the base's clean snapshot\n  virt.vbox.vm.clone:\n    name: ubuntu-lab\n    from: ubuntu-2404-base\n    snapshot: base\n    login: ubuntu-lab\n    address: 192.168.56.10/24\n\n- name: Start it\n  virt.vbox.vm.start:\n    name: ubuntu-lab\n"},
			},
			SeeAlso: []string{"virt.vbox.vm.import_ova", "virt.vbox.vm.start", "virt.vbox.vm.host_keys", "virt.vbox.vm.delete", "virt.vbox.vm.list"},
		},
	},
	{
		Name:          "virt.vbox.vm.delete",
		Capabilities:  []capability.Name{capability.NameVirtualBox},
		Transports:    []string{"winrm"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary: "Deletes a stopped VirtualBox VM and its disks.",
			Description: "Makes sure no VM of this name exists. None reports no change. A running or paused VM is refused: stop it first with virt.vbox.vm.stop. The VM is unregistered and its disks deleted, along with a seed ISO or console log virt.vbox.vm.clone put in its folder; install media attached from anywhere else (a shared ISO) is detached, never deleted. A VM that others were linked-cloned from is refused by VirtualBox while they exist. This cannot be undone." +
				vboxHostParamNote + " A check reads the VM and sends nothing.",
			Params: []collection.Param{vmNameParam},
			Returns: []collection.ReturnField{
				{Name: "uuid", Type: "string", Returned: "when a VM was deleted", Description: "The VM deleted."},
				{Name: "diff", Type: "dict", Returned: "always", Description: "Whether a VM of the name existed before this task and after it."},
			},
			Examples: []collection.Example{
				{Name: "Throw a lab VM away", RunbookYAML: "- name: Power the lab VM off\n  virt.vbox.vm.stop:\n    name: ubuntu-lab\n    mode: poweroff\n\n- name: Delete it\n  virt.vbox.vm.delete:\n    name: ubuntu-lab\n"},
			},
			SeeAlso: []string{"virt.vbox.vm.clone", "virt.vbox.vm.stop"},
		},
	},
	{
		Name:          "virt.vbox.vm.host_keys",
		Capabilities:  []capability.Name{capability.NameVirtualBox},
		Transports:    []string{"winrm"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary: "Reads a VM's SSH host keys from what cloud-init printed on its serial console.",
			Description: "Waits for cloud-init to print the VM's SSH host keys on its serial console, which virt.vbox.vm.clone logs to a file on the host, and reports them. They are read over the host's own authenticated connection, not from the network the VM answers SSH on, so they are what a known_hosts file can trust before the first SSH connection: pleiades trust-host <device> --from-console <host> writes them there. The newest complete block is the one read. Nothing is changed." +
				vboxHostParamNote + " A check reads the console once, without waiting.",
			Params: []collection.Param{
				vmNameParam,
				{Name: "timeout", Type: "int", Default: "300", Description: "How many seconds to wait for the keys."},
			},
			Returns: []collection.ReturnField{
				{Name: "ssh_host_keys", Type: "list", Returned: "always", Description: "Each key as algorithm and base64, the form a known_hosts line holds after its host names. Empty in a check that found none yet."},
			},
			Examples: []collection.Example{
				{Name: "Wait for a new VM's host keys", RunbookYAML: "- name: Wait for the lab VM's first boot\n  virt.vbox.vm.host_keys:\n    name: ubuntu-lab\n  register: keys\n"},
			},
			SeeAlso: []string{"virt.vbox.vm.clone"},
		},
	}, {
		Name:          "virt.vbox.vm.list",
		Capabilities:  []capability.Name{capability.NameVirtualBox},
		Transports:    []string{"winrm"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary: "Lists the VMs on a VirtualBox host, with their state and the address Pleiades gave them.",
			Description: "Reports every VM registered on the host for the account Pleiades reaches it as: its name, UUID, state, memory, CPUs and autostart mark, and, for a VM virt.vbox.vm.clone made, the host-only address it was given and the inventory device whose login it was seeded with. VirtualBox keeps a separate list of VMs for each Windows account, so these are not the VMs a person sees in their own VirtualBox Manager, and theirs are not listed here. Nothing is changed." +
				vboxHostParamNote + " A check is the same read.",
			Returns: []collection.ReturnField{
				{Name: "vms", Type: "list", Returned: "always", Description: "Each VM as name, uuid, state, memory_mb, cpus, autostart_enabled, and address and device when Pleiades made it, in the order VirtualBox lists them."},
			},
			Examples: []collection.Example{
				{Name: "List the lab's VMs", RunbookYAML: "- name: What runs on the lab host\n  virt.vbox.vm.list: {}\n"},
			},
			SeeAlso: []string{"virt.vbox.vm.info", "virt.vbox.vm.clone"},
		},
	},
}

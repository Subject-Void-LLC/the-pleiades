// This file holds the virt.vbox.* Collection: VMs and snapshots on a host
// that runs Oracle VirtualBox, reached the way the host already is.
package catalogdata

import (
	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/collectionscaffold"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/vmsize"
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

// vmSizeNote is what the methods that size a VM say its sizes are.
var vmSizeNote = "a T-shirt size, which sets its CPUs and memory together as a cloud's instance type does: " + vmsize.Describe()

// osTypeParam is the guest OS type a VM made from nothing is given.
var osTypeParam = collection.Param{Name: "os_type", Type: "string", Required: true,
	Description: "The VM's guest OS type, as VBoxManage list ostypes names it: Windows2025_64 for Windows Server 2025, Windows2022_64 for 2022, Ubuntu_64 for Ubuntu. VirtualBox tunes the VM to it, and virt.vbox.vm.clone seeds a clone of a Windows type with a Windows answer file."}

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
				{Name: "size", Type: "string", Returned: "when exists", Description: "Its T-shirt size, read from its CPUs and memory: xsmall, small, medium, large or xlarge, or empty when they are not exactly one size's."},
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
				" A VM with more memory than the host has free, less 1024 MB kept for the host itself, is refused rather than started, since the host would page to find it; the host's free memory is read at the start." +
				" A check reads the VM and the host and sends nothing.",
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
		Name:          "virt.vbox.vm.resize",
		Capabilities:  []capability.Name{capability.NameVirtualBox},
		Transports:    []string{"winrm"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary: "Changes a stopped VirtualBox VM's CPUs and memory, by T-shirt size or by count.",
			Description: "Makes sure a VM has the CPUs and memory asked for: a size, or memory_mb and cpus, either of which alone leaves the other as it is. A VM that has them already reports no change. VirtualBox changes them only while a VM is powered off, so a running, paused or saved VM is refused: stop it first with virt.vbox.vm.stop. A size larger than the host is refused: more CPUs than it has processors online, or more memory than it has. The guest sees the change at its next boot. A run that changed the VM emits virt.vbox.vm.resize back to the CPUs and memory it had." +
				vboxHostParamNote + " A check reads the VM and the host, and sends nothing.",
			Params: []collection.Param{
				vmNameParam,
				{Name: "size", Type: "string", Choices: vmsize.Names(), Description: "The size to give the VM, " + vmSizeNote + ". Give size, or memory_mb, cpus or both, not size with either."},
				{Name: "memory_mb", Type: "int", Description: "The memory to give it, in megabytes."},
				{Name: "cpus", Type: "int", Description: "The virtual CPU count to give it."},
			},
			Returns: []collection.ReturnField{
				{Name: "memory_mb", Type: "int", Returned: "always", Description: "Its memory after this task, in megabytes (predicted, in a check)."},
				{Name: "cpus", Type: "int", Returned: "always", Description: "Its virtual CPU count after this task (predicted, in a check)."},
				{Name: "size", Type: "string", Returned: "always", Description: "Its T-shirt size after this task, or empty when its CPUs and memory are not exactly one size's."},
				{Name: "diff", Type: "dict", Returned: "always", Description: "Its memory_mb, cpus and size before this task and after it."},
			},
			Examples: []collection.Example{
				{Name: "Make a lab VM bigger", RunbookYAML: "- name: Shut the lab VM down\n  virt.vbox.vm.stop:\n    name: ubuntu-lab\n\n- name: Make it medium\n  virt.vbox.vm.resize:\n    name: ubuntu-lab\n    size: medium\n\n- name: Start it again\n  virt.vbox.vm.start:\n    name: ubuntu-lab\n"},
				{Name: "Give a VM more memory only", RunbookYAML: "- name: Double the build VM's memory\n  virt.vbox.vm.resize:\n    name: build-vm\n    memory_mb: 8192\n"},
			},
			SeeAlso: []string{"virt.vbox.vm.clone", "virt.vbox.vm.stop", "virt.vbox.vm.info"},
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
		Name:          "virt.vbox.vm.import_disk",
		Capabilities:  []capability.Name{capability.NameVirtualBox},
		Transports:    []string{"winrm"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary: "Makes a VirtualBox VM from a disk image on the host, such as Microsoft's Windows Server evaluation VHDX.",
			Description: "Makes sure a VM of this name exists, creating it from a disk image already on the host: a VHDX, VHD, VMDK or VDI file, such as the evaluation VHDX Microsoft publishes for Windows Server. The image is copied into the VM's folder as a VDI that grows as the guest writes to it, and the image itself is left as it was, so one image can make many VMs. The copy is the VM's disk, and virt.vbox.vm.delete removes it with the VM. The VM gets os_type; a SATA controller holding the disk and an IDE controller for the DVD a clone's seed goes in; network cards Windows has a driver for, with nothing attached; and the firmware the disk boots with, read from its first sectors: EFI for a GUID partition table, BIOS for a master boot record. A VM already under the name reports no change and is not compared with the image. The VM is not started, and is meant as a base to snapshot and clone. For Windows the image must be generalized (by sysprep), and virt.vbox.vm.clone then gives each clone its own name, address and password on a DVD. Microsoft's Windows Server evaluation VHDX is generalized but never reads that DVD at its first boot (measured on the lab host), so its clones stop at the first-boot screens; virt.vbox.vm.install makes a Windows base whose clones read theirs." +
				vboxHostParamNote + " A check reads the host's VMs and sends nothing; it does not read the disk, so it reports the firmware only when firmware names one.",
			Params: []collection.Param{
				vmNameParam,
				{Name: "path", Type: "string", Required: true, Description: "The disk image's absolute path on the host. It may not hold a quote, a wildcard or a control character."},
				osTypeParam,
				{Name: "firmware", Type: "string", Default: "auto", Choices: []string{"auto", "bios", "efi"}, Description: "What the VM boots with. auto reads the disk's first sectors: EFI for a GUID partition table, BIOS for a master boot record."},
				{Name: "timeout", Type: "int", Default: "3600", Description: "How many seconds the copy may take. A disk of many gigabytes takes minutes."},
			},
			Returns: []collection.ReturnField{
				{Name: "uuid", Type: "string", Returned: "when not a check that would import", Description: "The VM's UUID."},
				{Name: "firmware", Type: "string", Returned: "when not a check with firmware auto", Description: "What the VM boots with: bios or efi."},
				{Name: "diff", Type: "dict", Returned: "always", Description: "Whether a VM of the name existed before this task and after it."},
			},
			Examples: []collection.Example{
				{Name: "Make a Windows Server base from Microsoft's VHDX", RunbookYAML: "- name: Make a Windows Server 2025 base from the evaluation VHDX\n  virt.vbox.vm.import_disk:\n    name: ws2025-base\n    path: G:\\iso\\server-2025-datacenter-eval.vhdx\n    os_type: Windows2025_64\n\n- name: Snapshot it, never booted\n  virt.vbox.snapshot.take:\n    vm: ws2025-base\n    name: base\n"},
			},
			SeeAlso: []string{"virt.vbox.vm.install", "virt.vbox.snapshot.take", "virt.vbox.vm.clone", "virt.vbox.vm.import_ova"},
		},
	},
	{
		Name:          "virt.vbox.vm.install",
		Capabilities:  []capability.Name{capability.NameVirtualBox},
		Transports:    []string{"winrm"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary: "Makes a Windows VM by installing Windows from its installation ISO, unattended, and generalizing it as a base to clone.",
			Description: "Makes sure a VM of this name exists, creating it by installing Windows from an installation ISO already on the host, with no one at the keyboard. The VM gets a new disk of disk_gb, BIOS firmware, the size asked for, os_type, and no network adapter, so setup downloads nothing. Its DVD drives hold the ISO and an answer file Pleiades builds (Autounattend.xml), which partitions the disk, installs image and accepts its license, then takes the new Windows through audit mode, where it deletes the copy of itself Windows cached, points the registry (HKLM\\SYSTEM\\Setup, UnattendFile) at D:\\Autounattend.xml, and runs sysprep to generalize the installation and shut it down. A generalized Windows looks for its answer file at first boot only there and in its own folders, never on a DVD, so the pointer is how a clone reads its seed. The task waits for the shutdown: about six to eight minutes on the lab host. When the VM is off, both DVDs are taken out, the answer file is deleted, and the VM is marked installed (the extradata pleiades/installed). The answer file holds a password only for audit mode's sign-in, made at random for this install and kept nowhere, which Windows forgets when it is generalized. The VM is meant as a base to snapshot and clone: virt.vbox.vm.clone gives each clone its own name, address and password. A VM already under the name reports no change when it is marked installed, and is refused when it is not, since an install that did not finish is no base: delete it with virt.vbox.vm.delete and run again. An install still running at the timeout is left running, with a picture of its screen saved in its folder to show where it stopped." +
				vboxHostParamNote + " A check reads the host's VMs and memory, and sends nothing.",
			Params: []collection.Param{
				vmNameParam,
				{Name: "iso", Type: "string", Required: true, Description: "The installation ISO's absolute path on the host, such as Microsoft's Windows Server evaluation ISO. It is attached and then taken out again, never changed or deleted."},
				{Name: "image", Type: "string", Required: true, Description: "The edition to install: its name in the ISO's install.wim, such as Windows Server 2025 Standard Evaluation, which is Server Core, or Windows Server 2025 Datacenter Evaluation (Desktop Experience); or its index there, 1 for the first."},
				osTypeParam,
				{Name: "disk_gb", Type: "int", Default: "64", Description: "The size of the VM's new disk, in gigabytes. It grows on the host as Windows writes to it."},
				{Name: "size", Type: "string", Default: "medium", Choices: vmsize.Names(), Description: "The VM's size while it installs, " + vmSizeNote + ". Windows Server needs at least small."},
				{Name: "language", Type: "string", Default: "en-US", Description: "The installation's language and locale, as a tag such as en-US. The ISO must hold it."},
				{Name: "timeout", Type: "int", Default: "7200", Description: "How many seconds to wait for the install to finish and the VM to shut itself down."},
			},
			Returns: []collection.ReturnField{
				{Name: "uuid", Type: "string", Returned: "when not a check that would install", Description: "The VM's UUID."},
				{Name: "size", Type: "string", Returned: "always", Description: "The VM's T-shirt size, or empty when its CPUs and memory are not exactly one size's."},
				{Name: "diff", Type: "dict", Returned: "always", Description: "Whether a VM of the name existed before this task and after it."},
			},
			Examples: []collection.Example{
				{Name: "Install Windows Server Core as a base", RunbookYAML: "- name: Install Windows Server 2025 Core from the evaluation ISO\n  virt.vbox.vm.install:\n    name: ws2025-core-base\n    iso: G:\\iso\\server-2025-eval.iso\n    image: Windows Server 2025 Standard Evaluation\n    os_type: Windows2025_64\n\n- name: Snapshot the generalized install\n  virt.vbox.snapshot.take:\n    vm: ws2025-core-base\n    name: base\n"},
			},
			SeeAlso: []string{"virt.vbox.vm.import_disk", "virt.vbox.snapshot.take", "virt.vbox.vm.clone", "virt.vbox.vm.delete"},
		},
	},
	{
		Name:          "virt.vbox.vm.eject_seed",
		Capabilities:  []capability.Name{capability.NameVirtualBox},
		Transports:    []string{"winrm"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary: "Takes a VM's seed out of its DVD drive and deletes it, once its first boot has read it.",
			Description: "Makes sure a VM virt.vbox.vm.clone made no longer holds its seed: the cloud-init seed or Windows answer file clone put on its DVD drive and in its folder, which holds a hash of the login's password or, for Windows, the Administrator's password itself. The drive is emptied at once, even while the VM runs and even when the guest has locked it, so the guest can never read the seed again. The seed file is then deleted; but VirtualBox keeps an image locked for as long as the VM that held it runs (measured on the lab host), so on a running VM the file stays until the VM is stopped, the task says so in a warning and reports deleted false, and running it again once the VM is stopped deletes it. Run it once the first boot has read the seed: after virt.vbox.vm.host_keys finds a Linux VM's keys, or after wait.connection reaches a Windows VM. A VM holding no seed, in a drive or on the host, reports no change. The seed cannot be put back." +
				vboxHostParamNote + " A check reads the VM and sends nothing.",
			Params: []collection.Param{vmNameParam},
			Returns: []collection.ReturnField{
				{Name: "ejected", Type: "bool", Returned: "always", Description: "Whether a seed was taken out, or would be, in a check."},
				{Name: "deleted", Type: "bool", Returned: "when a seed was taken out", Description: "Whether the seed file was deleted from the host: false while the VM runs, which keeps it locked."},
				{Name: "diff", Type: "dict", Returned: "always", Description: "Whether the VM held its seed before this task and after it."},
			},
			Examples: []collection.Example{
				{Name: "Delete a Windows VM's answer file after its first boot", RunbookYAML: "- name: Take the lab VM's answer file out, now that it has booted\n  virt.vbox.vm.eject_seed:\n    name: win-lab\n"},
			},
			SeeAlso: []string{"virt.vbox.vm.clone", "virt.vbox.vm.host_keys", "wait.connection"},
		},
	},
	{
		Name:          "virt.vbox.vm.screenshot",
		Capabilities:  []capability.Name{capability.NameVirtualBox},
		Transports:    []string{"winrm"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary: "Saves a picture of a running VM's screen as a PNG file on the machine running Pleiades.",
			Description: "Takes a picture of a running VM's screen on the host, brings it back over the host's own connection, and writes it to dest on the machine running this task, as Ansible's fetch brings a file back; the host keeps no copy. It is how to see a VM with no window, such as one stopped at a first-boot screen or an installer's error. A VM that has not set a display mode yet (still in its firmware, or stopped before it drew anything) has no picture to take, and says so. The VM is not changed; dest is replaced when it exists." +
				vboxHostParamNote + " A check reads the VM, takes no picture and writes nothing.",
			Params: []collection.Param{
				vmNameParam,
				{Name: "dest", Type: "string", Required: true, Description: "Where to write the PNG on the machine running this task. Its folder must exist."},
			},
			Returns: []collection.ReturnField{
				{Name: "dest", Type: "string", Returned: "always", Description: "Where the picture was written, or would be, in a check."},
				{Name: "width", Type: "int", Returned: "when not a check", Description: "The picture's width, in pixels."},
				{Name: "height", Type: "int", Returned: "when not a check", Description: "The picture's height, in pixels."},
				{Name: "bytes", Type: "int", Returned: "when not a check", Description: "The PNG file's size."},
			},
			Examples: []collection.Example{
				{Name: "See where a Windows VM's first boot stopped", RunbookYAML: "- name: Save a picture of the lab VM's screen\n  virt.vbox.vm.screenshot:\n    name: win-lab\n    dest: ./win-lab.png\n"},
			},
			SeeAlso: []string{"virt.vbox.vm.log", "virt.vbox.vm.send_keys"},
		},
	},
	{
		Name:          "virt.vbox.vm.log",
		Capabilities:  []capability.Name{capability.NameVirtualBox},
		Transports:    []string{"winrm"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary: "Reads the end of a VM's VirtualBox log, optionally only the lines matching a pattern.",
			Description: "Reads the end of the log VirtualBox writes for a VM's current or last run (VBox.log in its folder's Logs), and reports its last lines, or its last lines matching pattern. The log says what the VM's firmware and VirtualBox did: where a boot stopped, which hypervisor interface the guest used, and why a VM aborted. Nothing is changed." +
				vboxHostParamNote + " A check reads the log as a run does.",
			Params: []collection.Param{
				vmNameParam,
				{Name: "lines", Type: "int", Default: "40", Description: "How many lines to report, from the end: 1 to 2000."},
				{Name: "pattern", Type: "string", Description: "Report only lines matching this Go RE2 regular expression, such as EFI|GIM|fatal."},
			},
			Returns: []collection.ReturnField{
				{Name: "lines", Type: "list", Returned: "always", Description: "The log's last lines, or its last lines matching pattern, oldest first. Empty when the VM has never run."},
				{Name: "path", Type: "string", Returned: "always", Description: "The log file's path on the host."},
			},
			Examples: []collection.Example{
				{Name: "See where a VM's firmware stopped", RunbookYAML: "- name: Read the lab VM's firmware lines\n  virt.vbox.vm.log:\n    name: win-lab\n    pattern: \"EFI|debug point\"\n    lines: 20\n"},
			},
			SeeAlso: []string{"virt.vbox.vm.screenshot", "virt.vbox.vm.info"},
		},
	},
	{
		Name:          "virt.vbox.vm.send_keys",
		Capabilities:  []capability.Name{capability.NameVirtualBox},
		Transports:    []string{"winrm"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary: "Types into a running VM's console, as its keyboard would.",
			Description: "Types keys into a running VM's console, written as Packer's boot_command writes them: text as it is, a named key between angle brackets (<enter>, <tab>, <esc>, <bs>, <del>, <spacebar>, <up>, <down>, <left>, <right>, <home>, <end>, <pageup>, <pagedown>, <insert>, <f1> to <f12>), and <wait> or <wait5> for a pause of one or five seconds (at most 60). Text is typed with a US keyboard layout and may hold only printable ASCII. It is for a VM with no other way in yet, such as one at a first-boot screen; see what it shows with virt.vbox.vm.screenshot. Never type a secret: text travels on the host's VBoxManage command line, where any process on the host can read it. This cannot be undone." +
				vboxHostParamNote + " A check reads the VM and the keys, and types nothing.",
			Params: []collection.Param{
				vmNameParam,
				{Name: "keys", Type: "string", Required: true, Description: "What to type, as Packer's boot_command writes it, such as <tab><tab><enter>."},
			},
			Returns: []collection.ReturnField{
				{Name: "strokes", Type: "int", Returned: "always", Description: "How many steps were typed, or would be, in a check: each run of text, named key and pause is one."},
			},
			Examples: []collection.Example{
				{Name: "Accept a first-boot screen", RunbookYAML: "- name: Move to the Accept button and press it\n  virt.vbox.vm.send_keys:\n    name: win-lab\n    keys: <tab><tab><enter>\n"},
			},
			SeeAlso: []string{"virt.vbox.vm.screenshot"},
		},
	},
	{
		Name:          "virt.vbox.vm.addresses",
		Capabilities:  []capability.Name{capability.NameVirtualBox},
		Transports:    []string{"winrm"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary: "Reports the addresses a VM's host-only adapters were given, from VirtualBox's DHCP server and from virt.vbox.vm.clone.",
			Description: "Reports each address a VM's host-only adapters have: the leases VirtualBox's own DHCP server handed them (read from the leases file it keeps for the host's account), and the fixed address virt.vbox.vm.clone gave the VM (the extradata pleiades/address). A VM whose first boot did not apply its fixed address shows the address DHCP gave it too, which is how to reach it anyway. A NAT adapter's address is private to the VM and is not reported. Nothing is changed." +
				vboxHostParamNote + " A check reads the same as a run.",
			Params: []collection.Param{vmNameParam},
			Returns: []collection.ReturnField{
				{Name: "addresses", Type: "list", Returned: "always", Description: "Each address as nic (the adapter's number), mac, adapter (the host's), address, source (dhcp or fixed), and for a DHCP lease its state (acked while held, expired after), issued and expires."},
				{Name: "address", Type: "string", Returned: "when there is one", Description: "The address to reach the VM at: the fixed one, or else a held DHCP lease's. A first boot that applied its fixed address leaves the lease it took before then marked held for a while, so a VM whose fixed address was never applied is known by that lease being the only one it answers at."},
			},
			Examples: []collection.Example{
				{Name: "Find a VM's address", RunbookYAML: "- name: Where the lab VM answers\n  virt.vbox.vm.addresses:\n    name: win-lab\n  register: found\n"},
			},
			SeeAlso: []string{"virt.vbox.vm.clone", "virt.vbox.vm.list"},
		},
	},
	{
		Name:          "virt.vbox.vm.clone",
		Capabilities:  []capability.Name{capability.NameVirtualBox},
		Transports:    []string{"winrm"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary: "Makes a VM as a linked clone of another's snapshot, seeded with a device's login by cloud-init or a Windows answer file.",
			Description: "Makes sure a VM of this name exists, creating it as a linked clone of a snapshot of another VM, so it takes little space and starts from that snapshot's disk. A VM already under the name reports no change and is not reconfigured or reseeded; when its CPUs or memory are not those asked for, the task says so in a warning, and virt.vbox.vm.resize changes them. The new VM gets the size asked for, or the memory and CPUs, and one larger than the host is refused: more CPUs than it has processors online, or more memory than it has. It also gets a NAT adapter for the internet, a host-only adapter at a fixed address, a serial console written to console.log in its folder (virt.vbox.vm.host_keys reads a Linux VM's SSH host keys from there), and a seed on its DVD drive: a cloud-init NoCloud seed, or for a VM of a Windows OS type a Windows answer file (Autounattend.xml)." +
				" The seed is built by Pleiades, not on the host, and reaches the host on the command's standard input, never on a command line. For a Linux VM it carries login's user name, the public half of its key and a salted hash of its password, taken from the vault by pleiades add-credential login --generate; never the key or the password. SSH then admits the key only; the password is for the VM's console. A login of root may log in by key; any other user gets passwordless sudo. A Windows VM is reached over WinRM by the built-in Administrator's password, and an answer file can hold nothing else, so for one the login must be a device reached over WinRM whose credential names Administrator, and the answer file carries that password itself, beside the computer name, the fixed address and the first-boot screens it skips. Windows reads that answer file at first boot only from a base virt.vbox.vm.install made, which points it at the clone's one DVD (D:); a clone of another generalized image, such as Microsoft's evaluation VHDX, stops at its first-boot screens. A Windows VM given no size is small rather than xsmall. The seed stays in the VM's folder until virt.vbox.vm.eject_seed or virt.vbox.vm.delete removes it: eject it once the first boot has read it. The address and login are recorded on the VM as VirtualBox extradata (pleiades/address, pleiades/device), which virt.vbox.vm.list reports, since VirtualBox cannot know a guest's address without its Guest Additions. The VM is not started." +
				vboxHostParamNote + " A check reads the host's VMs and the snapshot, and sends nothing.",
			Params: []collection.Param{
				vmNameParam,
				{Name: "from", Type: "string", Required: true, Description: "The VM to clone, named as name is."},
				{Name: "snapshot", Type: "string", Required: true, Description: "The snapshot of from to clone. Exactly one of from's snapshots may have this name."},
				{Name: "login", Type: "string", Required: true, Description: "The inventory device whose stored login the VM is seeded with, usually the device that stands for this VM. For a Linux VM, one reached over SSH: its user name, its key's public half and a hash of its password. For a Windows VM, one reached over WinRM whose credential names Administrator: its password."},
				{Name: "address", Type: "string", Required: true, Description: "The host-only adapter's address with its prefix, as 192.168.56.10/24. Keep it out of the host-only DHCP server's range."},
				{Name: "hostname", Type: "string", Description: "The VM's host name, or a Windows VM's computer name, which is at most 15 letters, digits and hyphens. Defaults to name, which must then be a valid one."},
				{Name: "size", Type: "string", Choices: vmsize.Names(), Description: "The VM's size, " + vmSizeNote + ". Give size, or memory_mb and cpus, not both; with none of the three, the VM is xsmall, or small for a Windows VM."},
				{Name: "memory_mb", Type: "int", Default: "1024", Description: "Its memory, in megabytes, when size is not given."},
				{Name: "cpus", Type: "int", Default: "1", Description: "Its virtual CPU count, when size is not given."},
				{Name: "paravirt_provider", Type: "string", Choices: []string{"default", "legacy", "minimal", "hyperv", "kvm", "none"}, Description: "The paravirtualization interface the guest is offered, as VBoxManage modifyvm --paravirt-provider takes it: default picks one by the guest's OS type (kvm for Linux, hyperv for Windows), and none offers nothing, so the guest keeps time by its own clocks. Left out, the VM keeps from's."},
				{Name: "host_only_adapter", Type: "string", Default: "VirtualBox Host-Only Ethernet Adapter", Description: "The host's host-only adapter, as VBoxManage list hostonlyifs names it."},
			},
			Returns: []collection.ReturnField{
				{Name: "uuid", Type: "string", Returned: "when not a check that would clone", Description: "The VM's UUID, which is also a Linux VM's cloud-init instance ID."},
				{Name: "address", Type: "string", Returned: "always", Description: "The host-only address the VM was given, without its prefix."},
				{Name: "size", Type: "string", Returned: "always", Description: "The VM's T-shirt size, read from its CPUs and memory (those asked for, in a check that would clone), or empty when they are not exactly one size's."},
				{Name: "diff", Type: "dict", Returned: "always", Description: "Whether a VM of the name existed before this task and after it."},
			},
			Examples: []collection.Example{
				{Name: "Clone and start a lab VM", RunbookYAML: "- name: Make the lab VM from the base's clean snapshot\n  virt.vbox.vm.clone:\n    name: ubuntu-lab\n    from: ubuntu-2404-base\n    snapshot: base\n    login: ubuntu-lab\n    address: 192.168.56.10/24\n    size: small\n\n- name: Start it\n  virt.vbox.vm.start:\n    name: ubuntu-lab\n"},
				{Name: "Clone a Windows VM", RunbookYAML: "- name: Make a Windows lab VM, seeded with win-lab's Administrator password\n  virt.vbox.vm.clone:\n    name: win-lab\n    from: ws2025-base\n    snapshot: base\n    login: win-lab\n    address: 192.168.56.30/24\n    size: medium\n"},
			},
			SeeAlso: []string{"virt.vbox.vm.import_ova", "virt.vbox.vm.import_disk", "virt.vbox.vm.install", "virt.vbox.vm.start", "virt.vbox.vm.host_keys", "virt.vbox.vm.eject_seed", "virt.vbox.vm.resize", "virt.vbox.vm.delete", "virt.vbox.vm.list"},
		},
	},
	{
		Name:          "virt.vbox.vm.delete",
		Capabilities:  []capability.Name{capability.NameVirtualBox},
		Transports:    []string{"winrm"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary: "Deletes a stopped VirtualBox VM and its disks.",
			Description: "Makes sure no VM of this name exists. None reports no change. A running or paused VM is refused: stop it first with virt.vbox.vm.stop. The VM is unregistered and its disks deleted, along with a seed ISO or console log virt.vbox.vm.clone put in its folder and a screenshot virt.vbox.vm.install saved there; install media attached from anywhere else (a shared ISO) is detached, never deleted. A VM that others were linked-cloned from is refused by VirtualBox while they exist. This cannot be undone." +
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
				{Name: "vms", Type: "list", Returned: "always", Description: "Each VM as name, uuid, state, memory_mb, cpus, autostart_enabled, size when its CPUs and memory are one T-shirt size's, and address and device when Pleiades made it, in the order VirtualBox lists them."},
			},
			Examples: []collection.Example{
				{Name: "List the lab's VMs", RunbookYAML: "- name: What runs on the lab host\n  virt.vbox.vm.list: {}\n"},
			},
			SeeAlso: []string{"virt.vbox.vm.info", "virt.vbox.vm.clone"},
		},
	},
}

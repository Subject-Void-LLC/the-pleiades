---
status: beta
---

# virt.fusion.vm.info

Reports a VMware Fusion VM's power state and its guest's address.

Reads a VM on a Mac running VMware Fusion and changes nothing: its power state (vmcli's power query), the identifiers and hardware its vmx keys name (vmcli's ConfigParams query, an allowlist, never a key's raw value, since a vmx can carry a secret), its snapshots (vmrun listSnapshots), and when it is on VMware Tools' state and the guest's address (vmrun getGuestIPAddress). A read past the power state that fails is named under warnings and the rest is returned. A VM whose vmx file is not there is not a failure: exists is false and nothing else is reported, so a later task can decide what to do. The task's target is the Mac (a device onboarding found VMware Fusion answering on), not the VM, which is a resource on it, named by the path of its vmx file. A check is the same read.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `VMwareFusionCapable` |
| Transports | `ssh` |
| Requires elevation | no |
| Runs | on or against the target device; acts on its target device |
| Check mode | Supported: reports what it would change and changes nothing |
| Engine version | `>=0.2.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `vmx` | `string` | yes | - | The absolute path of the VM's .vmx file on the Mac, such as /Users/me/pleiades-vms/win11-arm/win11-arm.vmx. Spaces are allowed; a path that is not absolute, does not end in .vmx, or holds a control character is refused. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `exists` | `bool` | always | Whether the VM's vmx file is on the Mac. |
| `state` | `string` | when exists | Fusion's power state for it: on, off, suspended or paused. |
| `guest_ip` | `string` | when the VM is on and VMware Tools in its guest answers | The guest's IP address, as VMware Tools reports it. |
| `display_name` | `string` | when exists and the vmx sets it | The name Fusion shows for the VM (displayName). |
| `guest_os` | `string` | when exists and the vmx sets it | The guest type the vmx declares (guestOS), such as arm-windows11-64. |
| `memory_mb` | `int` | when exists and the vmx sets it | Its memory in megabytes (memsize). |
| `cpus` | `int` | when exists and the vmx sets it | Its virtual CPU count (numvcpus); a vmx that leaves it out runs one. |
| `firmware` | `string` | when exists and the vmx sets it | What it boots with: efi or bios. |
| `hardware_version` | `int` | when exists and the vmx sets it | Its virtual hardware version (virtualHW.version). |
| `nics` | `list of dict` | when exists and the configuration was read | Each Ethernet adapter the vmx names and does not mark absent, up to 10: number, and connection_type, virtual_dev, address_type and mac when the vmx sets them. |
| `disks` | `list of dict` | when exists and the configuration was read | Each disk or drive file the vmx names, up to 32, sorted by slot: slot (nvme0:0), file, and device_type when set. |
| `snapshots` | `list of string` | when exists and the snapshots were read | The VM's snapshot names, oldest first. |
| `tools_state` | `string` | when the VM is on | VMware Tools in the guest as vmrun names it: running, installed or unknown. |
| `warnings` | `list of string` | when a read beyond the power state failed | Which read failed (the configuration, the snapshots or VMware Tools' state) and the tool's words; the rest is still returned. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

Reading is not changing: this runs vmcli's power and configuration queries and vmrun's listSnapshots, checkToolsState and getGuestIPAddress and alters nothing, so there is nothing an undo could restore.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `virt.fusion.vm.start`
- `virt.fusion.vm.stop`

## Examples

Read a VM:

```yaml
- name: Read the Windows VM
  virt.fusion.vm.info:
    vmx: /Users/me/pleiades-vms/win11-arm/win11-arm.vmx
  register: vm
```


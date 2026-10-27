---
status: beta
---

# virt.fusion.vm.info

Reports a VMware Fusion VM's power state and its guest's address.

Reads a VM on a Mac running VMware Fusion, through vmcli's power query and vmrun's getGuestIPAddress, and changes nothing. A VM whose vmx file is not there is not a failure: exists is false and nothing else is reported, so a later task can decide what to do. The task's target is the Mac (a device onboarding found VMware Fusion answering on), not the VM, which is a resource on it, named by the path of its vmx file. A check is the same read.

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

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

Reading is not changing: this runs vmcli's power query and vmrun's getGuestIPAddress and alters nothing, so there is nothing an undo could restore.

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


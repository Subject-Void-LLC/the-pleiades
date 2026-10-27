---
status: beta
---

# virt.fusion.vm.stop

Stops or suspends a VMware Fusion VM.

Makes sure a VM is no longer running. Mode soft asks the guest to shut down, and needs VMware Tools running in the guest: vmrun refuses it without them. Mode hard powers it off at once, as pulling the plug would. Mode suspend writes its memory to disk, and virt.fusion.vm.start resumes it where it was. A VM that is already off reports no change, and so does a suspended one when the mode is suspend. A suspended VM asked for soft or hard is refused, since it is not running, and so is a paused one. The task's target is the Mac (a device onboarding found VMware Fusion answering on), not the VM, which is a resource on it, named by the path of its vmx file. A check reads the VM and sends nothing.

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
| `mode` | `string` | no | `soft` | How to stop it: soft (the guest shuts down), hard (powered off at once) or suspend (its memory written to disk). |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `state` | `string` | always | The VM's power state after this task, read back from the Mac. |
| `diff` | `dict` | always | The VM's power state before this task and after it. |

## Undoing this

**Can be undone.** A run that changes something records the instruction that reverses it, as an `inverse` stat holding the method to call and the parameters to call it with, resolved from the state this run actually found. A run that changed nothing records no instruction, which is how it says that undoing it means doing nothing.

A run that stopped or suspended a running VM emits virt.fusion.vm.start naming it; one that found it already stopped emits nothing. Starting it again is not the state it had: a soft or hard stop loses what was in its memory.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `virt.fusion.vm.start`
- `virt.fusion.vm.info`

## Examples

Suspend a VM:

```yaml
- name: Put the Windows VM away
  virt.fusion.vm.stop:
    vmx: /Users/me/pleiades-vms/win11-arm/win11-arm.vmx
    mode: suspend
```


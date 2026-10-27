---
status: beta
---

# virt.fusion.vm.start

Starts a VMware Fusion VM with no window, and holds the Mac awake while it runs.

Makes sure a VM is running, started headless with vmrun. A VM that is already on reports no change. A suspended VM resumes where it was. A paused VM is refused, since starting is not resuming. Unless keep_awake is false, the Mac is held awake for as long as the VM's process runs (caffeinate -w on that process): macOS throttles a VM started from an SSH session as background work, and a Windows guest started that way ran about five times slower than its own clock until it was held awake. vmrun's output is read from a file rather than a pipe, because the VM process it starts keeps that output open for its whole life. The task's target is the Mac (a device onboarding found VMware Fusion answering on), not the VM, which is a resource on it, named by the path of its vmx file. A check reads the VM and sends nothing.

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
| `keep_awake` | `bool` | no | `true` | Hold the Mac awake while the VM runs. Set it false only for a Mac that is kept awake some other way: a throttled VM runs, slowly, and can stall its own guest's work. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `state` | `string` | always | The VM's power state after this task, read back from the Mac. |
| `kept_awake` | `bool` | always | Whether this task started holding the Mac awake for the VM. False when nothing was started. |
| `vmx_pid` | `int` | when kept_awake | The process ID of the VM's vmware-vmx, which the hold on the Mac lasts as long as. |
| `diff` | `dict` | always | The VM's power state before this task and after it. |

## Undoing this

**Can be undone.** A run that changes something records the instruction that reverses it, as an `inverse` stat holding the method to call and the parameters to call it with, resolved from the state this run actually found. A run that changed nothing records no instruction, which is how it says that undoing it means doing nothing.

A run that started a VM that was off emits virt.fusion.vm.stop with mode soft; one that resumed a suspended VM emits it with mode suspend; one that found it on emits nothing. Stopping it does not undo what the guest did while it ran.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `virt.fusion.vm.stop`
- `virt.fusion.vm.info`

## Examples

Start a VM:

```yaml
- name: Start the Windows VM
  virt.fusion.vm.start:
    vmx: /Users/me/pleiades-vms/win11-arm/win11-arm.vmx
```


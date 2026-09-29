---
status: beta
---

# virt.vbox.vm.start

Starts a VirtualBox VM with no window.

Makes sure a VM is running, started headless. A VM that is already running reports no change. A paused VM is refused, since starting is not resuming. The task's target is the VirtualBox host (a device with virtualbox: true), not the VM, which is a resource on it. On a Windows host, a VM cannot start from the WinRM logon this task uses: Windows' catalog signature check fails for a non-administrator logon that is not interactive, and VirtualBox's hardening then refuses the hypervisor (VirtualBox ticket 20341). So when none of the account's VMs is running, the VM is marked for autostart and started by the account's VirtualBox autostart service, which runs under a service logon; examples/windows_lab/winrm-cert-setup.ps1 -VirtualBoxAutostart installs it. While any of the account's VMs runs, a plain start works, because every client is then handed the VirtualBox server that VM keeps alive. The autostart mark stays on while the VM runs, since VirtualBox refuses to change a running VM's settings, so a host restart in that time starts it again; virt.vbox.vm.stop clears it. A VM with more memory than the host has free, less 1024 MB kept for the host itself, is refused rather than started, since the host would page to find it; the host's free memory is read at the start. A check reads the VM and the host and sends nothing.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `VirtualBoxCapable` |
| Transports | `winrm` |
| Requires elevation | no |
| Runs | on or against the target device; acts on its target device |
| Check mode | Supported: reports what it would change and changes nothing |
| Engine version | `>=0.2.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `name` | `string` | yes | - | The VM's name on the host. It must start with a letter or digit and hold only letters, digits, '.', '_' and '-', at most 63 characters; any other name is refused rather than quoted. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `state` | `string` | always | The VM's state after this task, read back from the host. |
| `via_autostart_service` | `bool` | always | Whether the start went through the account's autostart service. False when nothing was started. |
| `diff` | `dict` | always | The VM's state before this task and after it. |

## Undoing this

**Can be undone.** A run that changes something records the instruction that reverses it, as an `inverse` stat holding the method to call and the parameters to call it with, resolved from the state this run actually found. A run that changed nothing records no instruction, which is how it says that undoing it means doing nothing.

A run that started a stopped VM emits virt.vbox.vm.stop naming it; one that found it running emits nothing. Stopping it does not undo what the guest did while it ran.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `virt.vbox.vm.stop`
- `virt.vbox.vm.info`

## Examples

Start a VM:

```yaml
- name: Start the lab VM
  virt.vbox.vm.start:
    name: ubuntu-lab
```


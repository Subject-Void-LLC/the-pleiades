---
status: beta
---

# virt.vbox.vm.stop

Stops a VirtualBox VM, by its power button or by cutting its power.

Makes sure a VM is not running. A VM that is already off, saved or aborted reports no change. mode acpi presses the VM's power button and waits for the guest to shut itself down, failing, rather than cutting the power, if it has not within timeout; mode poweroff stops it at once, as pulling its plug would, and loses whatever the guest had not written. Once the VM is off, its autostart mark is cleared, which virt.vbox.vm.start sets on a Windows host. The task's target is the VirtualBox host (a device with virtualbox: true), not the VM, which is a resource on it. A check reads the VM and sends nothing.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `VirtualBoxCapable` |
| Transports | `winrm` |
| Requires elevation | no |
| Check mode | Supported: reports what it would change and changes nothing |
| Engine version | `>=0.2.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `name` | `string` | yes | - | The VM's name on the host. It must start with a letter or digit and hold only letters, digits, '.', '_' and '-', at most 63 characters; any other name is refused rather than quoted. |
| `mode` | `string` | no | `acpi` | acpi asks the guest to shut down; poweroff cuts its power. |
| `timeout` | `int` | no | `120` | With mode acpi, how many seconds to wait for the guest to shut down before failing. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `state` | `string` | always | The VM's state after this task, read back from the host. |
| `diff` | `dict` | always | The VM's state before this task and after it. |

## Undoing this

**Can be undone.** A run that changes something records the instruction that reverses it, as an `inverse` stat holding the method to call and the parameters to call it with, resolved from the state this run actually found. A run that changed nothing records no instruction, which is how it says that undoing it means doing nothing.

A run that stopped a running VM emits virt.vbox.vm.start naming it; one that found it stopped emits nothing. Starting it again does not bring back what a power cut lost.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `virt.vbox.vm.start`

## Examples

Shut a VM down:

```yaml
- name: Shut the lab VM down
  virt.vbox.vm.stop:
    name: ubuntu-lab
```

Cut a VM's power:

```yaml
- name: Power the lab VM off now
  virt.vbox.vm.stop:
    name: ubuntu-lab
    mode: poweroff
```


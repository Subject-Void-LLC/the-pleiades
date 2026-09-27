---
status: beta
---

# virt.vbox.vm.delete

Deletes a stopped VirtualBox VM and its disks.

Makes sure no VM of this name exists. None reports no change. A running or paused VM is refused: stop it first with virt.vbox.vm.stop. The VM is unregistered and its disks deleted, along with a seed ISO or console log virt.vbox.vm.clone put in its folder; install media attached from anywhere else (a shared ISO) is detached, never deleted. A VM that others were linked-cloned from is refused by VirtualBox while they exist. This cannot be undone. The task's target is the VirtualBox host (a device with virtualbox: true), not the VM, which is a resource on it. A check reads the VM and sends nothing.

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

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `uuid` | `string` | when a VM was deleted | The VM deleted. |
| `diff` | `dict` | always | Whether a VM of the name existed before this task and after it. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

Deleting a VM deletes its disks, and nothing keeps what was on them, so there is no VM an undo could bring back. Take a snapshot and keep the base it was cloned from to make it again.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `virt.vbox.vm.clone`
- `virt.vbox.vm.stop`

## Examples

Throw a lab VM away:

```yaml
- name: Power the lab VM off
  virt.vbox.vm.stop:
    name: ubuntu-lab
    mode: poweroff

- name: Delete it
  virt.vbox.vm.delete:
    name: ubuntu-lab
```


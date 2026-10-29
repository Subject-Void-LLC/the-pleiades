---
status: beta
---

# virt.vbox.vm.detach_disk

Takes a disk off a stopped VirtualBox VM, deleting it from the host when asked.

Makes sure the disk at path is not attached to a VM: one attached is taken out of its slot, and with delete true forgotten and deleted from the host too, with everything the guest wrote on it; one not attached reports no change, except that with delete true a disk the host still holds, attached to nothing, is deleted. VirtualBox detaches a disk only while a VM is powered off, so a running, paused or saved VM is refused: stop it first with virt.vbox.vm.stop. A run that detached a disk it kept emits virt.vbox.vm.attach_disk back; one that deleted it emits nothing, since what the disk held is gone. The task's target is the VirtualBox host (a device with virtualbox: true), not the VM, which is a resource on it. A check reads the VM and the host's disks, and sends nothing.

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
| `path` | `string` | yes | - | The disk's path on the host, as virt.vbox.vm.attach_disk reported it. |
| `delete` | `bool` | no | `false` | Whether to forget the disk and delete its file from the host once it is off the VM, losing what the guest wrote on it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `detached` | `bool` | always | Whether this task took the disk off the VM (predicted, in a check). |
| `deleted` | `bool` | always | Whether this task deleted the disk from the host (predicted, in a check). |
| `diff` | `dict` | always | Whether the disk was attached before this task and after it. |
| `warnings` | `list` | when the VM could not be read back after the disk was taken off | What the task could not confirm: the disk is off the VM, which the task could not read again. |

## Undoing this

**Can be undone.** A run that changes something records the instruction that reverses it, as an `inverse` stat holding the method to call and the parameters to call it with, resolved from the state this run actually found. A run that changed nothing records no instruction, which is how it says that undoing it means doing nothing.

A run that detached a disk it kept emits virt.vbox.vm.attach_disk back; one that deleted the disk emits nothing, since what it held is gone; one that found it detached emits nothing.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `virt.vbox.vm.attach_disk`
- `virt.vbox.vm.stop`
- `virt.vbox.vm.info`

## Examples

Throw a lab VM's data disk away and start over:

```yaml
- name: Shut the check VM down
  virt.vbox.vm.stop:
    name: bsd-check

- name: Delete its data disk
  virt.vbox.vm.detach_disk:
    name: bsd-check
    path: G:\PleiadesLab\bsd-check\bsd-check-data.vdi
    delete: true

- name: Give it a fresh one
  virt.vbox.vm.attach_disk:
    name: bsd-check
    size_gb: 40
```


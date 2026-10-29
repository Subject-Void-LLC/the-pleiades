---
status: beta
---

# virt.vbox.vm.attach_disk

Gives a stopped VirtualBox VM a second disk, made new on the host when it is not there yet.

Makes sure a VM has the disk at path attached, as a hard disk in its first free SATA port (or, with none free, the port holding an empty DVD drive, which a Windows clone keeps once its seed is ejected; the drive is removed for the disk): one already attached reports no change; one the host has registered but not attached is attached; otherwise a new, empty VDI of size_gb is made at path (the VM's own folder and <name>-data.vdi when path is not given), growing on the host as the guest writes to it, and attached. The guest sees a blank disk it must partition and format itself; nothing here touches the guest. VirtualBox attaches a disk only while a VM is powered off, so a running, paused or saved VM is refused: stop it first with virt.vbox.vm.stop. A run that attached the disk emits virt.vbox.vm.detach_disk, deleting the disk only when this task made it. The task's target is the VirtualBox host (a device with virtualbox: true), not the VM, which is a resource on it. A check reads the VM and the host's disks, and sends nothing.

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
| `path` | `string` | no | - | The disk's path on the host, an absolute drive path such as G:\PleiadesLab\bsd-check\bsd-check-data.vdi; when not given, the VM's own folder and <name>-data.vdi. |
| `size_gb` | `int` | no | - | How big a disk to make, in gigabytes, when the host has none at path; a disk already there keeps its size. Required unless the disk exists. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `path` | `string` | always | The disk's path on the host. |
| `port` | `int` | always | The SATA port the disk is attached at (predicted, in a check). |
| `created` | `bool` | always | Whether this task made the disk (predicted, in a check). |
| `diff` | `dict` | always | Whether the disk was attached before this task and after it. |
| `warnings` | `list` | when the VM could not be read back after the disk was attached | What the task could not confirm: the disk is attached, and the port is the one it was put in rather than the one read back. |

## Undoing this

**Can be undone.** A run that changes something records the instruction that reverses it, as an `inverse` stat holding the method to call and the parameters to call it with, resolved from the state this run actually found. A run that changed nothing records no instruction, which is how it says that undoing it means doing nothing.

A run that attached the disk emits virt.vbox.vm.detach_disk, deleting the disk from the host only when this task made it; one that found it attached emits nothing.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `virt.vbox.vm.detach_disk`
- `virt.vbox.vm.stop`
- `virt.vbox.vm.info`

## Examples

Give a lab VM a data disk:

```yaml
- name: Shut the check VM down
  virt.vbox.vm.stop:
    name: bsd-check

- name: Give it a 40 GB data disk
  virt.vbox.vm.attach_disk:
    name: bsd-check
    size_gb: 40

- name: Start it again
  virt.vbox.vm.start:
    name: bsd-check
```


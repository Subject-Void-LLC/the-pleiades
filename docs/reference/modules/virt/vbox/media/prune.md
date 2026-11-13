---
status: beta
---

# virt.vbox.media.prune

Deletes the disks no registered VM uses and the leftover folders of VMs VirtualBox no longer knows, under the host's machine folder.

Removes what a VM VirtualBox forgot leaves behind: a delete that failed partway, or a VM unregistered without its files. Under the host's machine folder (its vm_folder, or VirtualBox's default machine folder when it names none) it finds every disk VirtualBox has registered that no registered VM uses, in its current state or a snapshot, and every folder holding no registered VM's settings and no disk one uses, among the folders that hold a VM's settings file (a .vbox, or the .vbox-prev VirtualBox keeps beside it) and the empty ones a delete leaves; any other folder is never touched. A folder this run's disk deletions leave empty is not in this run's list, so the next run removes it. A disk is deleted with its file, or only forgotten when its file is already gone; one with a descendant that is in use, or that is not being deleted too, is left, and descendants go before their parents. A folder holding a registered VM's files is left. When VirtualBox lists a VM it cannot read, no folder is removed, since nothing can say which folder is that VM's: delete it first with virt.vbox.vm.delete, naming its UUID. Nothing outside the machine folder is touched. A check lists exactly what a run would remove and removes nothing; it is how to see the list without acting on it. Nothing removed can be brought back. The task's target is the VirtualBox host (a device with virtualbox: true), not the VM, which is a resource on it. A check reads the host's VMs, disks and folders, and sends nothing.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `VirtualBoxCapable` |
| Transports | `winrm` |
| Requires elevation | no |
| Runs | on or against the target device; acts on its target device |
| Check mode | Supported: reports what it would change and changes nothing |
| Engine version | `>=0.4.0` |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `disks` | `list` | always | Each disk removed, or that a check would remove: its UUID, its file and whether its file was already gone. |
| `folders` | `list` | always | Each folder removed, or that a check would remove. |
| `diff` | `dict` | always | How many disks and folders there were to remove before this task, and after it. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

It deletes disks and folders no registered VM uses, and nothing keeps what was in them, so there is nothing an undo could put back; a check lists what a run would remove before anything is.

`pleiades rollback` and the Controller's rollback route replay a recorded instruction. It is recorded during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `virt.vbox.vm.delete`
- `virt.vbox.vm.list`

## Examples

See what would be removed, then remove it:

```yaml
- name: Remove the disks and folders no VM uses
  virt.vbox.media.prune: {}
```


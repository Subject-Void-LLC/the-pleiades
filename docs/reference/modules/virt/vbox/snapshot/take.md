---
status: beta
---

# virt.vbox.snapshot.take

Takes a snapshot of a VirtualBox VM under a name no other snapshot of it has.

Makes sure a VM has a snapshot with this name. A snapshot already under the name reports no change and takes no second one, although VirtualBox itself would. A running VM can be snapshotted; a snapshot of a running VM holds its memory too. The task's target is the VirtualBox host (a device with virtualbox: true), not the VM, which is a resource on it. A check reads the VM and sends nothing.

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
| `vm` | `string` | yes | - | The VM whose snapshot this is, named as for virt.vbox.vm.start. |
| `name` | `string` | yes | - | The snapshot's name, under the same rule as a VM's. VirtualBox lets two snapshots share a name; virt.vbox.snapshot.take never makes a second, and the methods that find a snapshot by name refuse one that matches more than one unless uuid says which. |
| `description` | `string` | no | - | Text stored with the snapshot, at most 200 characters on one line. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `uuid` | `string` | always | The snapshot's UUID: the one taken, or the one already under the name. |
| `diff` | `dict` | always | Whether a snapshot under the name existed before this task and after it. |

## Undoing this

**Can be undone.** A run that changes something records the instruction that reverses it, as an `inverse` stat holding the method to call and the parameters to call it with, resolved from the state this run actually found. A run that changed nothing records no instruction, which is how it says that undoing it means doing nothing.

A run that took a snapshot emits virt.vbox.snapshot.delete naming it by UUID, so the undo deletes that snapshot and no other of the same name; one that found a snapshot under the name emits nothing.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `virt.vbox.snapshot.restore`
- `virt.vbox.snapshot.delete`

## Examples

Snapshot a clean install:

```yaml
- name: Keep the clean install
  virt.vbox.snapshot.take:
    vm: ubuntu-lab
    name: clean
    description: fresh install, before any test
```


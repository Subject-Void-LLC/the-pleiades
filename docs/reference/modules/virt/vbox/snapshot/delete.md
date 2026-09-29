---
status: beta
---

# virt.vbox.snapshot.delete

Deletes a snapshot of a VirtualBox VM.

Makes sure a VM has no snapshot with this name. None under the name reports no change. The snapshot's saved state is merged away and cannot be brought back, so this cannot be undone. The task's target is the VirtualBox host (a device with virtualbox: true), not the VM, which is a resource on it. A check reads the VM and sends nothing.

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
| `vm` | `string` | yes | - | The VM whose snapshot this is, named as for virt.vbox.vm.start. |
| `name` | `string` | yes | - | The snapshot's name, under the same rule as a VM's. VirtualBox lets two snapshots share a name; virt.vbox.snapshot.take never makes a second, and the methods that find a snapshot by name refuse one that matches more than one unless uuid says which. |
| `uuid` | `string` | no | - | The snapshot's UUID, needed only when more than one snapshot of the VM has this name (which only VirtualBox itself, or a tool other than this one, makes). It must be one of them. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `uuid` | `string` | when a snapshot was deleted | The snapshot deleted. |
| `diff` | `dict` | always | Whether a snapshot under the name existed before this task and after it. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

Deleting merges the snapshot's saved disk state into what came after it and drops its saved memory, so the state it held is gone; a new snapshot taken under the same name would hold the VM as it is now, not as it was.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `virt.vbox.snapshot.take`

## Examples

Drop a snapshot:

```yaml
- name: Drop the old baseline
  virt.vbox.snapshot.delete:
    vm: ubuntu-lab
    name: clean
```


---
status: beta
---

# virt.vbox.snapshot.restore

Puts a VirtualBox VM back to a snapshot, discarding its current state.

Restores a VM to a snapshot. Everything the VM did since that snapshot is discarded, so this always reports a change and cannot be undone. A running VM is refused: stop it first with virt.vbox.vm.stop, so that discarding a running machine is a decision a runbook states rather than a side effect. A snapshot that does not exist is refused. The task's target is the VirtualBox host (a device with virtualbox: true), not the VM, which is a resource on it. A check reads the VM and sends nothing.

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
| `uuid` | `string` | always | The snapshot restored. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

Restoring discards everything the VM did since the snapshot, and nothing keeps what was discarded, so there is no state an undo could put back. Take a snapshot first to keep it.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `virt.vbox.snapshot.take`
- `virt.vbox.vm.stop`

## Examples

Reset to a clean install:

```yaml
- name: Stop the lab VM
  virt.vbox.vm.stop:
    name: ubuntu-lab
    mode: poweroff

- name: Back to the clean install
  virt.vbox.snapshot.restore:
    vm: ubuntu-lab
    name: clean

- name: Start it again
  virt.vbox.vm.start:
    name: ubuntu-lab
```


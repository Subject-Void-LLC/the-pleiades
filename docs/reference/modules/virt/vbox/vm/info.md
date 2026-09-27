---
status: beta
---

# virt.vbox.vm.info

Reports a VirtualBox VM's state, hardware and snapshots.

Reads a VM on a VirtualBox host with VBoxManage showvminfo and changes nothing. A VM that does not exist is not a failure: exists is false and nothing else is reported, so a later task can decide what to do. The task's target is the VirtualBox host (a device with virtualbox: true), not the VM, which is a resource on it. A check is the same read.

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
| `exists` | `bool` | always | Whether a VM with this name is registered on the host. |
| `uuid` | `string` | when exists | The VM's UUID. |
| `state` | `string` | when exists | VirtualBox's state for it: poweroff, running, saved, paused, aborted, or another VirtualBox reports. |
| `memory_mb` | `int` | when exists | Its memory, in megabytes. |
| `cpus` | `int` | when exists | Its virtual CPU count. |
| `autostart_enabled` | `bool` | when exists | Whether it is marked to start with the host account's autostart service. virt.vbox.vm.start sets this on a Windows host and virt.vbox.vm.stop clears it. |
| `snapshots` | `list` | when exists | Each snapshot as name, uuid and description, a parent before its children. |
| `current_snapshot_uuid` | `string` | when exists | The snapshot the VM's state descends from, or empty when it has none. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

Reading is not changing: this runs VBoxManage showvminfo and alters nothing, so there is nothing an undo could restore.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `virt.vbox.vm.start`
- `virt.vbox.snapshot.take`

## Examples

Read a VM:

```yaml
- name: Read the lab VM
  virt.vbox.vm.info:
    name: ubuntu-lab
  register: vm
```


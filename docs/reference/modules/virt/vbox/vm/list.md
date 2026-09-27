---
status: beta
---

# virt.vbox.vm.list

Lists the VMs on a VirtualBox host, with their state and the address Pleiades gave them.

Reports every VM registered on the host for the account Pleiades reaches it as: its name, UUID, state, memory, CPUs and autostart mark, and, for a VM virt.vbox.vm.clone made, the host-only address it was given and the inventory device whose login it was seeded with. VirtualBox keeps a separate list of VMs for each Windows account, so these are not the VMs a person sees in their own VirtualBox Manager, and theirs are not listed here. Nothing is changed. The task's target is the VirtualBox host (a device with virtualbox: true), not the VM, which is a resource on it. A check is the same read.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `VirtualBoxCapable` |
| Transports | `winrm` |
| Requires elevation | no |
| Check mode | Supported: reports what it would change and changes nothing |
| Engine version | `>=0.2.0` |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `vms` | `list` | always | Each VM as name, uuid, state, memory_mb, cpus, autostart_enabled, and address and device when Pleiades made it, in the order VirtualBox lists them. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

Reading is not changing: this lists VMs and alters nothing, so there is nothing an undo could restore.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `virt.vbox.vm.info`
- `virt.vbox.vm.clone`

## Examples

List the lab's VMs:

```yaml
- name: What runs on the lab host
  virt.vbox.vm.list: {}
```


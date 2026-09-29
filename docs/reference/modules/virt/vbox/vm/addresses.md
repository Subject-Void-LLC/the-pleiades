---
status: beta
---

# virt.vbox.vm.addresses

Reports the addresses a VM's host-only adapters were given, from VirtualBox's DHCP server and from virt.vbox.vm.clone.

Reports each address a VM's host-only adapters have: the leases VirtualBox's own DHCP server handed them (read from the leases file it keeps for the host's account), and the fixed address virt.vbox.vm.clone gave the VM (the extradata pleiades/address). A VM whose first boot did not apply its fixed address shows the address DHCP gave it too, which is how to reach it anyway. A NAT adapter's address is private to the VM and is not reported. Nothing is changed. The task's target is the VirtualBox host (a device with virtualbox: true), not the VM, which is a resource on it. A check reads the same as a run.

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

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `addresses` | `list` | always | Each address as nic (the adapter's number), mac, adapter (the host's), address, source (dhcp or fixed), and for a DHCP lease its state (acked while held, expired after), issued and expires. |
| `address` | `string` | when there is one | The address to reach the VM at: the fixed one, or else a held DHCP lease's. A first boot that applied its fixed address leaves the lease it took before then marked held for a while, so a VM whose fixed address was never applied is known by that lease being the only one it answers at. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

Reading is not changing: this reads the VM and the host's DHCP leases and alters nothing, so there is nothing an undo could restore.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `virt.vbox.vm.clone`
- `virt.vbox.vm.list`

## Examples

Find a VM's address:

```yaml
- name: Where the lab VM answers
  virt.vbox.vm.addresses:
    name: win-lab
  register: found
```


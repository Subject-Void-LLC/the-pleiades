---
status: beta
---

# virt.vbox.vm.eject_seed

Takes a VM's seed out of its DVD drive and deletes it, once its first boot has read it.

Makes sure a VM virt.vbox.vm.clone made no longer holds its seed: the cloud-init seed or Windows answer file clone put on its DVD drive and in its folder, which holds a hash of the login's password or, for Windows, the Administrator's password itself. The drive is emptied at once, even while the VM runs and even when the guest has locked it, so the guest can never read the seed again. The seed file is then deleted; but VirtualBox keeps an image locked for as long as the VM that held it runs (measured on the lab host), so on a running VM the file stays until the VM is stopped, the task says so in a warning and reports deleted false, and running it again once the VM is stopped deletes it. Run it once the first boot has read the seed: after virt.vbox.vm.host_keys finds a Linux VM's keys, or after wait.connection reaches a Windows VM. A VM holding no seed, in a drive or on the host, reports no change. The seed cannot be put back. The task's target is the VirtualBox host (a device with virtualbox: true), not the VM, which is a resource on it. A check reads the VM and sends nothing.

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
| `ejected` | `bool` | always | Whether a seed was taken out, or would be, in a check. |
| `deleted` | `bool` | when a seed was taken out | Whether the seed file was deleted from the host: false while the VM runs, which keeps it locked. |
| `diff` | `dict` | always | Whether the VM held its seed before this task and after it. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

The seed file is deleted, and nothing keeps what it held (a hash of the login's password, or a Windows VM's Administrator password), so it cannot be put back; a VM that needs seeding again is made again with virt.vbox.vm.clone.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `virt.vbox.vm.clone`
- `virt.vbox.vm.host_keys`
- `wait.connection`

## Examples

Delete a Windows VM's answer file after its first boot:

```yaml
- name: Take the lab VM's answer file out, now that it has booted
  virt.vbox.vm.eject_seed:
    name: win-lab
```


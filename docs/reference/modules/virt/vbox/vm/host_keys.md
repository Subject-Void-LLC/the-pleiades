---
status: beta
---

# virt.vbox.vm.host_keys

Reads a VM's SSH host keys from what cloud-init printed on its serial console.

Waits for cloud-init to print the VM's SSH host keys on its serial console, which virt.vbox.vm.clone logs to a file on the host, and reports them. They are read over the host's own authenticated connection, not from the network the VM answers SSH on, so they are what a known_hosts file can trust before the first SSH connection: pleiades trust-host <device> --from-console <host> writes them there. The newest complete block is the one read. Nothing is changed. The task's target is the VirtualBox host (a device with virtualbox: true), not the VM, which is a resource on it. A check reads the console once, without waiting.

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
| `timeout` | `int` | no | `300` | How many seconds to wait for the keys. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `ssh_host_keys` | `list` | always | Each key as algorithm and base64, the form a known_hosts line holds after its host names. Empty in a check that found none yet. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

Reading is not changing: this reads the VM's console log and alters nothing, so there is nothing an undo could restore.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `virt.vbox.vm.clone`

## Examples

Wait for a new VM's host keys:

```yaml
- name: Wait for the lab VM's first boot
  virt.vbox.vm.host_keys:
    name: ubuntu-lab
  register: keys
```


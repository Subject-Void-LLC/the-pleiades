---
status: beta
---

# virt.vbox.vm.screenshot

Saves a picture of a running VM's screen as a PNG file on the machine running Pleiades.

Takes a picture of a running VM's screen on the host, brings it back over the host's own connection, and writes it to dest on the machine running this task, as Ansible's fetch brings a file back; the host keeps no copy. It is how to see a VM with no window, such as one stopped at a first-boot screen or an installer's error. A VM that has not set a display mode yet (still in its firmware, or stopped before it drew anything) has no picture to take, and says so. The VM is not changed; dest is replaced when it exists. The task's target is the VirtualBox host (a device with virtualbox: true), not the VM, which is a resource on it. A check reads the VM, takes no picture and writes nothing.

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
| `dest` | `string` | yes | - | Where to write the PNG on the machine running this task. Its folder must exist. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `dest` | `string` | always | Where the picture was written, or would be, in a check. |
| `width` | `int` | when not a check | The picture's width, in pixels. |
| `height` | `int` | when not a check | The picture's height, in pixels. |
| `bytes` | `int` | when not a check | The PNG file's size. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

The VM is not changed; the file at dest is replaced by the picture, and what it held before is not kept.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `virt.vbox.vm.log`
- `virt.vbox.vm.send_keys`

## Examples

See where a Windows VM's first boot stopped:

```yaml
- name: Save a picture of the lab VM's screen
  virt.vbox.vm.screenshot:
    name: win-lab
    dest: ./win-lab.png
```


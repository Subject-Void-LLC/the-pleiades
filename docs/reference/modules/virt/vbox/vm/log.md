---
status: beta
---

# virt.vbox.vm.log

Reads the end of a VM's VirtualBox log, optionally only the lines matching a pattern.

Reads the end of the log VirtualBox writes for a VM's current or last run (VBox.log in its folder's Logs), and reports its last lines, or its last lines matching pattern. The log says what the VM's firmware and VirtualBox did: where a boot stopped, which hypervisor interface the guest used, and why a VM aborted. Nothing is changed. The task's target is the VirtualBox host (a device with virtualbox: true), not the VM, which is a resource on it. A check reads the log as a run does.

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
| `lines` | `int` | no | `40` | How many lines to report, from the end: 1 to 2000. |
| `pattern` | `string` | no | - | Report only lines matching this Go RE2 regular expression, such as EFI\|GIM\|fatal. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `lines` | `list` | always | The log's last lines, or its last lines matching pattern, oldest first. Empty when the VM has never run. |
| `path` | `string` | always | The log file's path on the host. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

Reading is not changing: this reads the VM's log and alters nothing, so there is nothing an undo could restore.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `virt.vbox.vm.screenshot`
- `virt.vbox.vm.info`

## Examples

See where a VM's firmware stopped:

```yaml
- name: Read the lab VM's firmware lines
  virt.vbox.vm.log:
    name: win-lab
    pattern: "EFI|debug point"
    lines: 20
```


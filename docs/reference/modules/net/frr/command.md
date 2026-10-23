---
status: beta
---

# net.frr.command

Runs one FRRouting show command through vtysh and returns its output.

Only show commands are accepted, so it changes nothing. A command ending in json returns its output parsed as data too.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `FRRCapable`, `CommandExecCapable` |
| Transports | `ssh` |
| Requires elevation | no |
| Runs | on or against the target device; acts on its target device |
| Check mode | Supported: reports what it would change and changes nothing |
| Engine version | `>=0.2.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `command` | `string` | yes | - | The show command, e.g. "show ip ospf neighbor". |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `stdout` | `string` | always | What vtysh printed. |
| `data` | `dict` | when the command ends in json | stdout parsed. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

Runs only show commands, which change nothing.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `net.frr.facts`

## Examples

Read OSPF neighbours:

```yaml
- name: OSPF neighbours
  net.frr.command:
    command: show ip ospf neighbor
```


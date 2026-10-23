---
status: beta
---

# net.trace

Traces the path from a Linux device to an address, and fails when it is not the path expected.

Runs tracepath, which needs no raw socket and so no privilege, and returns each hop's address. With expect, it fails the task when the destination was not reached, or a hop named in includes is missing, or one named in avoids is on the path. It changes nothing.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `LinuxCapable`, `CommandExecCapable` |
| Transports | `ssh` |
| Requires elevation | no |
| Runs | on or against the target device; acts on its target device |
| Check mode | Supported: reports what it would change and changes nothing |
| Engine version | `>=0.2.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `dest` | `string` | yes | - | The address to trace to. |
| `max_hops` | `int` | no | `30` | The most hops to try. |
| `expect` | `dict` | no | - | reached (bool), includes (list of addresses that must be hops) and avoids (list that must not be). |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `hops` | `list of string` | always | Each hop's address, in order, the destination last when reached. |
| `reached` | `bool` | always | Whether the destination answered. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

It sends probes and reads their answers, changing nothing.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `wait.routes`
- `net.batfish.check`

## Examples

Expect a path through r2:

```yaml
- name: h1's LAN reaches h2 through r2
  net.trace:
    dest: 10.3.3.10
    expect:
      reached: true
      includes: [10.0.12.1]
```


---
status: beta
---

# net.frr.interface

Sets an FRRouting interface administratively up or down, recording the state it found.

Reads the interface's administrative state through vtysh, and changes it only when it differs (shutdown or no shutdown in the running configuration, not frr.conf). Its undo sets the state it found, never simply the opposite, so undoing a run against an interface that was already down leaves it down.

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
| `name` | `string` | yes | - | The interface, as FRR names it (eth1, ens3). |
| `enabled` | `bool` | yes | - | true for up (no shutdown), false for down (shutdown). |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `enabled` | `bool` | always | Whether the interface is administratively up now. |
| `inverse` | `dict` | when something changed | The undo: this method with the state found. |

## Undoing this

**Can be undone.** A run that changes something records the instruction that reverses it, as an `inverse` stat holding the method to call and the parameters to call it with, resolved from the state this run actually found. A run that changed nothing records no instruction, which is how it says that undoing it means doing nothing.

Its undo sets the administrative state the interface had before the run, recorded from the router, never simply the opposite of the change.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `net.frr.config`
- `wait.routes`

## Examples

Take a link down:

```yaml
- name: Shut r1's link to r3
  net.frr.interface:
    name: eth2
    enabled: false
```


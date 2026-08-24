---
status: beta
---

# identity.group.modify

Changes the gid of an existing POSIX group on the target.

Converges an existing group's gid, refusing outright if the group does not exist rather than creating one (use identity.group.create for that). A POSIX group has no other mutable attribute this platform manages: renaming is not something groupmod supports and ansible.builtin.group does not offer it either, and membership is identity.user.*'s own concern. Group state is read from getent before anything is sent, so a gid already matching what was requested reports no change and no command reaches the device.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `PosixAccountCapable` |
| Transports | `ssh` |
| Requires elevation | yes |
| Engine version | `>=1.0.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `name` | `string` | yes | - | The group to modify. Must already exist. |
| `gid` | `int` | yes | - | Converge the group to this numeric gid. |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `name` | `string` | always | The group this task acted on. |
| `diff` | `dict` | always | What getent reported about the group before this task and after it, each holding exists and gid. |

## Undoing this

**Can be undone.** A run that changes something records the instruction that reverses it, as an `inverse` stat holding the method to call and the parameters to call it with, resolved from the state this run actually found. A run that changed nothing records no instruction, which is how it says that undoing it means doing nothing.

A run that converged the gid emits an identity.group.modify pinned to the old gid, which is a real, restorable inverse. A run that found the gid already matching emits nothing.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `identity.group.create`
- `identity.group.remove`

## Examples

Change a group's gid:

```yaml
- name: Renumber admins
  identity.group.modify:
    name: admins
    gid: 6000
```


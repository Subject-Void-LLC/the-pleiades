---
status: beta
---

# identity.group.remove

Removes a POSIX group from the target.

Makes sure a group is absent from the target, removing it if present. This is ansible.builtin.group with state=absent. Group state is read from getent before anything is sent, so a group already absent reports no change and no command reaches the device. groupdel itself refuses to remove a group that is still any user's primary group; that refusal surfaces here as a plain task failure naming what groupdel said, not something this method works around.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `PosixAccountCapable` |
| Transports | `ssh` |
| Requires elevation | yes |
| Check mode | Supported: reports what it would change and changes nothing |
| Engine version | `>=1.0.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `name` | `string` | yes | - | The group name to remove. |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `name` | `string` | always | The group this task acted on. |
| `diff` | `dict` | always | What getent reported about the group before this task and after it. After always reports exists: false on a successful run. |

## Undoing this

**Can be undone.** A run that changes something records the instruction that reverses it, as an `inverse` stat holding the method to call and the parameters to call it with, resolved from the state this run actually found. A run that changed nothing records no instruction, which is how it says that undoing it means doing nothing.

A run that removed a present group emits an identity.group.create pinned to the exact gid this run captured before removing it, which is a real, restorable inverse. A run that found the group already absent emits nothing. What the inverse cannot restore is any user's primary or supplementary membership in the group at the moment it was removed.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `identity.group.create`
- `identity.group.modify`

## Examples

Remove a group:

```yaml
- name: Make sure the old admins group is gone
  identity.group.remove:
    name: admins
```


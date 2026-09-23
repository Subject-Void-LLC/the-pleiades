---
status: beta
---

# identity.group.create

Makes sure a POSIX group exists on the target.

Makes sure a group is present on the target, creating it if absent. This is ansible.builtin.group with state=present. Group state is read from getent before anything is sent, so a group already present at the requested gid (or present with none requested) reports no change and no command reaches the device; a group present at a different gid than requested is converged with groupmod rather than recreated.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `PosixAccountCapable` |
| Transports | `ssh` |
| Requires elevation | yes |
| Check mode | Supported: reports what it would change and changes nothing |
| Engine version | `>=0.2.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `name` | `string` | yes | - | The group name to create or converge. |
| `gid` | `int` | no | - | The numeric group ID to assign. An existing group with a different gid is converged to this one. |
| `system` | `bool` | no | `false` | Create the group as a system group (groupadd -r). Ignored when the group already exists. |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `name` | `string` | always | The group this task acted on. |
| `diff` | `dict` | always | What getent reported about the group before this task and after it, each holding exists and gid. Recorded even on a run that changed nothing. |

## Undoing this

**Can be undone.** A run that changes something records the instruction that reverses it, as an `inverse` stat holding the method to call and the parameters to call it with, resolved from the state this run actually found. A run that changed nothing records no instruction, which is how it says that undoing it means doing nothing.

A run that created an absent group emits an identity.group.remove naming it. A run that found the group already present but converged its gid emits an identity.group.modify pinned to the old gid, which is a real, restorable inverse. A run that found the group already at the requested gid emits nothing.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `identity.group.modify`
- `identity.group.remove`
- `identity.user.create`

## Examples

Create a plain group:

```yaml
- name: Make sure admins exists
  identity.group.create:
    name: admins
```

Pin a gid:

```yaml
- name: Create a service group
  identity.group.create:
    name: appsvc
    gid: 5000
    system: true
```


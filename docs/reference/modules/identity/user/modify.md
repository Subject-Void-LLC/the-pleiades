---
status: beta
---

# identity.user.modify

Changes attributes of an existing POSIX user account on the target.

Converges an existing account's uid, primary group, shell, home or comment to whichever of those the runbook names, refusing outright if the account does not exist rather than creating one (use identity.user.create for that). Account state is read from getent before anything is sent, so an attribute already matching what was requested is left alone, and a run that requests nothing different reports no change and no command reaches the device.

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
| `name` | `string` | yes | - | The account to modify. Must already exist. |
| `uid` | `int` | no | - | Converge the account to this numeric user ID. |
| `group` | `string` | no | - | Converge the account's primary group, by name or numeric gid. |
| `shell` | `string` | no | - | Converge the account's login shell. |
| `home` | `string` | no | - | Converge the account's home directory path. Its contents are not moved. |
| `comment` | `string` | no | - | Converge the account's GECOS comment field. |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `name` | `string` | always | The account this task acted on. |
| `diff` | `dict` | always | What getent reported about the account before this task and after it, each holding exists, uid, gid, comment, home and shell. |

## Undoing this

**Can be undone.** A run that changes something records the instruction that reverses it, as an `inverse` stat holding the method to call and the parameters to call it with, resolved from the state this run actually found. A run that changed nothing records no instruction, which is how it says that undoing it means doing nothing.

A run that converged one or more attributes emits an identity.user.modify pinned to exactly the old values of the attributes it changed, which is a real, restorable inverse. A run that found every requested attribute already matching emits nothing.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `identity.user.create`
- `identity.user.remove`

## Examples

Change a login shell:

```yaml
- name: Switch deploy to a restricted shell
  identity.user.modify:
    name: deploy
    shell: /usr/sbin/nologin
```


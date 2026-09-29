---
status: beta
---

# identity.user.remove

Removes a POSIX user account from the target.

Makes sure a user account is absent from the target, removing it if present. This is ansible.builtin.user with state=absent. Account state is read from getent before anything is sent, so an account already absent reports no change and no command reaches the device.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `PosixAccountCapable` |
| Transports | `ssh` |
| Requires elevation | yes |
| Runs | on or against the target device; acts on its target device |
| Check mode | Supported: reports what it would change and changes nothing |
| Engine version | `>=0.2.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `name` | `string` | yes | - | The account name to remove. |
| `remove` | `bool` | no | `false` | Also delete the home directory and mail spool (userdel -r). Left false, they are left on disk. |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `name` | `string` | always | The account this task acted on. |
| `diff` | `dict` | always | What getent reported about the account before this task and after it. After always reports exists: false on a successful run. |

## Undoing this

**Can be undone.** A run that changes something records the instruction that reverses it, as an `inverse` stat holding the method to call and the parameters to call it with, resolved from the state this run actually found. A run that changed nothing records no instruction, which is how it says that undoing it means doing nothing.

A run that removed a present account emits an identity.user.create pinned to the exact uid, group, shell, home and comment this run captured before removing it, which is a real, restorable inverse for the account's identity attributes. A run that found the account already absent emits nothing. What the inverse cannot restore is the account's password (never captured by this platform), or the home directory's contents once remove=true has asked userdel to delete them.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `identity.user.create`
- `identity.user.modify`

## Examples

Remove an account:

```yaml
- name: Make sure the old deploy account is gone
  identity.user.remove:
    name: deploy
```

Remove an account and its home directory:

```yaml
- name: Remove deploy entirely
  identity.user.remove:
    name: deploy
    remove: true
```


---
status: beta
---

# identity.user.create

Makes sure a POSIX user account exists on the target.

Makes sure a user account is present on the target, creating it if absent. This is ansible.builtin.user with state=present. Account state is read from getent before anything is sent, so an account already present with every requested attribute already matching reports no change and no command reaches the device; an account present with a different uid, group, shell, home or comment than requested is converged with usermod rather than recreated. Supplementary group membership and the account password are not managed by this method.

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
| `name` | `string` | yes | - | The account name to create or converge. |
| `uid` | `int` | no | - | The numeric user ID to assign. An existing account with a different uid is converged to this one. |
| `group` | `string` | no | - | The primary group, by name or numeric gid. An existing account with a different primary group is converged to this one. |
| `shell` | `string` | no | - | The login shell, such as /bin/bash. An existing account with a different shell is converged to this one. |
| `home` | `string` | no | - | The home directory path. An existing account with a different home is converged to this one; its contents are not moved. |
| `comment` | `string` | no | - | The GECOS comment field, typically the account's full name. |
| `create_home` | `bool` | no | `true` | Create the home directory when the account is created. Ignored when the account already exists. |
| `system` | `bool` | no | `false` | Create the account as a system account (useradd -r). Ignored when the account already exists. |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `name` | `string` | always | The account this task acted on. |
| `diff` | `dict` | always | What getent reported about the account before this task and after it, each holding exists, uid, gid, comment, home and shell. Recorded even on a run that changed nothing. |

## Undoing this

**Can be undone.** A run that changes something records the instruction that reverses it, as an `inverse` stat holding the method to call and the parameters to call it with, resolved from the state this run actually found. A run that changed nothing records no instruction, which is how it says that undoing it means doing nothing.

A run that created an absent account emits an identity.user.remove naming it. A run that found the account already present but converged one or more attributes (uid, group, shell, home or comment) emits an identity.user.modify pinned to exactly the old values of the attributes it changed, which is a real, restorable inverse. A run that found the account already exactly as requested emits nothing. What no inverse here can restore is the account's password, or anything a login shell or profile script did while the account existed.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `identity.user.modify`
- `identity.user.remove`
- `identity.group.create`

## Examples

Create a plain account:

```yaml
- name: Make sure deploy exists
  fqcn: identity.user.create
  params:
    name: deploy
```

Pin uid and shell:

```yaml
- name: Create a service account
  fqcn: identity.user.create
  params:
    name: appsvc
    uid: 5000
    shell: /usr/sbin/nologin
    system: true
```


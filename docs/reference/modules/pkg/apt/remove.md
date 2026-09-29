---
status: beta
---

# pkg.apt.remove

Removes a package via APT.

Makes sure a package is absent from a Debian-family host, removing it if it is present. This is ansible.builtin.apt with state=absent. Installed state is read from dpkg before anything is sent, so a package that is already absent reports no change and no command reaches the device. This removes the package but does not purge it (apt-get remove, not apt-get purge), so its configuration files are left on disk; there is no separate purge parameter today.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `AptCapable` |
| Transports | `ssh` |
| Requires elevation | yes |
| Runs | on or against the target device; acts on its target device |
| Check mode | Supported: reports what it would change and changes nothing |
| Engine version | `>=0.2.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `name` | `string` | yes | - | The APT package name to remove. |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `name` | `string` | always | The package this task acted on. |
| `version` | `string` | always | Always empty after a successful run: a removed package has no installed version, and a package that was already absent had none either. |
| `diff` | `dict` | always | What dpkg reported about the package before this task and after it, each holding installed and version. |

## Undoing this

**Can be undone.** A run that changes something records the instruction that reverses it, as an `inverse` stat holding the method to call and the parameters to call it with, resolved from the state this run actually found. A run that changed nothing records no instruction, which is how it says that undoing it means doing nothing.

A run that removed a present package emits a pkg.apt.install pinned to the exact version this run captured before removing it, which is a real, restorable inverse: reinstalling that version undoes exactly what this run did to the package database. A run that found the package already absent emits nothing. What the inverse cannot restore is anything the removal's own maintainer scripts did beyond deleting the package's files.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `pkg.apt.install`
- `pkg.apt.upgrade`
- `pkg.dnf.remove`
- `pkg.remove`

## Examples

Remove a package:

```yaml
- name: Make sure telnet is not installed
  pkg.apt.remove:
    name: telnet
```


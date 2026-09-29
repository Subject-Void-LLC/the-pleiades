---
status: beta
---

# pkg.apt.install

Makes sure a package is installed via APT.

Makes sure a package is present on a Debian-family host, installing it if it is absent. This is ansible.builtin.apt with state=present. Installed state is read from dpkg before anything is sent, so a package that is already present at the requested version (or present at any version, when none is requested) reports no change and no command reaches the device. version pins to an exact version string, the same way apt-get install name=version does; leave it unset to mean whatever apt-get would install unpinned.

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
| `name` | `string` | yes | - | The APT package name to install, such as curl or nginx. |
| `version` | `string` | no | - | Install exactly this version rather than whatever is current. A package already installed at a different version is upgraded or downgraded to match. |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `name` | `string` | always | The package this task acted on. |
| `version` | `string` | always | The version left installed after this task. |
| `diff` | `dict` | always | What dpkg reported about the package before this task and after it, each holding installed and version. Recorded even on a run that changed nothing, because "it was already like this" is what tells a later rollback to do nothing. |

## Undoing this

**Can be undone.** A run that changes something records the instruction that reverses it, as an `inverse` stat holding the method to call and the parameters to call it with, resolved from the state this run actually found. A run that changed nothing records no instruction, which is how it says that undoing it means doing nothing.

A run that installed an absent package emits a pkg.apt.remove naming it. A run that found it already present, even at a different version than requested, emits nothing: reinstalling over an existing package is not something removing it would undo correctly, since the version that was already there before this task ran would be lost along with the one this task left. What the inverse cannot restore is anything the package's install scripts did: a service they started, a user they created, a config file they wrote outside dpkg's own tracking.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `pkg.apt.remove`
- `pkg.apt.upgrade`
- `pkg.dnf.install`
- `pkg.install`

## Examples

Install a package:

```yaml
- name: Make sure curl is installed
  pkg.apt.install:
    name: curl
```

Pin an exact version:

```yaml
- name: Install a specific nginx build
  pkg.apt.install:
    name: nginx
    version: 1.24.0-2ubuntu7
```


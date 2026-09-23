---
status: beta
---

# pkg.dnf.install

Makes sure a package is installed via DNF.

Makes sure a package is present on a Red Hat-family host, installing it if it is absent. This is ansible.builtin.dnf with state=present. Installed state is read from rpm before anything is sent, so a package that is already present at the requested version (or present at any version, when none is requested) reports no change and no command reaches the device. version pins to an exact version-release string, appended to the package name the way dnf's own name-version syntax expects (name-version, e.g. nginx-1.24.0); leave it unset to mean whatever dnf would install unpinned.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `DnfCapable` |
| Transports | `ssh` |
| Requires elevation | yes |
| Check mode | Supported: reports what it would change and changes nothing |
| Engine version | `>=0.2.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `name` | `string` | yes | - | The RPM package name to install, such as curl or nginx. |
| `version` | `string` | no | - | Install exactly this version-release rather than whatever is current. A package already installed at a different version is upgraded or downgraded to match. |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `name` | `string` | always | The package this task acted on. |
| `version` | `string` | always | The version-release left installed after this task. |
| `diff` | `dict` | always | What rpm reported about the package before this task and after it, each holding installed and version. Recorded even on a run that changed nothing, because "it was already like this" is what tells a later rollback to do nothing. |

## Undoing this

**Can be undone.** A run that changes something records the instruction that reverses it, as an `inverse` stat holding the method to call and the parameters to call it with, resolved from the state this run actually found. A run that changed nothing records no instruction, which is how it says that undoing it means doing nothing.

A run that installed an absent package emits a pkg.dnf.remove naming it. A run that found it already present, even at a different version than requested, emits nothing, for the same reason pkg.apt.install's inverse does not fire on a version change: the version already there before this task ran is gone the moment dnf replaces it, so removing afterward would not restore it. What the inverse cannot restore is anything the package's scriptlets did beyond placing its own files.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `pkg.dnf.remove`
- `pkg.dnf.upgrade`
- `pkg.apt.install`
- `pkg.install`

## Examples

Install a package:

```yaml
- name: Make sure curl is installed
  pkg.dnf.install:
    name: curl
```

Pin an exact version:

```yaml
- name: Install a specific nginx build
  pkg.dnf.install:
    name: nginx
    version: 1.24.0-1.el9
```


---
status: beta
---

# pkg.install

Makes sure a package is present, whichever package manager the device runs.

Makes sure a package is installed, without caring which package manager the device uses. This is ansible.builtin.package with state=present: it resolves the device's package manager and hands the call to that manager's concrete method, so on an APT host it runs pkg.apt.install and behaves exactly as that method does, including reporting no change when the package is already present at the requested version. Use the concrete method instead when a runbook is written for one platform and should say so.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `PackageManagerCapable` |
| Transports | - |
| Requires elevation | yes |
| Engine version | `>=1.0.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `name` | `string` | yes | - | The package to act on. This is passed straight through to the concrete method for the device's package manager, so it means whatever that manager means by a package name: an APT package name on a Debian-family host, an RPM package name on a Red Hat-family one. |
| `version` | `string` | no | - | Pin to this exact version instead of whatever the package manager considers current. Passed straight through to the concrete method; leave unset to mean "whatever version is current." |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `name` | `string` | always | The package this task acted on, as recorded by the concrete method that ran. |
| `version` | `string` | always | The version left installed after this task, empty when the package is absent afterward. The exact value comes from the concrete method that ran. |
| `diff` | `dict` | always | What the package manager reported before this task and after it. The exact keys come from the concrete method that ran, since what there is to say about a package differs between package managers. |

## Undoing this

**Can be undone.** A run that changes something records the instruction that reverses it, as an `inverse` stat holding the method to call and the parameters to call it with, resolved from the state this run actually found. A run that changed nothing records no instruction, which is how it says that undoing it means doing nothing.

The inverse is whatever the concrete method records, so on an APT host a run that installed an absent package emits a pkg.apt.remove and a run that found it already present emits nothing. The instruction names the concrete method rather than pkg.remove, which is deliberate: by the time an undo runs, the device that was resolved is the device that must be undone.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `pkg.remove`
- `pkg.upgrade`
- `pkg.apt.install`
- `pkg.dnf.install`

## Examples

Install a package without naming the package manager:

```yaml
- name: Make sure curl is installed
  fqcn: pkg.install
  params:
    name: curl
```


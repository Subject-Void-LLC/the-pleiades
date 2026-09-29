---
status: beta
---

# pkg.upgrade

Makes sure the newest available version of a package is installed, whichever package manager the device runs.

Makes sure a package is at its newest available version, without caring which package manager the device uses. This is ansible.builtin.package with state=latest for one named package, not a full-system upgrade: it resolves the device's package manager and hands the call to that manager's concrete method, so on an APT host it runs pkg.apt.upgrade and behaves exactly as that method does, including installing the package fresh when it is absent and reporting no change when it is already current. Use the concrete method instead when a runbook is written for one platform and should say so.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `PackageManagerCapable` |
| Transports | - |
| Requires elevation | yes |
| Runs | on or against the target device; acts on its target device |
| Check mode | Supported: reports what it would change and changes nothing |
| Engine version | `>=0.2.0` |

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

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

Every concrete upgrade method this resolves to is itself irreversible: downgrading a package is not something apt or dnf reliably support once the previous version has left the local cache and the repository's own metadata, so no method in this namespace can promise a safe undo for it.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `pkg.install`
- `pkg.remove`
- `pkg.apt.upgrade`
- `pkg.dnf.upgrade`

## Examples

Keep a package current without naming the package manager:

```yaml
- name: Keep openssl at its newest available version
  pkg.upgrade:
    name: openssl
```


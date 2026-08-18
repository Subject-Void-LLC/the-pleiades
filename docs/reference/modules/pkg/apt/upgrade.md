---
status: beta
---

# pkg.apt.upgrade

Makes sure a package is at its newest available version via APT.

Makes sure one named package is at the newest version APT knows about, upgrading it if a newer one is available. This is ansible.builtin.apt with state=latest for a single package, not apt-get upgrade or apt-get dist-upgrade: it never touches any package other than the one named. A package that is absent is installed fresh, since there is no "current" version to upgrade from. A package that is already at the newest candidate version reports no change and no command reaches the device.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `AptCapable` |
| Transports | `ssh` |
| Requires elevation | yes |
| Engine version | `>=1.0.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `name` | `string` | yes | - | The APT package to keep current. |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `name` | `string` | always | The package this task acted on. |
| `version` | `string` | always | The version left installed after this task. |
| `diff` | `dict` | always | What dpkg reported about the package before this task and after it, each holding installed and version. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

Downgrading a package is not something apt-get reliably supports once the previous version has left the local cache and the repository's own metadata (a mirror generally keeps only the latest build of a package), so no safe inverse can be promised here. This mirrors exec.command's own reasoning for why it cannot be undone: the platform does not control whether the information an undo would need still exists anywhere reachable.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `pkg.apt.install`
- `pkg.apt.remove`
- `pkg.dnf.upgrade`
- `pkg.upgrade`

## Examples

Keep a package at its newest available version:

```yaml
- name: Keep openssl current
  fqcn: pkg.apt.upgrade
  params:
    name: openssl
```


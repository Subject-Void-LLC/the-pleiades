---
status: beta
---

# virt.clab.server.version_check

Asks whether a newer containerlab exists.

Returns containerlab's own answer, which reaches the internet from the server. It only reads, so a check is the same call. The target is the containerlab API server, a generic_http device that onboarding recognised (it declares ContainerlabAPICapable); its login is the device's stored credential, with http_auth basic, and neither the password nor the token it gets is logged, returned or journaled.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `ContainerlabAPICapable` |
| Transports | `https` |
| Requires elevation | no |
| Runs | in the host process (the CLI or a Runner); acts on its target device |
| Check mode | Supported: reports what it would change and changes nothing |
| Engine version | `>=0.3.0` |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `result` | `string` | always | containerlab's answer. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

It only reads the server, so there is nothing to undo.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `virt.clab.server.version`

## Examples

Asks whether a newer containerlab exists.:

```yaml
- name: Is it current
  virt.clab.server.version_check:
    target: clab-api
```


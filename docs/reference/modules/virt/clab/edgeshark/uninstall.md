---
status: beta
---

# virt.clab.edgeshark.uninstall

Uninstalls Edgeshark from the server's host, if it is installed.

Reads Edgeshark's status and removes it only when it is installed. Its undo is virt.clab.edgeshark.install, recorded when this task removed it. The target is the containerlab API server, a generic_http device that onboarding recognised (it declares ContainerlabAPICapable); its login is the device's stored credential, with http_auth basic, and neither the password nor the token it gets is logged, returned or journaled.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `ContainerlabAPICapable` |
| Transports | `https` |
| Requires elevation | no |
| Runs | in the host process (the CLI or a Runner); acts on its target device |
| Check mode | Supported: reports what it would change and changes nothing |
| Engine version | `>=0.3.0` |

## Undoing this

**Can be undone.** A run that changes something records the instruction that reverses it, as an `inverse` stat holding the method to call and the parameters to call it with, resolved from the state this run actually found. A run that changed nothing records no instruction, which is how it says that undoing it means doing nothing.

Its undo is virt.clab.edgeshark.install, recorded when this task removed it.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `virt.clab.edgeshark.install`
- `virt.clab.edgeshark.status`

## Examples

Remove capture:

```yaml
- name: Edgeshark is gone
  virt.clab.edgeshark.uninstall:
    target: clab-api
```


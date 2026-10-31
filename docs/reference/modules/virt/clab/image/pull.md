---
status: beta
---

# virt.clab.image.pull

Pulls an image onto the server, unless it is already there.

Pulls the image the reference names into the server's runtime. An image already present under that reference is reported unchanged. Its undo is virt.clab.image.remove, recorded when this task pulled it. The target is the containerlab API server, a generic_http device that onboarding recognised (it declares ContainerlabAPICapable); its login is the device's stored credential, with http_auth basic, and neither the password nor the token it gets is logged, returned or journaled.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `ContainerlabAPICapable` |
| Transports | `https` |
| Requires elevation | no |
| Runs | in the host process (the CLI or a Runner); acts on its target device |
| Check mode | Supported: reports what it would change and changes nothing |
| Engine version | `>=0.3.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `image` | `string` | yes | - | The image reference, such as alpine:3.20.10. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `present` | `bool` | always | Whether the image is on the server now. |

## Undoing this

**Can be undone.** A run that changes something records the instruction that reverses it, as an `inverse` stat holding the method to call and the parameters to call it with, resolved from the state this run actually found. A run that changed nothing records no instruction, which is how it says that undoing it means doing nothing.

Its undo removes the image, recorded when this task pulled it.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `virt.clab.image.remove`
- `virt.clab.image.list`

## Examples

Have the node image:

```yaml
- name: alpine is on the server
  virt.clab.image.pull:
    target: clab-api
    image: alpine:3.20.10
```


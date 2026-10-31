---
status: beta
---

# virt.clab.image.remove

Removes an image from the server, if it is there.

Removes the image the reference names. An image that is not there is reported unchanged. Its undo is a pull of the same reference, which is partial: the tag comes back but the layers may be a newer build. The target is the containerlab API server, a generic_http device that onboarding recognised (it declares ContainerlabAPICapable); its login is the device's stored credential, with http_auth basic, and neither the password nor the token it gets is logged, returned or journaled.

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
| `image` | `string` | yes | - | The image reference. |
| `force` | `bool` | no | `false` | Remove it even when a container uses it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `present` | `bool` | always | Whether the image is on the server now. |

## Undoing this

**Can be undone.** A run that changes something records the instruction that reverses it, as an `inverse` stat holding the method to call and the parameters to call it with, resolved from the state this run actually found. A run that changed nothing records no instruction, which is how it says that undoing it means doing nothing.

Its undo pulls the same reference, which is partial: the tag comes back, though the layers may be a newer build.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `virt.clab.image.pull`

## Examples

Drop an image:

```yaml
- name: No old image
  virt.clab.image.remove:
    target: clab-api
    image: alpine:3.19
```


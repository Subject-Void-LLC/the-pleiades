---
status: beta
---

# container.image.pull

Pulls an image by digest and names it by its tag.

Makes the image a reference names present on the engine. A tag is resolved through its registry to the digest it names now (a multi-platform image's index, so it means the same on every machine), the image is pulled by that digest when absent, and the tag is then pointed at it on the engine; an image named by digest is pulled as named. The registry credential bound for the image's registry is used when there is one. Present and tagged is no change. A run that pulled an absent image records its removal as the undo (nothing can be using an image that was not there); one that only moved or added the tag records moving it back or removing it. A check resolves the digest and reads the engine, and pulls and tags nothing. The engine is reached the first way onboarding proved for the device (this machine's socket, an SSH channel to the engine's socket, the engine's own dial-stdio over SSH, or mutual TLS), its identity checked against the one onboarding recorded, and every request outside the operations this method declares is refused before it reaches the engine.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `ContainerEngineCapable` |
| Transports | `engine_local`, `engine_socket`, `engine_stdio`, `engine_tls` |
| Requires elevation | no |
| Runs | on or against the target device; acts on its target device |
| Check mode | Supported: reports what it would change and changes nothing |
| Engine version | `>=0.2.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `engine` | `string` | no | - | Which container engine to use on a device that runs more than one. Needed only there: on a device with one engine, that engine is used. An engine the device does not run is refused. |
| `image` | `string` | yes | - | The image: a reference by tag (resolved to its digest), or by digest. |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task, when the engine is reached over SSH. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `image` | `string` | always | The image by digest. |
| `digest` | `string` | always | The image's digest. |
| `image_id` | `string` | when the image is on the engine | The engine's ID for the image. |
| `pulled` | `bool` | always | Whether the image had to be pulled (by a check, would be). |
| `tagged` | `bool` | always | Whether the tag had to be pointed at the image (by a check, would be). |

## Undoing this

**Can be undone.** A run that changes something records the instruction that reverses it, as an `inverse` stat holding the method to call and the parameters to call it with, resolved from the state this run actually found. A run that changed nothing records no instruction, which is how it says that undoing it means doing nothing.

A run that pulled an absent image records its removal, by ID; one that only pointed the tag at the image records pointing it back, or removing the tag when it was new; one that found both as asked records nothing.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `container.image.resolve`
- `container.image.remove`
- `container.run`

## Examples

Pull a pinned web server:

```yaml
- name: Pull nginx
  container.image.pull:
    image: nginx:1.28.1-alpine
```


---
status: beta
---

# container.image.push

Pushes an image to its registry.

Pushes the local image a tag names to the registry the tag names, with the registry credential bound for that registry when there is one. The registry is asked first what the tag names there; when that is already the local image, nothing is sent and there is no change. A registry that cannot say (a repository not yet created) is pushed to. A push has no undo: the engine has no operation that deletes from a registry. A check reads the image and asks the registry, and sends nothing. The engine is reached the first way onboarding proved for the device (this machine's socket, an SSH channel to the engine's socket, the engine's own dial-stdio over SSH, or mutual TLS), its identity checked against the one onboarding recorded, and every request outside the operations this method declares is refused before it reaches the engine.

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
| `image` | `string` | yes | - | The image by tag, registry included (ghcr.io/org/app:1.2). A reference by digest is refused: a registry is pushed to by tag. |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task, when the engine is reached over SSH. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `image` | `string` | always | The image's reference. |
| `image_id` | `string` | when the image is on the engine | The engine's ID for the image. |
| `digest` | `string` | when the registry holds the image | The digest the registry's tag names, after the push. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

A push changes the registry, which the engine has no operation to undo: a tag it moved cannot be moved back, nor a pushed image deleted, through the engine.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `container.image.tag`
- `container.registry.login`

## Examples

Push a release:

```yaml
- name: Publish
  container.image.push:
    image: registry.example.com/app:1.4.2
```


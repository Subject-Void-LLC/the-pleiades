---
status: beta
---

# container.image.remove

Removes an image or one of its tags.

Removes the reference image names: a tag is removed, and the image with it when it was the image's last name; an image ID removes the image and every name it has. An absent image is no change. An image a container uses, in any state, is refused unless force is set. With id, a reference that now names another image is refused, so an undo never removes an image that took the tag since. A removal has no undo. A check reads the image and the containers, and removes nothing. The engine is reached the first way onboarding proved for the device (this machine's socket, an SSH channel to the engine's socket, the engine's own dial-stdio over SSH, or mutual TLS), its identity checked against the one onboarding recorded, and every request outside the operations this method declares is refused before it reaches the engine.

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
| `engine` | `string` | no | - | Which container engine to use on a device that runs more than one. Needed only there: on a device with one engine, that engine is used. An engine the device does not run is refused. **Choices:** `docker`, `podman`. |
| `image` | `string` | yes | - | The image: a reference, or an image ID. |
| `id` | `string` | no | - | The image ID the reference must still name. |
| `force` | `bool` | no | `false` | Remove an image a stopped container uses; the engine still refuses one a running container uses. On Podman, force also deletes the stopped containers using the image, which Docker leaves. |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task, when the engine is reached over SSH. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `image` | `string` | always | The reference the task named. |
| `image_id` | `string` | when the image is on the engine | The engine's ID for the image. |
| `used_by` | `list of string` | when the image exists | The containers using the image, by name. |
| `removed` | `list of string` | when it was removed | The tags the engine removed and the image IDs it deleted. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

A removed image's content is gone from the engine; container.image.pull brings an image from a registry back, and container.image.load one from a saved archive.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `container.image.prune`
- `container.remove`

## Examples

Remove an old build:

```yaml
- name: Remove build 117
  container.image.remove:
    image: app:build-117
```


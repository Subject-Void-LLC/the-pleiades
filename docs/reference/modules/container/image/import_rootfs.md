---
status: beta
---

# container.image.import_rootfs

Makes an image from a filesystem archive.

Imports the filesystem archive at path (a tar of a root filesystem, as container export writes it) as a one-layer image the tag names. With digest, an archive with any other digest is refused before the engine reads it, as for container.image.load. An import makes a new image each time, so it always reports a change. A run records pointing the tag back at the image it named before as the undo, or removing the imported image when the tag was new. A check reads and hashes the archive, and imports nothing. The archive is on the machine the engine runs on: this machine's own files when the engine is reached through its local socket, and otherwise the device's, over its SSH login. The engine is reached the first way onboarding proved for the device (this machine's socket, an SSH channel to the engine's socket, the engine's own dial-stdio over SSH, or mutual TLS), its identity checked against the one onboarding recorded, and every request outside the operations this method declares is refused before it reaches the engine.

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
| `path` | `string` | yes | - | The archive's path on the machine the engine runs on: absolute and clean. |
| `image` | `string` | yes | - | The tag the new image gets, repository:tag; a missing tag is latest. |
| `digest` | `string` | no | - | The archive's digest, sha256:<hex>, as container.image.save returned it. An archive with any other digest is refused before the engine reads a byte of it. |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task, when the engine is reached over SSH. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `path` | `string` | always | The archive's path. |
| `digest` | `string` | always | The archive's digest, sha256:<hex>. |
| `size` | `int` | always | The archive's size in bytes. |
| `image` | `string` | always | The tag the image gets. |
| `image_id` | `string` | when it ran | The new image's ID. |

## Undoing this

**Can be undone.** A run that changes something records the instruction that reverses it, as an `inverse` stat holding the method to call and the parameters to call it with, resolved from the state this run actually found. A run that changed nothing records no instruction, which is how it says that undoing it means doing nothing.

A run records pointing the tag back at the image it named before, or removing the imported image, by ID, when the tag was new.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `container.image.load`

## Examples

Import a root filesystem:

```yaml
- name: Import the rootfs
  container.image.import_rootfs:
    path: /srv/rootfs/base.tar
    image: base:2026-10
```


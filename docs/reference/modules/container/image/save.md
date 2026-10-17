---
status: beta
---

# container.image.save

Saves images to an archive and returns its digest.

Writes the images named, with their tags, as one archive at path, and returns the archive's digest, which container.image.load takes to refuse any other archive: one build's archive serves every later task. The archive is written to a private temporary (mode 0600) beside path and renamed over it, so a reader sees the old file or the new one; a file already holding the same archive is left as it is and is no change. A missing image is refused. The archive replaces what was at path, which is not kept, so a save has no undo. A check reads the images and streams the archive through a hash, writing nothing, so it predicts the digest exactly. The archive is on the machine the engine runs on: this machine's own files when the engine is reached through its local socket, and otherwise the device's, over its SSH login. The engine is reached the first way onboarding proved for the device (this machine's socket, an SSH channel to the engine's socket, the engine's own dial-stdio over SSH, or mutual TLS), its identity checked against the one onboarding recorded, and every request outside the operations this method declares is refused before it reaches the engine.

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
| `images` | `list` | yes | - | The images to save, by reference or ID. |
| `path` | `string` | yes | - | The archive's path on the machine the engine runs on: absolute and clean. |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task, when the engine is reached over SSH. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `path` | `string` | always | The archive's path. |
| `digest` | `string` | always | The archive's digest, sha256:<hex>. |
| `size` | `int` | always | The archive's size in bytes. |
| `images` | `list of string` | always | The saved images' IDs. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

An archive this task wrote replaces whatever was at the path, which is not kept; file.remove removes the archive when it is no longer needed.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `container.image.load`
- `file.remove`

## Examples

Save a build for later tasks:

```yaml
- name: Save the build
  container.image.save:
    images: [app:build-118]
    path: /var/lib/builds/app-118.tar
  register: archive
```


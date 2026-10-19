---
status: beta
---

# container.image.load

Loads images from an archive, refusing one whose digest differs.

Loads the archive at path, as container.image.save or docker save wrote it, into the engine. With digest, an archive with any other digest is refused before the engine reads it: the archive is copied to a private temporary file on the machine running the task, hashed on the way, and the engine loads that copy, so what it loads is what was checked. Loading always reports a change, and has no undo, since a load can move tags from the images they named and the engine does not say which. A check reads and hashes the archive, and loads nothing. The archive is on the machine the engine runs on: this machine's own files when the engine is reached through its local socket, and otherwise the device's, over its SSH login. The engine is reached the first way onboarding proved for the device (this machine's socket, an SSH channel to the engine's socket, the engine's own dial-stdio over SSH, or mutual TLS), its identity checked against the one onboarding recorded, and every request outside the operations this method declares is refused before it reaches the engine.

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
| `digest` | `string` | no | - | The archive's digest, sha256:<hex>, as container.image.save returned it. An archive with any other digest is refused before the engine reads a byte of it. |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task, when the engine is reached over SSH. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `path` | `string` | always | The archive's path. |
| `digest` | `string` | always | The archive's digest, sha256:<hex>. |
| `size` | `int` | always | The archive's size in bytes. |
| `images` | `list of string` | when it ran | The images the engine said it loaded, by tag or ID. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

A load can move tags the archive names from the images they named before, which the engine does not report, so what to put back is not known.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `container.image.save`
- `container.image.import`

## Examples

Load the saved build:

```yaml
- name: Save the build
  container.image.save:
    images: [app:build-118]
    path: /var/lib/builds/app-118.tar
  register: archive

- name: Load the build
  container.image.load:
    path: /var/lib/builds/app-118.tar
    digest: "{{ result.archive.digest }}"
```


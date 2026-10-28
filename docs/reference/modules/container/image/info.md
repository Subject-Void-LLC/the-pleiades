---
status: beta
---

# container.image.info

Reports images on the engine.

Reports each image named, or every image the engine lists when none is named (an untagged one with no tags): ID, tags, repository digests, creation time, size and labels, and for a named image also its OS, architecture and variant and what a container made from it runs: entrypoint, command, user, working directory, exposed ports, volumes and environment variable names. A named image that is absent is listed under missing rather than failing the task. An image's environment values are never reported, since a value baked into an image can be a secret. It changes nothing; a check is the same read. The engine is reached the first way onboarding proved for the device (this machine's socket, an SSH channel to the engine's socket, the engine's own dial-stdio over SSH, or mutual TLS), its identity checked against the one onboarding recorded, and every request outside the operations this method declares is refused before it reaches the engine.

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
| `images` | `list` | no | - | The images to report, by reference or ID. Absent means every image. |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task, when the engine is reached over SSH. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `images` | `list of dict` | always | One entry per image: id, tags, digests, created, size and labels; for every image listed (none named) also containers, how many containers use it; for an image named also os, architecture, variant, entrypoint, command, user, working_dir, exposed_ports, volumes and env_names (names only, never a value). |
| `missing` | `list of string` | always | The images named that the engine does not have. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

It reads and changes nothing, so there is nothing to undo.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `container.image.pull`
- `container.image.resolve`

## Examples

Is the release here:

```yaml
- name: Read the release image
  container.image.info:
    images: [registry.example.com/app:1.4.2]
  register: release
```


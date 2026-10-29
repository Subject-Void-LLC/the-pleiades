---
status: beta
---

# container.image.resolve

Resolves a tag to the digest it names in its registry.

Asks the registry a reference names, through the engine (so through the device's own network, proxy and mirrors), which digest its tag names now: a multi-platform image's index, as `docker buildx imagetools inspect` reports it, so it means the same on any machine. The registry credential bound for that registry is used when there is one. A tag the registry answers not found for is not a failure: exists is false and image is the reference asked for, so a later task can decide what to do. Any other refusal fails the task, a registry that answers unauthorized for a repository it does not show included, since that does not say the tag is absent. It changes nothing; a check is the same read, an absent tag included. The engine is reached the first way onboarding proved for the device (this machine's socket, an SSH channel to the engine's socket, the engine's own dial-stdio over SSH, or mutual TLS), its identity checked against the one onboarding recorded, and every request outside the operations this method declares is refused before it reaches the engine.

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
| `image` | `string` | yes | - | The image by tag; a missing tag is latest. |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task, when the engine is reached over SSH. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `exists` | `bool` | always | Whether the registry holds the tag. |
| `image` | `string` | always | The image by digest, or the reference asked for when the tag does not exist. |
| `digest` | `string` | when exists | The digest the tag names. |
| `media_type` | `string` | when exists | Whether the digest is an index or a single image's manifest, as its media type. |
| `platforms` | `list of string` | when exists | The platforms the image is built for, os/arch[/variant]. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

It reads and changes nothing, so there is nothing to undo.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `container.image.pull`
- `build.digests.refresh`

## Examples

Pin a base image:

```yaml
- name: Resolve the base
  container.image.resolve:
    image: alpine:3.20
  register: base
```


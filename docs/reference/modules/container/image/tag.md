---
status: beta
---

# container.image.tag

Names an image by another tag.

Points tag at the image source names (a reference or an image ID). A tag already naming that image is no change; one naming another image is moved. A missing source is refused. A run that moved the tag records moving it back as the undo, and one that added a new tag records removing it. A check reads both and tags nothing. The engine is reached the first way onboarding proved for the device (this machine's socket, an SSH channel to the engine's socket, the engine's own dial-stdio over SSH, or mutual TLS), its identity checked against the one onboarding recorded, and every request outside the operations this method declares is refused before it reaches the engine.

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
| `source` | `string` | yes | - | The image to name: a reference or an image ID. |
| `tag` | `string` | yes | - | The tag to point at it, repository:tag; a missing tag is latest. A reference by digest is refused. |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task, when the engine is reached over SSH. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `image` | `string` | always | The tag. |
| `image_id` | `string` | when the image is on the engine | The engine's ID for the image. |

## Undoing this

**Can be undone.** A run that changes something records the instruction that reverses it, as an `inverse` stat holding the method to call and the parameters to call it with, resolved from the state this run actually found. A run that changed nothing records no instruction, which is how it says that undoing it means doing nothing.

A run that moved the tag records pointing it back at the image it named before; one that added a new tag records removing it, by the image's ID; one that found the tag already there records nothing.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `container.image.push`
- `container.image.remove`

## Examples

Name a build for its registry:

```yaml
- name: Tag for the registry
  container.image.tag:
    source: app:build-118
    tag: registry.example.com/app:1.4.2
```


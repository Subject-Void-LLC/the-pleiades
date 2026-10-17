---
status: beta
---

# container.image.prune

Keeps each repository's newest tags and removes the rest.

For each repository on the engine whose name matches repositories, orders its tags as versions, as `sort -V` does (1.10 after 1.9; a word such as latest after every number), keeps the keep_newest newest and removes the rest. A tag of an image a container uses, in any state, is never removed, and does not count toward keep_newest. Removing a tag removes the image only when it was the image's last name. Nothing is forced, so a container that starts using an image meanwhile stops the prune there. A prune has no undo. A check reads the images and containers and removes nothing, returning what a run would remove. The engine is reached the first way onboarding proved for the device (this machine's socket, an SSH channel to the engine's socket, the engine's own dial-stdio over SSH, or mutual TLS), its identity checked against the one onboarding recorded, and every request outside the operations this method declares is refused before it reaches the engine.

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
| `repositories` | `string` | yes | - | A pattern over repository names as the engine shows them (app, ghcr.io/org/app): * matches any run of characters but /, ? one character, [...] a set. |
| `keep_newest` | `int` | yes | - | How many of each repository's newest tags to keep, from 0 to 1000. |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task, when the engine is reached over SSH. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `kept` | `list of string` | always | The tags kept as the newest. |
| `kept_in_use` | `list of string` | always | The older tags kept because a container uses their image. |
| `removed` | `list of string` | always | The tags removed (by a check, that would be). |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

A pruned tag, and an image whose last tag it was, is gone from the engine; container.image.pull brings an image back from its registry.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `container.image.remove`

## Examples

Keep the last three builds:

```yaml
- name: Prune old builds
  container.image.prune:
    repositories: registry.example.com/app
    keep_newest: 3
```


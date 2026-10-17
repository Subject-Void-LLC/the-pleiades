---
status: beta
---

# container.image.search

Searches a registry for images.

Runs the registry's search through the engine, anonymously: Docker Hub unless term names another registry. Each result's name, description, stars and whether it is official are returned, with control characters dropped from the registry's text. It changes nothing; a check is the same read. The engine is reached the first way onboarding proved for the device (this machine's socket, an SSH channel to the engine's socket, the engine's own dial-stdio over SSH, or mutual TLS), its identity checked against the one onboarding recorded, and every request outside the operations this method declares is refused before it reaches the engine.

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
| `term` | `string` | yes | - | What to search for. |
| `limit` | `int` | no | `25` | The most results to return, from 1 to 100. |
| `official_only` | `bool` | no | `false` | Return official images only. |
| `min_stars` | `int` | no | `0` | Return images with at least this many stars only. |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task, when the engine is reached over SSH. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `results` | `list of dict` | always | One entry per image found: name, description, stars, official. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

It reads and changes nothing, so there is nothing to undo.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `container.image.pull`

## Examples

Find an official image:

```yaml
- name: Search
  container.image.search:
    term: postgres
    official_only: true
    limit: 5
```


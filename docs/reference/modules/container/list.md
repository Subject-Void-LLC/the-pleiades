---
status: beta
---

# container.list

Lists the engine's containers.

Lists the engine's running containers, or every container with all, optionally only those whose name matches a glob, sorted by name: each one's name, ID, state, image ID, the image reference a container.* method recorded, and labels. More than 1000 containers is refused rather than cut; narrow it with match. It changes nothing, so a check is the same read. The engine is reached the first way onboarding proved for the device (this machine's socket, an SSH channel to the engine's socket, the engine's own dial-stdio over SSH, or mutual TLS), its identity checked against the one onboarding recorded, and every request outside the operations this method declares is refused before it reaches the engine.

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
| `all` | `bool` | no | `false` | List stopped containers too. |
| `match` | `string` | no | - | Only containers whose name matches this glob (web-*). |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task, when the engine is reached over SSH. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `containers` | `list of dict` | always | The containers, each with name, id, state, image_id, image and labels. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

It reads what the engine reports, and changes nothing.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `container.info`

## Examples

Every web container, stopped ones too:

```yaml
- name: Web containers
  container.list:
    all: true
    match: web-*
  register: webs
```


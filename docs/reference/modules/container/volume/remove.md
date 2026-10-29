---
status: beta
---

# container.volume.remove

Removes a volume no container uses.

Removes the volume named name and its data; an absent one is no change. A volume any container mounts, running or stopped, is refused, naming them. With created_at, a volume the engine made at another moment is refused, so an undo never removes another volume made under the name since. A removal has no undo: the data is gone. A check reads the volume and the containers using it, and removes nothing. The engine is reached the first way onboarding proved for the device (this machine's socket, an SSH channel to the engine's socket, the engine's own dial-stdio over SSH, or mutual TLS), its identity checked against the one onboarding recorded, and every request outside the operations this method declares is refused before it reaches the engine.

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
| `name` | `string` | yes | - | The volume's name. |
| `created_at` | `string` | no | - | When the engine made the volume, as container.volume.create returned it, which the volume named must still have. |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task, when the engine is reached over SSH. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `name` | `string` | always | The volume's name. |
| `created_at` | `string` | when the volume exists | When the engine made the volume, as it reports it. |
| `removed` | `bool` | when the volume existed | Whether the volume was removed (by a check, would be). |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

A removed volume's data is gone: container.volume.create makes an empty one, and nothing brings the data back.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `container.volume.create`
- `container.remove`

## Examples

Remove a scratch volume:

```yaml
- name: Remove the scratch volume
  container.volume.remove:
    name: scratch
```


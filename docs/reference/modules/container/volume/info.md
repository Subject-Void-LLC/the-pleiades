---
status: beta
---

# container.volume.info

Reads a volume.

Reads the volume named name as the engine reports it: its driver, mount point on the host, scope, labels, when it was made, and its driver options by name only, since a local volume's mount options can carry a password. An absent volume is reported as exists false rather than refused, so a runbook can branch on it. Reading changes nothing, and a check reads the same way. The engine is reached the first way onboarding proved for the device (this machine's socket, an SSH channel to the engine's socket, the engine's own dial-stdio over SSH, or mutual TLS), its identity checked against the one onboarding recorded, and every request outside the operations this method declares is refused before it reaches the engine.

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
| `name` | `string` | yes | - | The volume's name. |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task, when the engine is reached over SSH. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `name` | `string` | always | The volume's name. |
| `exists` | `bool` | always | Whether the volume exists. |
| `driver` | `string` | when the volume exists | The volume's driver. |
| `mountpoint` | `string` | when the volume exists | Where the volume's data is on the host. |
| `scope` | `string` | when the volume exists | The volume's scope: local, or global. |
| `created_at` | `string` | when the volume exists | When the engine made the volume, as it reports it. |
| `labels` | `dict` | when the volume exists | The volume's labels. |
| `option_names` | `list of string` | when the volume exists | The names of the volume's driver options. Their values are never reported. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

Reading is not changing: the method reads one volume and changes nothing.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `container.volume.create`
- `container.network.info`

## Examples

Read a volume a later task branches on:

```yaml
- name: Read pgdata
  container.volume.info:
    name: pgdata
  register: pgdata
```


---
status: beta
---

# container.volume.create

Makes a named volume exist as declared.

Makes the volume named name exist on the device's engine, with the local driver and the declared labels. An absent volume is created; one that matches is no change; one whose driver or a declared label differs is refused, naming each difference, and never replaced, since that would lose its data. Driver options are not offered: a local volume's mount options can carry a password. Records removing the volume as the undo, pinned to the moment the engine made it, so the undo never removes another volume made under the name since. A check reads the volume and creates nothing. The engine is reached the first way onboarding proved for the device (this machine's socket, an SSH channel to the engine's socket, the engine's own dial-stdio over SSH, or mutual TLS), its identity checked against the one onboarding recorded, and every request outside the operations this method declares is refused before it reaches the engine.

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
| `driver` | `string` | no | `local` | The volume driver: local, the engine's own, which keeps the volume's data on the host. |
| `labels` | `dict` | no | - | Labels on the volume. Labels it has beyond these are allowed. |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task, when the engine is reached over SSH. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `name` | `string` | always | The volume's name. |
| `driver` | `string` | always | The volume's driver. |
| `created_at` | `string` | when the volume exists | When the engine made the volume, as it reports it. |

## Undoing this

**Can be undone.** A run that changes something records the instruction that reverses it, as an `inverse` stat holding the method to call and the parameters to call it with, resolved from the state this run actually found. A run that changed nothing records no instruction, which is how it says that undoing it means doing nothing.

A run that created the volume records its removal, pinned to the moment the engine made it; one that found it as declared records nothing.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `container.volume.remove`
- `container.volume.info`
- `container.run`

## Examples

A volume for a database's data:

```yaml
- name: Database data
  container.volume.create:
    name: pgdata
    labels:
      app: shop
```


---
status: beta
---

# container.prune

Removes unused containers, images, volumes or networks.

Removes the unused objects of the kind what names: stopped containers (created, exited or dead), dangling images, anonymous volumes no container uses (a named volume holds data someone named, and is never pruned), or networks no container uses (the engine's own bridge, host and none never). labels narrows it to objects carrying each label (key or key=value). The objects are listed first and removed by ID, never forced, so a run removes exactly what its check listed, and one that came into use since is refused by the engine and reported. More than 500 is refused rather than cut; narrow it with labels. A prune has no undo. A check lists and removes nothing. The engine is reached the first way onboarding proved for the device (this machine's socket, an SSH channel to the engine's socket, the engine's own dial-stdio over SSH, or mutual TLS), its identity checked against the one onboarding recorded, and every request outside the operations this method declares is refused before it reaches the engine.

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
| `what` | `string` | yes | - | What to prune. |
| `labels` | `list` | no | - | Only objects carrying every one of these labels, key or key=value. |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task, when the engine is reached over SSH. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `pruned` | `list of string` | always | The names (or IDs) removed, or that would be. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

A pruned container's writable layer, image or volume's data, or network is gone.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `container.remove`

## Examples

Clear finished job containers:

```yaml
- name: Old job containers
  container.prune:
    what: containers
    labels: ["role=job"]
```


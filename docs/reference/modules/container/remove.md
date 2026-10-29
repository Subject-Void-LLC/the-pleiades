---
status: beta
---

# container.remove

Removes a container.

Removes the container named name; an absent one is no change. A running, restarting or paused container is refused unless force is set, which kills it first. remove_volumes also removes the anonymous volumes the container's image declared; named volumes are never removed here. With id, a container that no longer has that ID is refused, so an undo never removes another container that took the name. A removal has no undo. A check removes nothing. The engine is reached the first way onboarding proved for the device (this machine's socket, an SSH channel to the engine's socket, the engine's own dial-stdio over SSH, or mutual TLS), its identity checked against the one onboarding recorded, and every request outside the operations this method declares is refused before it reaches the engine.

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
| `name` | `string` | yes | - | The container's name. |
| `id` | `string` | no | - | The container's ID, which the container named must still have: an undo recorded against one container never acts on another that took its name since. |
| `force` | `bool` | no | `false` | Remove a running container, killing it first. |
| `remove_volumes` | `bool` | no | `false` | Also remove the anonymous volumes the container's image declared. |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task, when the engine is reached over SSH. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `name` | `string` | always | The container's name. |
| `id` | `string` | when the container exists | The container's ID. |
| `state` | `string` | when the container exists or would | The container's state as the engine names it (created, running, exited, paused), after the task. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

A removed container's writable layer, and any anonymous volume removed with it, is gone; container.run makes a new one from its declaration.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `container.stop`
- `container.recreate`

## Examples

Remove an old container:

```yaml
- name: Remove the old web
  container.remove:
    name: web-old
    force: true
```


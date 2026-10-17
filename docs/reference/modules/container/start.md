---
status: beta
---

# container.start

Starts a stopped container.

Starts the container named name when it is created or exited; a running one is left alone, and a paused or dead one is refused. With id, a container that no longer has that ID is refused, so an undo never starts another container that took the name. Records stopping it as the undo. A check reads the container and starts nothing. The engine is reached the first way onboarding proved for the device (this machine's socket, an SSH channel to the engine's socket, the engine's own dial-stdio over SSH, or mutual TLS), its identity checked against the one onboarding recorded, and every request outside the operations this method declares is refused before it reaches the engine.

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
| `name` | `string` | yes | - | The container's name. |
| `id` | `string` | no | - | The container's ID, which the container named must still have: an undo recorded against one container never acts on another that took its name since. |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task, when the engine is reached over SSH. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `name` | `string` | always | The container's name. |
| `id` | `string` | when the container exists | The container's ID. |
| `state` | `string` | when the container exists or would | The container's state as the engine names it (created, running, exited, paused), after the task. |

## Undoing this

**Can be undone.** A run that changes something records the instruction that reverses it, as an `inverse` stat holding the method to call and the parameters to call it with, resolved from the state this run actually found. A run that changed nothing records no instruction, which is how it says that undoing it means doing nothing.

A run that changed the container's state records the opposite change, by ID; one that found it already so records nothing.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `container.stop`
- `container.run`

## Examples

Start the backup job:

```yaml
- name: Start backup
  container.start:
    name: backup
```


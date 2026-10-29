---
status: beta
---

# container.network.remove

Removes a network no container uses.

Removes the network named name; an absent one is no change. A network any container uses, running or stopped, is refused, naming them: a stopped container that declares the network could not start once it is gone. The engine's own networks are refused. With id, a network that no longer has that ID is refused, so an undo never removes another network that took the name. Records creating the network again with the settings it had as the undo: driver, internal, IPv6, its first subnet with its gateway and range, MTU and parent are kept in the journal, and labels are not, so an undo for a labeled network cannot be replayed from the journal. The undo is partial when the network had an option or a second subnet no parameter of container.network.create sets. A check reads the network and the containers using it, and removes nothing. The engine is reached the first way onboarding proved for the device (this machine's socket, an SSH channel to the engine's socket, the engine's own dial-stdio over SSH, or mutual TLS), its identity checked against the one onboarding recorded, and every request outside the operations this method declares is refused before it reaches the engine.

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
| `name` | `string` | yes | - | The network's name. |
| `id` | `string` | no | - | The network's ID, which the network named must still have: an undo recorded against one network never acts on another that took its name since. |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task, when the engine is reached over SSH. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `name` | `string` | always | The network's name. |
| `id` | `string` | when the network exists | The network's ID. |
| `removed` | `bool` | when the network existed | Whether the network was removed (by a check, would be). |

## Undoing this

**Can be undone.** A run that changes something records the instruction that reverses it, as an `inverse` stat holding the method to call and the parameters to call it with, resolved from the state this run actually found. A run that changed nothing records no instruction, which is how it says that undoing it means doing nothing.

A run that removed the network records creating it again with the settings it had: driver, internal, IPv6, its first subnet with its gateway and range, MTU, parent and labels. Labels are not kept in the journal. The undo is partial when the network had an option or a second subnet no parameter sets; a network is a fresh one with a new ID once made again, and the containers that used it are not reconnected.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `container.network.create`
- `container.network.disconnect`

## Examples

Remove an old network:

```yaml
- name: Remove the old network
  container.network.remove:
    name: appnet-old
```


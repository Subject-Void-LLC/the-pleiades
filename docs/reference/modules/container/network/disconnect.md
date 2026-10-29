---
status: beta
---

# container.network.disconnect

Disconnects a container from a network.

Disconnects the container named container from the network named network. A container not on the network, or a network or container that does not exist, is no change. With network_id or container_id, an object that no longer has that ID is refused, so an undo never acts on another that took the name. Records connecting it again, by both IDs, with the aliases and static IPv4 address it had, as the undo. A check reads both and disconnects nothing. The engine is reached the first way onboarding proved for the device (this machine's socket, an SSH channel to the engine's socket, the engine's own dial-stdio over SSH, or mutual TLS), its identity checked against the one onboarding recorded, and every request outside the operations this method declares is refused before it reaches the engine.

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
| `network` | `string` | yes | - | The network's name. |
| `container` | `string` | yes | - | The container's name. |
| `network_id` | `string` | no | - | The network's ID, which the network named must still have: an undo recorded against one network never acts on another that took its name since. |
| `container_id` | `string` | no | - | The container's ID, which the container named must still have. |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task, when the engine is reached over SSH. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `network` | `string` | always | The network's name. |
| `container` | `string` | always | The container's name. |
| `network_id` | `string` | when both exist | The network's ID. |
| `container_id` | `string` | when both exist | The container's ID. |
| `connected` | `bool` | when the task succeeds | Whether the container is on the network after the task (by a check, would be). |

## Undoing this

**Can be undone.** A run that changes something records the instruction that reverses it, as an `inverse` stat holding the method to call and the parameters to call it with, resolved from the state this run actually found. A run that changed nothing records no instruction, which is how it says that undoing it means doing nothing.

A run that disconnected the container records connecting it again, by both IDs, with the aliases and static IPv4 address it had; one that found it not connected records nothing.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `container.network.connect`
- `container.network.remove`

## Examples

Take the web server off the old network:

```yaml
- name: Leave appnet-old
  container.network.disconnect:
    network: appnet-old
    container: web
```


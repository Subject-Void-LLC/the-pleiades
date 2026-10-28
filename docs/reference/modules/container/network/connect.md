---
status: beta
---

# container.network.connect

Connects a container to a network.

Connects the container named container to the network named network, with the declared aliases and static IPv4 address. A container already on the network with them is no change; one on it without them is refused, naming each difference, since changing a container's place on a network means disconnecting it. Both must exist. A container whose network mode is none, host or another container's has no network to join, and is refused, naming container.recreate. With network_id or container_id, an object that no longer has that ID is refused. Records disconnecting it, by both IDs, as the undo. A check reads both and connects nothing. The engine is reached the first way onboarding proved for the device (this machine's socket, an SSH channel to the engine's socket, the engine's own dial-stdio over SSH, or mutual TLS), its identity checked against the one onboarding recorded, and every request outside the operations this method declares is refused before it reaches the engine.

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
| `aliases` | `list` | no | - | Names the container answers to on this network, beside its own. |
| `ipv4_address` | `string` | no | - | A static IPv4 address for the container on this network, inside a subnet the network was created with. |
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

A run that connected the container records disconnecting it, by both IDs; one that found it connected as declared records nothing.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `container.network.disconnect`
- `container.network.create`
- `container.run`

## Examples

Put the web server on the application network:

```yaml
- name: Join appnet
  container.network.connect:
    network: appnet
    container: web
    aliases: [shop]
```


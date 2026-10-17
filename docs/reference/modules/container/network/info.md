---
status: beta
---

# container.network.info

Reads a network.

Reads the network named name as the engine reports it: its ID, driver, scope, internal, IPv6, subnets, labels, driver options and the running containers on it. An absent network is reported as exists false rather than refused, so a runbook can branch on it. Reading changes nothing, and a check reads the same way. The engine is reached the first way onboarding proved for the device (this machine's socket, an SSH channel to the engine's socket, the engine's own dial-stdio over SSH, or mutual TLS), its identity checked against the one onboarding recorded, and every request outside the operations this method declares is refused before it reaches the engine.

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
| `name` | `string` | yes | - | The network's name. |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task, when the engine is reached over SSH. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `name` | `string` | always | The network's name. |
| `exists` | `bool` | always | Whether the network exists. |
| `id` | `string` | when the network exists | The network's ID. |
| `driver` | `string` | when the network exists | The network's driver. |
| `scope` | `string` | when the network exists | The network's scope: local, or swarm. |
| `internal` | `bool` | when the network exists | Whether the network has no route outside the host. |
| `enable_ipv6` | `bool` | when the network exists | Whether the network has IPv6 addresses. |
| `subnets` | `list of dict` | when the network exists | Each subnet, with its gateway and IP range when it has them. |
| `labels` | `dict` | when the network exists | The network's labels. |
| `options` | `dict` | when the network exists | The network's driver options, as the engine reports them. |
| `containers` | `list of string` | when the network exists | The names of the running containers on the network. A stopped container that declares it is not listed. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

Reading is not changing: the method reads one network and changes nothing.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `container.network.create`
- `container.engine.info`

## Examples

Read a network a later task branches on:

```yaml
- name: Read appnet
  container.network.info:
    name: appnet
  register: appnet
```


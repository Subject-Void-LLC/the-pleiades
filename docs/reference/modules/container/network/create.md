---
status: beta
---

# container.network.create

Makes a network exist as declared.

Makes the network named name exist on the device's engine with the declared driver and settings. An absent network is created; one that matches is no change. One that differs (its driver, internal, enable_ipv6, or a declared subnet, gateway, IP range, MTU, parent or label) is refused, naming each difference, and never replaced, since replacing a network disconnects every container on it. A setting the task leaves out is the engine's choice and is not compared, so a network made with an engine-chosen subnet still matches. The engine's own networks (bridge, host, none, podman) are refused. Records removing the network, by ID, as the undo. A check reads the network and creates nothing. The engine is reached the first way onboarding proved for the device (this machine's socket, an SSH channel to the engine's socket, the engine's own dial-stdio over SSH, or mutual TLS), its identity checked against the one onboarding recorded, and every request outside the operations this method declares is refused before it reaches the engine.

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
| `driver` | `string` | no | `bridge` | The network driver: bridge (a private network on the host), or macvlan or ipvlan (containers on the parent interface's own network). **Choices:** `bridge`, `macvlan`, `ipvlan`. |
| `internal` | `bool` | no | `false` | Give the network no route outside the host: its containers reach each other and nothing else. |
| `enable_ipv6` | `bool` | no | `false` | Give the network IPv6 addresses as well as IPv4 ones. Required for an IPv6 subnet. |
| `subnet` | `string` | no | - | The network's subnet, an address with its prefix length (10.20.0.0/24). Left out, the engine chooses one from its pool. |
| `gateway` | `string` | no | - | The gateway address, inside subnet. Needs subnet. |
| `ip_range` | `string` | no | - | The part of subnet containers are given addresses from, an address with its prefix length. Needs subnet. |
| `mtu` | `int` | no | - | The bridge's MTU, from 68 to 65535. Bridge only: macvlan and ipvlan take their parent's. |
| `parent` | `string` | no | - | The host interface a macvlan or ipvlan network sits on (eth0, or eth0.10 for a VLAN). Left out, the engine makes a network with no way out. |
| `labels` | `dict` | no | - | Labels on the network. Labels it has beyond these are allowed. |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task, when the engine is reached over SSH. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `name` | `string` | always | The network's name. |
| `id` | `string` | when the network exists | The network's ID. |
| `driver` | `string` | always | The network's driver. |

## Undoing this

**Can be undone.** A run that changes something records the instruction that reverses it, as an `inverse` stat holding the method to call and the parameters to call it with, resolved from the state this run actually found. A run that changed nothing records no instruction, which is how it says that undoing it means doing nothing.

A run that created the network records its removal, by ID; one that found it as declared records nothing.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `container.network.remove`
- `container.network.connect`
- `container.network.info`

## Examples

A private network for an application:

```yaml
- name: App network
  container.network.create:
    name: appnet
    subnet: 10.20.0.0/24
    labels:
      app: shop
```

A network with no way out:

```yaml
- name: Back end
  container.network.create:
    name: backend
    internal: true
```


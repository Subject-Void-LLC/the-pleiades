---
status: beta
---

# virt.clab.vxlan.create

Creates a VxLAN tunnel between a host interface and a remote VTEP.

Creates a VxLAN interface on the server's host that carries the traffic of link to remote. Its undo is virt.clab.vxlan.delete with the prefix vx-, which removes every VxLAN interface containerlab made with that prefix, so it is partial when others exist. The target is the containerlab API server, a generic_http device that onboarding recognised (it declares ContainerlabAPICapable); its login is the device's stored credential, with http_auth basic, and neither the password nor the token it gets is logged, returned or journaled.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `ContainerlabAPICapable` |
| Transports | `https` |
| Requires elevation | no |
| Runs | in the host process (the CLI or a Runner); acts on its target device |
| Check mode | Not supported: a check run names this task as unchecked, since whether the tunnel comes up is known only once it is made |
| Engine version | `>=0.3.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `remote` | `string` | yes | - | The remote VTEP's IP address. |
| `link` | `string` | yes | - | An existing interface on the host to bridge to the tunnel. |
| `vni` | `int` | no | - | The VxLAN network identifier; 10 when unset. |
| `port` | `int` | no | - | The UDP port; 14789 when unset. |
| `dev` | `string` | no | - | The device to source the tunnel from; found when unset. |
| `mtu` | `int` | no | - | The interface's MTU; worked out when unset. |

## Undoing this

**Can be undone.** A run that changes something records the instruction that reverses it, as an `inverse` stat holding the method to call and the parameters to call it with, resolved from the state this run actually found. A run that changed nothing records no instruction, which is how it says that undoing it means doing nothing.

Its undo removes the VxLAN interfaces named vx-*, which is partial when others carry that prefix.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `virt.clab.vxlan.delete`
- `virt.clab.veth.create`

## Examples

Stretch a lab to another host:

```yaml
- name: VxLAN to host B
  virt.clab.vxlan.create:
    target: clab-api
    remote: 10.0.0.2
    link: veth-r1
    vni: 100
```


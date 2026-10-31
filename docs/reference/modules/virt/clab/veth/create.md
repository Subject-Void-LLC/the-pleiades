---
status: beta
---

# virt.clab.veth.create

Creates a vEth pair between two endpoints.

Creates a virtual Ethernet pair whose ends are in the two endpoints, each written node:interface (for a node in a lab) or kind:name (a host or bridge interface). The server has no call that removes one, so it cannot be undone. The target is the containerlab API server, a generic_http device that onboarding recognised (it declares ContainerlabAPICapable); its login is the device's stored credential, with http_auth basic, and neither the password nor the token it gets is logged, returned or journaled.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `ContainerlabAPICapable` |
| Transports | `https` |
| Requires elevation | no |
| Runs | in the host process (the CLI or a Runner); acts on its target device |
| Check mode | Not supported: a check run names this task as unchecked, since whether the endpoints can be joined is known only once the pair is made |
| Engine version | `>=0.3.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `a_endpoint` | `string` | yes | - | End A, as node:interface or kind:name. |
| `b_endpoint` | `string` | yes | - | End B, as node:interface or kind:name. |
| `mtu` | `int` | no | - | The pair's MTU; 9500 when unset. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

The server has no call that removes a vEth pair.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `virt.clab.vxlan.create`

## Examples

Wire a node to the host:

```yaml
- name: r1 to the bridge
  virt.clab.veth.create:
    target: clab-api
    a_endpoint: clab-nclab-r1:eth9
    b_endpoint: host:veth-r1
```


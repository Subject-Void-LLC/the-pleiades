---
status: beta
---

# virt.clab.node.ports

Lists the browser-reachable ports a lab node publishes.

Returns the ports the server reports for the node. A lab that is not deployed, or a node it does not have, is not a failure: exists is false and ports is empty, so a later task can decide what to do. It only reads, so a check is the same call. The target is the containerlab API server, a generic_http device that onboarding recognised (it declares ContainerlabAPICapable); its login is the device's stored credential, with http_auth basic, and neither the password nor the token it gets is logged, returned or journaled.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `ContainerlabAPICapable` |
| Transports | `https` |
| Requires elevation | no |
| Runs | in the host process (the CLI or a Runner); acts on its target device |
| Check mode | Supported: reports what it would change and changes nothing |
| Absence | A thing that is not there is exists false, not a failure, in a run and a check |
| Engine version | `>=0.3.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `lab` | `string` | yes | - | The lab's name, as containerlab deployed it. |
| `node` | `string` | yes | - | The node's name in the lab (n1), or its container's name (clab-<lab>-n1). |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `exists` | `bool` | always | Whether the lab is deployed and has the node. |
| `ports` | `list` | always | The node's published browser ports; empty when it does not exist. |
| `name` | `string` | when it exists | The node's container name. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

It only reads the server, so there is nothing to undo.

`pleiades rollback` and the Controller's rollback route replay a recorded instruction. It is recorded during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `virt.clab.lab.info`

## Examples

Lists the browser-reachable ports a lab node publishes.:

```yaml
- name: r1's ports
  virt.clab.node.ports:
    target: clab-api
    lab: nclab
    node: r1
```


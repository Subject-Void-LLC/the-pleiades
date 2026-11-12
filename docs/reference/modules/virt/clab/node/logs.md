---
status: beta
---

# virt.clab.node.logs

Reads the last lines of a lab node's output.

Returns the last tail lines the node's container wrote. The text is the node's own, and may hold anything the node printed. A lab that is not deployed, or a node it does not have, is not a failure: exists is false and no output is reported, so a later task can decide what to do. It only reads, so a check is the same call. The target is the containerlab API server, a generic_http device that onboarding recognised (it declares ContainerlabAPICapable); its login is the device's stored credential, with http_auth basic, and neither the password nor the token it gets is logged, returned or journaled.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `ContainerlabAPICapable` |
| Transports | `https` |
| Requires elevation | no |
| Runs | in the host process (the CLI or a Runner); acts on its target device |
| Check mode | Supported: reports what it would change and changes nothing |
| Engine version | `>=0.3.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `lab` | `string` | yes | - | The lab's name, as containerlab deployed it. |
| `node` | `string` | yes | - | The node's name in the lab (n1), or its container's name (clab-<lab>-n1). |
| `tail` | `int` | no | `100` | How many of the last lines. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `exists` | `bool` | always | Whether the lab is deployed and has the node. |
| `logs` | `string` | when it exists | The node's output. |
| `name` | `string` | when it exists | The node's container name. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

It only reads the server, so there is nothing to undo.

`pleiades rollback` and the Controller's rollback route replay a recorded instruction. It is recorded during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `virt.clab.node.exec`

## Examples

Reads the last lines of a lab node's output.:

```yaml
- name: Why did r1 stop
  virt.clab.node.logs:
    target: clab-api
    lab: nclab
    node: r1
    tail: 50
```


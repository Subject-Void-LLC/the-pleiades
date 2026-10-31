---
status: beta
---

# virt.clab.lab.info

Inspects one lab: whether it exists and its nodes.

A lab that is not deployed is not a failure: exists is false and no nodes are reported, so a later task can decide what to do. It only reads, so a check is the same call. The target is the containerlab API server, a generic_http device that onboarding recognised (it declares ContainerlabAPICapable); its login is the device's stored credential, with http_auth basic, and neither the password nor the token it gets is logged, returned or journaled.

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

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `exists` | `bool` | always | Whether the lab is deployed. |
| `nodes` | `list of dict` | when it exists | Each node: name (container), node, kind, image, state (running, exited or paused), status, ipv4_address, ipv6_address, container_id, and ports (each host_ip, host_port, port and protocol it publishes). |
| `node_count` | `int` | when it exists | How many nodes. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

It only reads the server, so there is nothing to undo.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `virt.clab.lab.list`
- `virt.clab.lab.deploy`

## Examples

Inspects one lab: whether it exists and its nodes.:

```yaml
- name: Is the lab up
  virt.clab.lab.info:
    target: clab-api
    lab: nclab
  register: lab
```


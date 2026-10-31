---
status: beta
---

# virt.clab.netem.show

Shows the netem impairments on a lab node's interfaces.

Returns each interface that has a delay, jitter, loss or rate set. It only reads, so a check is the same call. The target is the containerlab API server, a generic_http device that onboarding recognised (it declares ContainerlabAPICapable); its login is the device's stored credential, with http_auth basic, and neither the password nor the token it gets is logged, returned or journaled.

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

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `interfaces` | `list of dict` | always | Each impaired interface: interface, delay, jitter, packet_loss and rate. |
| `name` | `string` | always | The node's container name. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

It only reads the server, so there is nothing to undo.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `virt.clab.netem.set`
- `virt.clab.netem.reset`

## Examples

Shows the netem impairments on a lab node's interfaces.:

```yaml
- name: What is impaired
  virt.clab.netem.show:
    target: clab-api
    lab: nclab
    node: r1
```


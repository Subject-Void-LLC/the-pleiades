---
status: beta
---

# virt.clab.lab.save

Saves each node's running configuration to its startup configuration.

Runs containerlab's save for the lab, so a node that reads a startup configuration comes back with what it runs now. It overwrites those saved configurations, which this method does not read first, so it cannot be undone. The target is the containerlab API server, a generic_http device that onboarding recognised (it declares ContainerlabAPICapable); its login is the device's stored credential, with http_auth basic, and neither the password nor the token it gets is logged, returned or journaled.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `ContainerlabAPICapable` |
| Transports | `https` |
| Requires elevation | no |
| Runs | in the host process (the CLI or a Runner); acts on its target device |
| Check mode | Not supported: a check run names this task as unchecked, since saving overwrites configurations the server does not show beforehand |
| Engine version | `>=0.3.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `lab` | `string` | yes | - | The lab's name, as containerlab deployed it. |
| `node_filter` | `string` | no | - | Only this node, by its name in the lab or its container's name. Every node when empty. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `message` | `string` | always | The server's message. |
| `output` | `string` | always | containerlab's own output. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

It overwrites each node's saved configuration, which this method does not read first.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `virt.clab.lab.deploy`

## Examples

Keep what the lab runs:

```yaml
- name: Save the configurations
  virt.clab.lab.save:
    target: clab-api
    lab: nclab
```


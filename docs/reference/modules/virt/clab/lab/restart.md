---
status: beta
---

# virt.clab.lab.restart

Makes a lab's nodes restarted.

Restarts every node of the lab through the server. It always changes the lab. A lab-wide restart cannot say which nodes were in which state before, so it records no undo; virt.clab.node.restart records an exact one for one node. The target is the containerlab API server, a generic_http device that onboarding recognised (it declares ContainerlabAPICapable); its login is the device's stored credential, with http_auth basic, and neither the password nor the token it gets is logged, returned or journaled.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `ContainerlabAPICapable` |
| Transports | `https` |
| Requires elevation | no |
| Runs | in the host process (the CLI or a Runner); acts on its target device |
| Check mode | Not supported: a check run names this task as unchecked, since a restart always changes the lab, and how the nodes come back is known only once it has run |
| Engine version | `>=0.3.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `lab` | `string` | yes | - | The lab's name, as containerlab deployed it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `nodes` | `list of dict` | always | The nodes after, as lab.info reports them. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

A restart cannot be put back.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `virt.clab.node.restart`
- `virt.clab.lab.info`

## Examples

Lab restart:

```yaml
- name: Lab restart
  virt.clab.lab.restart:
    target: clab-api
    lab: nclab
```


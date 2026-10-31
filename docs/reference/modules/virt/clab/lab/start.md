---
status: beta
---

# virt.clab.lab.start

Makes a lab's nodes started.

Starts every stopped node of the lab through the server, and leaves the running ones. A lab whose nodes all run already is reported unchanged. A lab-wide start cannot say which nodes were in which state before, so it records no undo; virt.clab.node.start records an exact one for one node. The target is the containerlab API server, a generic_http device that onboarding recognised (it declares ContainerlabAPICapable); its login is the device's stored credential, with http_auth basic, and neither the password nor the token it gets is logged, returned or journaled.

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
| `nodes` | `list of dict` | always | The nodes after, as lab.info reports them. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

A lab-wide start cannot say which nodes were in which state before; virt.clab.node.start records an exact undo for one node.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `virt.clab.node.start`
- `virt.clab.lab.info`

## Examples

Lab start:

```yaml
- name: Lab start
  virt.clab.lab.start:
    target: clab-api
    lab: nclab
```


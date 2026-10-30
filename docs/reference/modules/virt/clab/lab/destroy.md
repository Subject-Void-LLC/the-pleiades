---
status: beta
---

# virt.clab.lab.destroy

Destroys a lab, unless it is not deployed.

Removes the lab's containers and network through the server. A lab that is not deployed is not a failure and is reported unchanged. It cannot be undone: the containers' state is gone, and an undo that deployed the topology again would be a new lab, not this one. The target is the containerlab API server, a generic_http device that onboarding recognised (it declares ContainerlabAPICapable); its login is the device's stored credential, with http_auth basic, and neither the password nor the token it gets is logged, returned or journaled.

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
| `cleanup` | `bool` | no | `false` | Also remove the lab's directory and configuration state. |
| `purge_lab_dir` | `bool` | no | `false` | Remove the lab's directory on the server. |
| `graceful` | `bool` | no | `false` | Stop the nodes gracefully before removing them. |
| `keep_mgmt_net` | `bool` | no | `false` | Keep the management network. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `exists` | `bool` | always | Whether the lab is deployed now (false after a destroy). |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

A destroyed lab's containers and their state are gone; deploying its topology again makes a new lab, not this one.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `virt.clab.lab.deploy`

## Examples

Tear a lab down:

```yaml
- name: The lab is gone
  virt.clab.lab.destroy:
    target: clab-api
    lab: nclab
    cleanup: true
```


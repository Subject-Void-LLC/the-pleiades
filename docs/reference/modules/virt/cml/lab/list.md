---
status: beta
---

# virt.cml.lab.list

Lists the labs on a CML controller.

Reads every lab the controller's account can see, with its title, state and node count, and changes nothing. The task's target is a cml_controller device (its api_base, TLS settings and its own credential, stored with add-credential --password-stdin); the method runs on this machine and talks to the controller's REST API. A check is the same read.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `CMLAPICapable` |
| Transports | `https` |
| Requires elevation | no |
| Runs | in the host process (the CLI or a Runner); acts on its target device |
| Check mode | Supported: reports what it would change and changes nothing |
| Engine version | `>=0.2.0` |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `labs` | `list` | always | Each lab as id, title, state (DEFINED_ON_CORE, STARTED or STOPPED) and nodes (its node count), by title. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

It only reads the controller.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `virt.cml.lab.info`

## Examples

List the labs:

```yaml
- name: List the controller's labs
  virt.cml.lab.list: {}
  register: labs
```


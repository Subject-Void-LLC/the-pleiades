---
status: beta
---

# net.catalyst.tag_facts

Gathers the tags defined on a Cisco Catalyst Center, as facts.

Reads every tag the targeted Catalyst Center knows about, and separates the controller's own system tags (its internal bookkeeping) from operator-created ones, so a runbook grouping devices by operator intent does not have to filter system tags out itself.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `CatalystAPICapable` |
| Transports | `https` |
| Requires elevation | no |
| Engine version | `>=1.0.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `insecure_skip_verify` | `bool` | no | `false` | Skip TLS certificate verification for this call. For a lab or sandbox controller with a self-signed certificate only; never set true against a production controller. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `tags` | `list of map` | always | One entry per tag: id, name, system_tag. |
| `operator_tags` | `list of string` | always | Names of every tag not created by the controller itself. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

A read-only fact gatherer changes nothing on the device, so there is nothing to undo.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## Examples

Gather operator-created tags:

```yaml
- name: Gather Catalyst Center tag facts
  net.catalyst.tag_facts:
  register: tags
```


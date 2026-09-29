---
status: beta
---

# net.catalyst.site_facts

Gathers a Cisco Catalyst Center's site hierarchy, as facts.

Reads every site the targeted Catalyst Center manages. Emits the full site_name_hierarchy for each site, not just its bare name, since two sites in different regions can share a bare name.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `CatalystAPICapable` |
| Transports | `https` |
| Requires elevation | no |
| Runs | in the host process (the CLI or a Runner); acts on its target device |
| Check mode | Supported: reports what it would change and changes nothing |
| Engine version | `>=0.2.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `insecure_skip_verify` | `bool` | no | `false` | Skip TLS certificate verification for this call. For a lab or sandbox controller with a self-signed certificate only; never set true against a production controller. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `sites` | `list of map` | always | One entry per site: id, name, site_name_hierarchy. |
| `site_count` | `int` | always | The number of sites in the sites fact. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

A read-only fact gatherer changes nothing on the device, so there is nothing to undo.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `net.catalyst.device_facts`

## Examples

Gather the site hierarchy:

```yaml
- name: Gather Catalyst Center site facts
  net.catalyst.site_facts:
  register: sites
```


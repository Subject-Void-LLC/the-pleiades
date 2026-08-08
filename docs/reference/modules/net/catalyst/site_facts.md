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
| Engine version | `>=1.0.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `insecure_skip_verify` | `bool` | no | `false` | Skip TLS certificate verification for this call. For a lab or sandbox controller with a self-signed certificate only; never set true against a production controller. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `sites` | `list of map` | always | One entry per site: id, name, site_name_hierarchy. |
| `site_count` | `int` | always | The number of sites in the sites fact. |

## See also

- `net.catalyst.device_facts`

## Examples

Gather the site hierarchy:

```yaml
- name: Gather Catalyst Center site facts
  fqcn: net.catalyst.site_facts
  register: sites
```


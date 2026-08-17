---
status: beta
---

# net.catalyst.device_facts

Gathers every device a Cisco Catalyst Center manages, as facts.

Pages through the Catalyst Center's device inventory and emits one fact entry per device: identity, platform, software, role, and reachability/collection status. Never reports changed: reading an inventory does not alter it.

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
| `page_size` | `int` | no | `500` | How many devices to request per page from the Catalyst Center API. A tuning knob, not a correctness one: an invalid or non-positive value silently falls back to the default. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `devices` | `list of map` | always | One entry per device: id, hostname, management_ip, family, series, platform_id, software_type, software_version, role, serial_number, reachability_status, collection_status. |
| `device_count` | `int` | always | The number of devices in the devices fact. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

A read-only fact gatherer changes nothing on the device, so there is nothing to undo.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `net.catalyst.reachability`
- `net.catalyst.site_facts`

## Examples

Gather the managed fleet:

```yaml
- name: Gather Catalyst Center device facts
  fqcn: net.catalyst.device_facts
  register: fleet
```


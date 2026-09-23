---
status: beta
---

# net.catalyst.reachability

Reports which devices a Cisco Catalyst Center can currently reach and manage.

Answers a different question than device_facts: device_facts describes what the fleet is, gathered once; reachability describes what the fleet is doing right now, the check a gate task waits on before acting. Reports reachability_status (can the controller talk to the device at all) and collection_status (is it successfully collecting from it) separately, since a device can be reachable and still not managed.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `CatalystAPICapable` |
| Transports | `https` |
| Requires elevation | no |
| Check mode | Supported: reports what it would change and changes nothing |
| Engine version | `>=0.2.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `insecure_skip_verify` | `bool` | no | `false` | Skip TLS certificate verification for this call. For a lab or sandbox controller with a self-signed certificate only; never set true against a production controller. |
| `page_size` | `int` | no | `500` | How many devices to request per page from the Catalyst Center API. A tuning knob, not a correctness one: an invalid or non-positive value silently falls back to the default. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `devices` | `list of map` | always | One entry per device: hostname, management_ip, reachability_status, collection_status, reachable. |
| `unreachable` | `list of string` | always | Hostnames (or management IPs, if hostname is unset) of every unreachable device. |
| `reachable_count` | `int` | always | Count of devices with reachability_status Reachable. |
| `managed_count` | `int` | always | Count of devices with collection_status Managed. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

A read-only reachability check changes nothing on the device, so there is nothing to undo.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `net.catalyst.device_facts`

## Examples

Gate a change on full fleet reachability:

```yaml
- name: Confirm the fleet is reachable before changing anything
  net.catalyst.reachability:
  register: health

- name: Apply the change
  net.ios.config:
  when_cel: "stat.health[''].unreachable.size() == 0"
```


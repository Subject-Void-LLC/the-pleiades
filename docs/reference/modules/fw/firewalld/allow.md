---
status: beta
---

# fw.firewalld.allow

Opens a port or service in firewalld.

Makes sure a port or service is allowed through firewalld in the given zone. This is close to community.general.firewalld with state=enabled, except permanent and immediate are decided independently rather than as one of firewalld's own permanent/runtime toggle: either can be true without the other. Both configurations are read before anything is sent, so a half already allowing the rule is left alone and reports no change from that half.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `FirewalldCapable` |
| Transports | `ssh` |
| Requires elevation | yes |
| Check mode | Supported: reports what it would change and changes nothing |
| Engine version | `>=0.2.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `port` | `int` | no | - | The port to allow. Exactly one of port or service is required. |
| `protocol` | `string` | no | `tcp` | The protocol for port (tcp or udp). Ignored when service is given. |
| `service` | `string` | no | - | The firewalld service name to allow, e.g. http. Exactly one of port or service is required. |
| `zone` | `string` | no | `public` | The firewalld zone to allow the rule in. |
| `permanent` | `bool` | no | `true` | Also add the rule to the permanent configuration, so it survives a reload. |
| `immediate` | `bool` | no | `true` | Also add the rule to the runtime configuration, so it takes effect now. |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `zone` | `string` | always | The zone this task acted on. |
| `diff` | `dict` | always | Whether the rule was allowed in the permanent configuration and in the runtime one, before this task and after it. Recorded even on a run that changed nothing. |

## Undoing this

**Can be undone.** A run that changes something records the instruction that reverses it, as an `inverse` stat holding the method to call and the parameters to call it with, resolved from the state this run actually found. A run that changed nothing records no instruction, which is how it says that undoing it means doing nothing.

A run that allowed the permanent configuration, the runtime one, or both emits an fw.firewalld.deny naming the same port or service and zone, with permanent and immediate set to match exactly which half this run actually changed. A run that found everything already allowed emits nothing.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `fw.firewalld.deny`
- `fw.firewalld.reload`

## Examples

Open a port:

```yaml
- name: Allow HTTPS
  fw.firewalld.allow:
    port: 443
```

Allow a service immediately without persisting it:

```yaml
- name: Temporarily allow SSH from a maintenance zone
  fw.firewalld.allow:
    service: ssh
    zone: maintenance
    permanent: false
```


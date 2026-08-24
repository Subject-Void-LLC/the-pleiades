---
status: beta
---

# fw.firewalld.reload

Reloads firewalld to apply pending rule changes.

Runs firewall-cmd --reload, which applies the permanent configuration to the runtime one and is what makes an fw.firewalld.allow or fw.firewalld.deny run with permanent: true, immediate: false visible without a full firewalld restart. This is ansible.posix.firewalld's own no-op-detecting reload, except firewall-cmd exposes no way to ask whether a reload would have made any difference, so this always reports changed the same way svc.systemd.daemon_reload does for the identical reason.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `FirewalldCapable` |
| Transports | `ssh` |
| Requires elevation | yes |
| Engine version | `>=1.0.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

A reload applies the permanent configuration to the runtime one, and there is no instruction that un-applies it. Undoing whatever permanent rules were added or removed is the task that changed them, not this one.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `fw.firewalld.allow`
- `fw.firewalld.deny`

## Examples

Persist a rule and apply it now:

```yaml
- name: Allow HTTPS permanently
  fw.firewalld.allow:
    port: 443
    immediate: false

- name: Apply it
  fw.firewalld.reload: {}
```


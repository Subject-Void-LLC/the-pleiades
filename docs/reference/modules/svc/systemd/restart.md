---
status: beta
---

# svc.systemd.restart

Restarts a systemd unit, starting it if it was not running.

Restarts a systemd unit. This is ansible.builtin.systemd with state=restarted, and like that module it starts a unit that was not running rather than failing. It is the one method in this namespace that is never converged: restarting a running unit is the point, not a no-op, so this always sends the command and always reports changed. That makes it the method most worth putting behind a when condition or a handler, so a service is only bounced when something it reads actually changed. A unit systemd does not know is refused, and a masked unit is refused with the mask named.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `SystemdCapable` |
| Transports | `ssh` |
| Requires elevation | yes |
| Engine version | `>=1.0.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `name` | `string` | yes | - | The systemd unit to act on, such as nginx or nginx.service. This is ansible.builtin.systemd's own parameter name. The unit must already exist: a name systemd does not know is refused rather than reported as already stopped, since that is nearly always a typo. |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `name` | `string` | always | The unit this task acted on. |
| `diff` | `dict` | always | What systemd reported about the unit before this task and after it, each holding exists, active, enabled and systemd's own load_state, active_state and unit_file_state. Recorded even on a run that changed nothing, because "it was already like this" is what tells a later rollback to do nothing. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

A restart's effect is the interruption itself, and there is no instruction that un-interrupts a service. Restarting it a second time would run this method again rather than reverse it. The unit's state is deliberately still recorded under diff, so a rollback reaching this task can see that the unit was running before and after and decide for itself, but this method emits no instruction because none would be true.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `svc.systemd.start`
- `svc.systemd.stop`
- `svc.restart`

## Examples

Restart after a config change:

```yaml
- name: Write the nginx config
  fqcn: file.copy
  params:
    src: ./nginx.conf
    dest: /etc/nginx/nginx.conf
  register: nginx_config

- name: Restart nginx only if the config actually changed
  fqcn: svc.systemd.restart
  params:
    name: nginx
  when:
    - nginx_config.changed
```


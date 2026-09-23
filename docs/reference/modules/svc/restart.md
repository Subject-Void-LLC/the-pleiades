---
status: beta
---

# svc.restart

Restarts a service, whichever service manager the device runs.

Restarts a service, starting it if it was not running. This is ansible.builtin.service with state=restarted: it resolves the device's service manager and hands the call to that manager's concrete method. It is the one method here that is never converged, since restarting a running service is the point rather than a no-op, so it always reports changed. That makes it the one most worth putting behind a when condition, so a service is only bounced when something it reads actually changed.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `ServiceManagerCapable` |
| Transports | - |
| Requires elevation | yes |
| Check mode | Supported: reports what it would change and changes nothing |
| Engine version | `>=0.2.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `name` | `string` | yes | - | The service to act on. This is passed straight through to the concrete method for the device's service manager, so it means whatever that manager means by a service name: a systemd unit such as nginx or nginx.service on a Linux host. |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `name` | `string` | always | The service this task acted on, as recorded by the concrete method that ran. |
| `diff` | `dict` | always | What the service manager reported before this task and after it. The exact keys come from the concrete method that ran, since what there is to say about a service differs between service managers. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

A restart's effect is the interruption itself, and no instruction un-interrupts a service. Restarting a second time repeats this method rather than reversing it. The concrete method still records the service's state under diff, so a rollback reaching this task can see what was running and decide for itself.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `svc.start`
- `svc.stop`
- `svc.systemd.restart`

## Examples

Restart only when the config changed:

```yaml
- name: Write the config
  file.copy:
    src: ./app.conf
    dest: /etc/app/app.conf
  register: app_config

- name: Restart the service if the config changed
  svc.restart:
    name: app
  when:
    - app_config.changed
```


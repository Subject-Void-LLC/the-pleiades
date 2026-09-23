---
status: beta
---

# svc.windows.restart

Restarts a Windows service, starting it if it was not running.

Restarts a Windows service. This is ansible.windows.win_service with state=restarted, and like that module it starts a service that was not running rather than failing. It is the one method in this namespace that is never converged: restarting a running service is the point, not a no-op, so this always sends the command and always reports changed. That makes it the method most worth putting behind a when condition or a handler, so a service is only bounced when something it reads actually changed. A service the Service Control Manager does not know is refused, and a service whose start type is Disabled is refused with that named.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `WindowsServiceCapable` |
| Transports | `winrm` |
| Requires elevation | yes |
| Check mode | Supported: reports what it would change and changes nothing |
| Engine version | `>=0.2.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `name` | `string` | yes | - | The Windows service to act on, its short service name (not its display name), such as Spooler rather than "Print Spooler". The service must already exist: a name the Service Control Manager does not know is refused rather than reported as already stopped, since that is nearly always a typo. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `name` | `string` | always | The service this task acted on. |
| `diff` | `dict` | always | What the Service Control Manager reported about the service before this task and after it, each holding exists, running, status and start_type. Recorded even on a run that changed nothing, because "it was already like this" is what tells a later rollback to do nothing. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

A restart's effect is the interruption itself, and there is no instruction that un-interrupts a service. Restarting it a second time would run this method again rather than reverse it. The service's state is still recorded under diff, so a rollback reaching this task can see the service was running before and after and decide for itself, but this method emits no instruction because none would be true.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `svc.windows.start`
- `svc.windows.stop`
- `svc.restart`

## Examples

Restart after a config change:

```yaml
- name: Write the app's config
  file.copy:
    src: ./app.config
    dest: C:\Program Files\App\app.config
  register: app_config

- name: Restart the app service only if the config actually changed
  svc.windows.restart:
    name: AppService
  when:
    - app_config.changed
```


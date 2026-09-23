---
status: beta
---

# svc.windows.stop

Stops a Windows service now, without changing its start type.

Makes sure a Windows service is not running right now. This is ansible.windows.win_service with state=stopped, and it leaves the start type alone: a service stopped by this task still starts at the next reboot unless svc.windows.disable is also run. State is read before anything is sent, so a service that is already stopped reports no change. A service the Service Control Manager does not know is refused rather than reported as stopped, since a typo in the name should not read as success.

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

**Can be undone.** A run that changes something records the instruction that reverses it, as an `inverse` stat holding the method to call and the parameters to call it with, resolved from the state this run actually found. A run that changed nothing records no instruction, which is how it says that undoing it means doing nothing.

A run that stopped a running service emits an svc.windows.start naming it. A run that found it already stopped emits nothing. Starting it again returns the service to running, but nothing can restore what it missed while it was down: requests that were refused, queues that backed up, timers that did not fire.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `svc.windows.start`
- `svc.windows.disable`
- `svc.stop`

## Examples

Stop a service:

```yaml
- name: Stop the print spooler before changing its config
  svc.windows.stop:
    name: Spooler
```

Stop it now and keep it from coming back at boot:

```yaml
- name: Stop the print spooler
  svc.windows.stop:
    name: Spooler

- name: Keep the print spooler from starting at boot
  svc.windows.disable:
    name: Spooler
```


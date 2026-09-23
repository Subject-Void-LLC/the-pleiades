---
status: beta
---

# svc.windows.start

Starts a Windows service now, without changing its start type.

Makes sure a Windows service is running right now. This is ansible.windows.win_service with state=started, and it is deliberately not also enable: starting and setting the start type are separate in the Service Control Manager and separate here, so a task that wants both says both. State is read before anything is sent, so a service that is already running reports no change and no command reaches the device. A service the Service Control Manager does not know is refused rather than reported as started, and a service whose start type is Disabled is refused with that named, since Windows itself refuses to start one and its own error describes a generic failure rather than the cause.

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

A run that started a stopped service emits an svc.windows.stop naming it. A run that found it already running emits nothing. What the inverse cannot restore is anything the service did while it was up: connections it accepted, files it wrote, messages it consumed. Stopping it again returns the service to where it was, not the system.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `svc.windows.stop`
- `svc.windows.restart`
- `svc.windows.enable`
- `svc.start`

## Examples

Start a service:

```yaml
- name: Make sure the print spooler is running
  svc.windows.start:
    name: Spooler
```

Start it and make it survive a reboot:

```yaml
- name: Start the print spooler
  svc.windows.start:
    name: Spooler

- name: Make the print spooler start at boot too
  svc.windows.enable:
    name: Spooler
```


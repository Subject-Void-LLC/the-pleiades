---
status: beta
---

# svc.windows.enable

Makes a Windows service start at boot, without starting it now.

Makes sure a Windows service's start type is Automatic. This is ansible.windows.win_service with start_mode=auto, and it is deliberately not also start: a service enabled by this task is not running until svc.windows.start runs or the device reboots. State is read before anything is sent, so a service whose start type is already Automatic reports no change. Windows recognizes a third start type, Manual, that neither this method nor svc.windows.disable targets: a service found Manual becomes Automatic, and running svc.windows.disable afterward would leave it Disabled rather than back at Manual, which is why that specific transition records no inverse instruction rather than a wrong one.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `WindowsServiceCapable` |
| Transports | `winrm` |
| Requires elevation | yes |
| Engine version | `>=1.0.0` |

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

A run that enabled a service whose start type was Disabled emits an svc.windows.disable naming it. A run that found the start type already Automatic emits nothing. A run that changed a start type of Manual to Automatic emits no inverse at all, deliberately: this namespace's disable only sets Disabled, which is a stronger claim than restoring Manual, and emitting an instruction that would over-correct is worse than emitting none. The undo restores the boot-time setting only; it never stops a service that is running, because enabling never started one.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `svc.windows.disable`
- `svc.windows.start`
- `svc.enable`

## Examples

Make a service start at boot:

```yaml
- name: Make sure the print spooler comes back after a reboot
  fqcn: svc.windows.enable
  params:
    name: Spooler
```


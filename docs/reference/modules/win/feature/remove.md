---
status: beta
---

# win.feature.remove

Disables a Windows optional feature or role via DISM.

Makes sure a Windows optional feature or role is disabled. This is ansible.windows.win_optional_feature with state=absent, built on dism.exe /online /disable-feature. Unlike install, this does not pass /all: removing a feature should not silently remove the parent features it depended on. State is read before anything is sent, so a feature that is already disabled reports no change. A feature name DISM does not recognize is refused rather than reported as already disabled, since that is nearly always a typo. Many features need a restart before removal fully takes effect; check reboot_required rather than assuming changed alone means the feature is gone. A check reads the feature and sends nothing; when the feature would change, its diff leaves out the state the feature would end in and reboot_required, since DISM decides between the finished and pending states only when it runs.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `WindowsFeatureCapable` |
| Transports | `winrm` |
| Requires elevation | yes |
| Check mode | Supported: reports what it would change and changes nothing |
| Engine version | `>=1.0.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `name` | `string` | yes | - | The Windows optional feature or role's DISM feature name, such as IIS-WebServerRole, not its display name. The feature must be one DISM recognizes: a name it does not is refused rather than reported as already the target state, since that is nearly always a typo. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `name` | `string` | always | The feature this task acted on. |
| `reboot_required` | `bool` | always | Whether DISM reported that a restart is needed for this change to take full effect (its own ERROR_SUCCESS_REBOOT_REQUIRED). False on a run that changed nothing. |
| `diff` | `dict` | always | What DISM reported about the feature before this task and after it, each holding exists and state. Recorded even on a run that changed nothing, because "it was already like this" is what tells a later rollback to do nothing. |

## Undoing this

**Can be undone.** A run that changes something records the instruction that reverses it, as an `inverse` stat holding the method to call and the parameters to call it with, resolved from the state this run actually found. A run that changed nothing records no instruction, which is how it says that undoing it means doing nothing.

A run that disabled an enabled feature emits a win.feature.install naming it. A run that found it already disabled emits nothing. Re-enabling it does not pass /all a second time from this inverse, so a parent feature the original install pulled in stays exactly as removing this one left it.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `win.feature.install`

## Examples

Disable IIS:

```yaml
- name: Make sure the web server role is disabled
  win.feature.remove:
    name: IIS-WebServerRole
```


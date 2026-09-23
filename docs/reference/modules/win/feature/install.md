---
status: beta
---

# win.feature.install

Enables a Windows optional feature or role via DISM, including its required parent features.

Makes sure a Windows optional feature or role is enabled. This is ansible.windows.win_optional_feature with state=present (or win_feature's default), built on dism.exe /online /enable-feature rather than the ServerManager PowerShell module, since dism.exe works on every Windows SKU and this platform's own DISMLogPath capability already commits to it. /all is passed, so enabling a feature also enables the parent features it requires, matching what the Windows GUI's own "Add roles and features" does by default. State is read before anything is sent, so a feature that is already enabled reports no change and no command reaches the device. A feature name DISM does not recognize is refused rather than reported as already enabled, since that is nearly always a typo. Many features need a restart before they finish taking effect; check reboot_required rather than assuming changed alone means the feature is fully usable. A check reads the feature and sends nothing; when the feature would change, its diff leaves out the state the feature would end in and reboot_required, since DISM decides between the finished and pending states only when it runs.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `WindowsFeatureCapable` |
| Transports | `winrm` |
| Requires elevation | yes |
| Check mode | Supported: reports what it would change and changes nothing |
| Engine version | `>=0.2.0` |

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

A run that enabled a disabled feature emits a win.feature.remove naming it. A run that found it already enabled emits nothing. What the inverse cannot undo is anything the feature's own presence changed on the system while it was enabled, and either direction may need the restart reboot_required reports before it is complete.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `win.feature.remove`

## Examples

Enable IIS:

```yaml
- name: Make sure the web server role is enabled
  win.feature.install:
    name: IIS-WebServerRole
  register: iis

- name: Reboot if DISM asked for one
  exec.winrm.shell:
    shell: powershell
    command: Restart-Computer -Force
    expect_disconnect: true
  when:
    - iis.reboot_required
```


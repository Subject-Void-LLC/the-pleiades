---
status: beta
---

# win.capability.remove

Removes a Windows capability (a feature on demand) with DISM, run as SYSTEM.

Makes sure a Windows capability, what Get-WindowsCapability lists, is not installed. Its state is read first with dism.exe /get-capabilityinfo, so a capability that is not present, only staged, or removed and waiting only for a restart reports no change and nothing is sent; a name DISM does not recognize is refused. The removal runs dism.exe as SYSTEM in a one-shot scheduled task in the \Pleiades\ folder, as win.capability.install does, which this method registers, waits for and removes. Many capabilities need a restart to finish going, which reboot_required says; win.reboot restarts the host. A run that removed one emits win.capability.install naming it, which fetches it from Windows Update again: a capability first installed from a source needs that source named in its rollback: list. A check reads the capability and sends nothing.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `WindowsFeatureCapable` |
| Transports | `winrm` |
| Requires elevation | yes |
| Runs | on or against the target device; acts on its target device |
| Check mode | Supported: reports what it would change and changes nothing |
| Engine version | `>=0.2.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `name` | `string` | yes | - | The capability's whole identity as DISM lists it, with its four tildes and version, such as ServerCore.AppCompatibility~~~~0.0.1.0 or Language.Basic~~~en-US~0.0.1.0 (Get-WindowsCapability -Online lists them). A name DISM does not recognize is refused rather than reported as already in the asked state, since that is nearly always a typo. |
| `timeout` | `int` | no | `3600` | How many seconds DISM may run. Task Scheduler stops it at that limit and the task fails saying so. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `name` | `string` | always | The capability this task acted on. |
| `reboot_required` | `bool` | always | Whether a restart is needed to finish: DISM answered 3010, or the capability was found already waiting for one. win.reboot restarts the host. |
| `diff` | `dict` | always | What DISM reported about the capability before this task and after it, each holding name, exists and state. Recorded even on a run that changed nothing. |

## Undoing this

**Can be undone.** A run that changes something records the instruction that reverses it, as an `inverse` stat holding the method to call and the parameters to call it with, resolved from the state this run actually found. A run that changed nothing records no instruction, which is how it says that undoing it means doing nothing.

A run that removed the capability emits a win.capability.install naming it, which fetches it from Windows Update again: one first installed from a source needs that source in its rollback: list. One found absent, or pending a restart to finish going, emits nothing.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `win.capability.install`
- `win.reboot`
- `win.feature.remove`

## Examples

Remove Steps Recorder:

```yaml
- name: Remove Steps Recorder
  win.capability.remove:
    name: App.StepsRecorder~~~~0.0.1.0
```


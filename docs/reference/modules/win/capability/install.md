---
status: beta
---

# win.capability.install

Installs a Windows capability (a feature on demand) with DISM, run as SYSTEM so Windows Update will serve it.

Makes sure a Windows capability, what Get-WindowsCapability lists and Windows calls a feature on demand, is installed: ServerCore.AppCompatibility, OpenSSH.Server and the like. Its state is read first with dism.exe /get-capabilityinfo, so a capability already installed, or installed and waiting only for a restart, reports no change and nothing is sent; a name DISM does not recognize is refused. The payload comes from Windows Update unless source names a folder or mounted image holding the host's features on demand, and then Windows Update is left alone. The Windows Update agent refuses a WinRM logon (Add-WindowsCapability over WinRM answers Access is denied), so the install runs dism.exe as SYSTEM in a one-shot scheduled task in the \Pleiades\ folder, which this method registers, waits for and removes. A large capability from Windows Update can take many minutes: ServerCore.AppCompatibility took 23 on a one-CPU host. Many capabilities need a restart to finish, which reboot_required says, both on the run that installed one and on a later run that finds it waiting; win.reboot restarts the host. A run that installed it emits win.capability.remove naming it. A check reads the capability and sends nothing.

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
| `source` | `string` | no | - | A folder or mounted image on the host holding its features on demand, such as D:\LanguagesAndOptionalFeatures, used instead of Windows Update. It may not hold a %, a quote or a line break. |
| `timeout` | `int` | no | `3600` | How many seconds DISM may run. Task Scheduler stops it at that limit and the task fails saying so. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `name` | `string` | always | The capability this task acted on. |
| `reboot_required` | `bool` | always | Whether a restart is needed to finish: DISM answered 3010, or the capability was found already waiting for one. win.reboot restarts the host. |
| `diff` | `dict` | always | What DISM reported about the capability before this task and after it, each holding name, exists and state. Recorded even on a run that changed nothing. |

## Undoing this

**Can be undone.** A run that changes something records the instruction that reverses it, as an `inverse` stat holding the method to call and the parameters to call it with, resolved from the state this run actually found. A run that changed nothing records no instruction, which is how it says that undoing it means doing nothing.

A run that installed the capability emits a win.capability.remove naming it; one that found it installed, or pending a restart to finish installing, emits nothing. Either direction may need the restart reboot_required reports before it is complete.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `win.capability.remove`
- `win.reboot`
- `win.feature.install`

## Examples

Make Server Core run more applications:

```yaml
- name: Install Server Core's application compatibility feature
  win.capability.install:
    name: ServerCore.AppCompatibility~~~~0.0.1.0
  register: appcompat

- name: Restart to finish it
  win.reboot: {}
  when:
    - result.appcompat.reboot_required
```


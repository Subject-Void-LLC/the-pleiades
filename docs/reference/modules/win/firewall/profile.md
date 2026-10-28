---
status: beta
---

# win.firewall.profile

Sets, or reports, one Windows Defender Firewall profile over WinRM: whether it is on, its default actions and its logging.

Names one profile, domain, private or public, and compares the settings the task gives with the machine's own (PersistentStore), changing only those that differ; a task giving none only reports the profile. A setting Windows holds as NotConfigured compares as what it does: on, block inbound, allow outbound, log nothing. The setting in force is read too (ActiveStore): one Group Policy sets to another value fails the task, naming it, since the machine's own setting has no effect there, after making the change if one was needed. The script is a constant and every value reaches it as JSON on standard input, held to its form first. The undo puts back the earlier value of each setting changed, as the value that was in force for a NotConfigured one. A check reads and reports what would change.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `WinRMCapable` |
| Transports | `winrm` |
| Requires elevation | no |
| Runs | on or against the target device; acts on its target device |
| Check mode | Supported: reports what it would change and changes nothing |
| Engine version | `>=0.2.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `name` | `string` | yes | - | The profile. One per task, so its undo is exact. **Choices:** `domain`, `private`, `public` (case ignored). |
| `enabled` | `bool` | no | - | Whether the firewall is on for this profile. |
| `default_inbound_action` | `string` | no | - | What it does with inbound traffic no rule matches. Windows' default is block. **Choices:** `allow`, `block` (case ignored). |
| `default_outbound_action` | `string` | no | - | What it does with outbound traffic no rule matches. Windows' default is allow. **Choices:** `allow`, `block` (case ignored). |
| `log_allowed` | `bool` | no | - | Whether allowed connections are logged. |
| `log_blocked` | `bool` | no | - | Whether dropped packets are logged. |
| `log_file` | `string` | no | - | The log file's path on the machine, as Windows takes it (%systemroot% may appear). |
| `log_max_size_kb` | `int` | no | - | The log's largest size in kilobytes, 1 to 32767. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `name` | `string` | always | The profile. |
| `settings` | `dict` | always | Its settings before, as the machine keeps them, in this method's parameters' form. |
| `in_force` | `dict` | always | Its settings in force before, Group Policy's included. |
| `differences` | `list of string` | always | How the machine's own settings differed from what was asked, empty when they matched. |
| `overridden` | `list of string` | always | The settings asked for that Group Policy held at another value. |

## Undoing this

**Can be undone.** A run that changes something records the instruction that reverses it, as an `inverse` stat holding the method to call and the parameters to call it with, resolved from the state this run actually found. A run that changed nothing records no instruction, which is how it says that undoing it means doing nothing.

A run that changed the profile undoes to the earlier value of each setting it changed. A setting Windows held as NotConfigured is put back as the value it had in effect, which behaves the same.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `win.firewall.rule`

## Examples

Log what the domain profile drops:

```yaml
- name: Log dropped packets on the domain network
  win.firewall.profile:
    name: domain
    log_blocked: true
    log_max_size_kb: 16384
```

Make sure the public profile is on and closed:

```yaml
- name: The public profile is on and lets nothing in
  win.firewall.profile:
    name: public
    enabled: true
    default_inbound_action: block
```


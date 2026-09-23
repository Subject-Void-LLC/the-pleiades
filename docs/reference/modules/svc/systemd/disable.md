---
status: beta
---

# svc.systemd.disable

Stops a systemd unit starting at boot, without stopping it now.

Makes sure a systemd unit is not set to start at boot. This is ansible.builtin.systemd with enabled=no, and it leaves the running system alone: a unit disabled by this task keeps running until svc.systemd.stop runs or the device reboots. State is read before anything is sent, so a unit that is already disabled reports no change. A static unit is refused, since a unit with no [Install] section was never enabled and cannot be disabled.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `SystemdCapable` |
| Transports | `ssh` |
| Requires elevation | yes |
| Check mode | Supported: reports what it would change and changes nothing |
| Engine version | `>=0.2.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `name` | `string` | yes | - | The systemd unit to act on, such as nginx or nginx.service. This is ansible.builtin.systemd's own parameter name. The unit must already exist: a name systemd does not know is refused rather than reported as already stopped, since that is nearly always a typo. |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `name` | `string` | always | The unit this task acted on. |
| `diff` | `dict` | always | What systemd reported about the unit before this task and after it, each holding exists, active, enabled and systemd's own load_state, active_state and unit_file_state. Recorded even on a run that changed nothing, because "it was already like this" is what tells a later rollback to do nothing. |

## Undoing this

**Can be undone.** A run that changes something records the instruction that reverses it, as an `inverse` stat holding the method to call and the parameters to call it with, resolved from the state this run actually found. A run that changed nothing records no instruction, which is how it says that undoing it means doing nothing.

A run that disabled an enabled unit emits an svc.systemd.enable naming it. A run that found it already disabled emits nothing. One thing the undo does not preserve: a unit that was enabled-runtime, meaning enabled only until the next reboot, comes back as permanently enabled, because systemctl enable has no way to say "only until reboot" about a unit it is restoring.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `svc.systemd.enable`
- `svc.systemd.stop`
- `svc.disable`

## Examples

Keep a service from starting at boot:

```yaml
- name: Stop nginx coming back after a reboot
  svc.systemd.disable:
    name: nginx
```


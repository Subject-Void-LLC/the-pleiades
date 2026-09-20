---
status: beta
---

# svc.systemd.start

Starts a systemd unit now, without changing whether it starts at boot.

Makes sure a systemd unit is running right now. This is ansible.builtin.systemd with state=started, and it is deliberately not also enable: starting and enabling are separate in systemd and separate here, so a task that wants both says both. State is read before anything is sent, so a unit that is already running reports no change and no command reaches the device. A unit systemd does not know is refused rather than reported as started, and a masked unit is refused with the mask named, since systemd's own error for that case describes a symlink rather than the cause.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `SystemdCapable` |
| Transports | `ssh` |
| Requires elevation | yes |
| Check mode | Supported: reports what it would change and changes nothing |
| Engine version | `>=1.0.0` |

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

A run that started a stopped unit emits an svc.systemd.stop naming it. A run that found it already running emits nothing, since undoing it means doing nothing. What the inverse cannot restore is anything the service did while it was up: connections it accepted, files it wrote, messages it consumed. Stopping it again returns the unit to where it was, not the system.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `svc.systemd.stop`
- `svc.systemd.restart`
- `svc.systemd.enable`
- `svc.start`

## Examples

Start a service:

```yaml
- name: Make sure nginx is running
  svc.systemd.start:
    name: nginx
```

Start it and make it survive a reboot:

```yaml
- name: Start nginx
  svc.systemd.start:
    name: nginx

- name: Make nginx start at boot too
  svc.systemd.enable:
    name: nginx
```


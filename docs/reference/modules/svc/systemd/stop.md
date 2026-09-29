---
status: beta
---

# svc.systemd.stop

Stops a systemd unit now, without changing whether it starts at boot.

Makes sure a systemd unit is not running right now. This is ansible.builtin.systemd with state=stopped, and it leaves the boot-time setting alone: a unit stopped by this task still starts at the next reboot unless svc.systemd.disable is also run. State is read before anything is sent, so a unit that is already stopped reports no change. A unit systemd does not know is refused rather than reported as stopped, which matters more here than anywhere else in this namespace: systemctl answers "inactive" for a name that has never existed, so a method trusting it would report success for a typo.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `SystemdCapable` |
| Transports | `ssh` |
| Requires elevation | yes |
| Runs | on or against the target device; acts on its target device |
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

A run that stopped a running unit emits an svc.systemd.start naming it. A run that found it already stopped emits nothing. Starting it again returns the unit to running, but nothing can restore what the service missed while it was down: requests that were refused, queues that backed up, timers that did not fire.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `svc.systemd.start`
- `svc.systemd.disable`
- `svc.stop`

## Examples

Stop a service:

```yaml
- name: Stop nginx before swapping its config
  svc.systemd.stop:
    name: nginx
```

Stop it now and keep it from coming back at boot:

```yaml
- name: Stop nginx
  svc.systemd.stop:
    name: nginx

- name: Keep nginx from starting at boot
  svc.systemd.disable:
    name: nginx
```


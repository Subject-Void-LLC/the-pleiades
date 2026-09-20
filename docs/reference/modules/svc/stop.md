---
status: beta
---

# svc.stop

Stops a service now, whichever service manager the device runs.

Makes sure a service is not running right now, without caring which init system the device uses. This is ansible.builtin.service with state=stopped: it resolves the device's service manager and hands the call to that manager's concrete method. It stops the service now and leaves the boot-time setting alone, so a service stopped by this task starts again at the next reboot unless svc.disable is also run.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `ServiceManagerCapable` |
| Transports | - |
| Requires elevation | yes |
| Check mode | Supported: reports what it would change and changes nothing |
| Engine version | `>=1.0.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `name` | `string` | yes | - | The service to act on. This is passed straight through to the concrete method for the device's service manager, so it means whatever that manager means by a service name: a systemd unit such as nginx or nginx.service on a Linux host. |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `name` | `string` | always | The service this task acted on, as recorded by the concrete method that ran. |
| `diff` | `dict` | always | What the service manager reported before this task and after it. The exact keys come from the concrete method that ran, since what there is to say about a service differs between service managers. |

## Undoing this

**Can be undone.** A run that changes something records the instruction that reverses it, as an `inverse` stat holding the method to call and the parameters to call it with, resolved from the state this run actually found. A run that changed nothing records no instruction, which is how it says that undoing it means doing nothing.

The inverse is whatever the concrete method records, so on a systemd host a run that stopped a running service emits an svc.systemd.start and a run that found it already stopped emits nothing. Starting it again restores the service, never what it missed while it was down.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `svc.start`
- `svc.disable`
- `svc.systemd.stop`

## Examples

Stop a service without naming the init system:

```yaml
- name: Stop nginx before maintenance
  svc.stop:
    name: nginx
```


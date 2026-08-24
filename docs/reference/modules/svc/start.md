---
status: beta
---

# svc.start

Starts a service now, whichever service manager the device runs.

Makes sure a service is running right now, without caring which init system the device uses. This is ansible.builtin.service with state=started: it resolves the device's service manager and hands the call to that manager's concrete method, so on a Linux host it runs svc.systemd.start and behaves exactly as that method does, including reporting no change when the service is already running. Use the concrete method instead when a runbook is written for one platform and should say so.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `ServiceManagerCapable` |
| Transports | - |
| Requires elevation | yes |
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

The inverse is whatever the concrete method records, so on a systemd host a run that started a stopped service emits an svc.systemd.stop and a run that found it already running emits nothing. The instruction names the concrete method rather than svc.stop, which is deliberate: by the time an undo runs, the device that was resolved is the device that must be undone.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `svc.stop`
- `svc.restart`
- `svc.enable`
- `svc.systemd.start`

## Examples

Start a service without naming the init system:

```yaml
- name: Make sure nginx is running
  svc.start:
    name: nginx
```


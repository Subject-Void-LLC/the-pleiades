---
status: beta
---

# virt.clab.node.ssh_access

Opens a temporary SSH door to a lab node through the server.

Asks the server for time-limited SSH access to the node: a port on the server that reaches it. The access is a session on the server, which this method's undo, virt.clab.ssh_session.terminate, closes; left alone it ends at its expiry. It returns the host, port, account and the command, never a password. The target is the containerlab API server, a generic_http device that onboarding recognised (it declares ContainerlabAPICapable); its login is the device's stored credential, with http_auth basic, and neither the password nor the token it gets is logged, returned or journaled.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `ContainerlabAPICapable` |
| Transports | `https` |
| Requires elevation | no |
| Runs | in the host process (the CLI or a Runner); acts on its target device |
| Check mode | Not supported: a check run names this task as unchecked, since it opens a session on the server, which a check must not |
| Engine version | `>=0.3.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `lab` | `string` | yes | - | The lab's name, as containerlab deployed it. |
| `node` | `string` | yes | - | The node's name in the lab (n1), or its container's name (clab-<lab>-n1). |
| `duration` | `string` | no | `1h` | How long the access lasts, as a duration (30m, 1h). |
| `ssh_username` | `string` | no | - | The node's SSH account, when it is not the server's default for its kind. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `host` | `string` | always | The server's host. |
| `port` | `int` | always | The port on the server that reaches the node. |
| `username` | `string` | always | The account to log in as. |
| `expiration` | `string` | always | When the access ends. |
| `command` | `string` | always | The ssh command that uses it. |

## Undoing this

**Can be undone.** A run that changes something records the instruction that reverses it, as an `inverse` stat holding the method to call and the parameters to call it with, resolved from the state this run actually found. A run that changed nothing records no instruction, which is how it says that undoing it means doing nothing.

Its undo closes the session it opened, by its port.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `virt.clab.ssh_session.list`
- `virt.clab.ssh_session.terminate`

## Examples

A door for ten minutes:

```yaml
- name: Reach r1
  virt.clab.node.ssh_access:
    target: clab-api
    lab: nclab
    node: r1
    duration: 10m
```


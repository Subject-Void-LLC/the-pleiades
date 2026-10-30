---
status: beta
---

# virt.clab.netem.set

Sets delay, jitter, loss or rate on a lab node's interface, unless it is already so.

Reads the interface's impairments first and acts only when they differ from what is asked, so a run that repeats it is unchanged. What it sets replaces what was there: an impairment not named is cleared. Its undo puts back what it found: the same settings, or a reset when the interface had none. corruption is not shown by netem.show, so a call that sets it always acts and its undo is partial. The target is the containerlab API server, a generic_http device that onboarding recognised (it declares ContainerlabAPICapable); its login is the device's stored credential, with http_auth basic, and neither the password nor the token it gets is logged, returned or journaled.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `ContainerlabAPICapable` |
| Transports | `https` |
| Requires elevation | no |
| Runs | in the host process (the CLI or a Runner); acts on its target device |
| Check mode | Supported: reports what it would change and changes nothing |
| Engine version | `>=0.3.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `lab` | `string` | yes | - | The lab's name, as containerlab deployed it. |
| `node` | `string` | yes | - | The node's name in the lab (n1), or its container's name (clab-<lab>-n1). |
| `interface` | `string` | yes | - | The interface (eth1), or its alias. |
| `delay` | `string` | no | - | A duration, such as 100ms. |
| `jitter` | `string` | no | - | A duration; needs delay. |
| `loss` | `float` | no | - | Packet loss, 0 to 100 percent. |
| `rate` | `int` | no | - | Rate limit in kbit/s. |
| `corruption` | `float` | no | - | Packet corruption, 0 to 100 percent. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `interfaces` | `list of dict` | always | The node's impaired interfaces after. |

## Undoing this

**Can be undone.** A run that changes something records the instruction that reverses it, as an `inverse` stat holding the method to call and the parameters to call it with, resolved from the state this run actually found. A run that changed nothing records no instruction, which is how it says that undoing it means doing nothing.

Its undo puts back what it found: the same settings, or a reset when the interface had none; partial when corruption was set, which the server does not show.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `virt.clab.netem.reset`
- `virt.clab.netem.show`

## Examples

A slow, lossy link:

```yaml
- name: r1 eth1 is slow
  virt.clab.netem.set:
    target: clab-api
    lab: nclab
    node: r1
    interface: eth1
    delay: 50ms
    loss: 1
```


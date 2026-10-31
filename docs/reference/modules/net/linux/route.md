---
status: beta
---

# net.linux.route

Makes a route present in a Linux host's main routing table, or absent.

Reads the routing table (ip route show) and acts only when the route differs: a route to the same destination and metric through another gateway or device is replaced, and its undo puts the old one back exactly; a new route's undo removes it. A route removed is made again by its undo. It runs ip as the account the task connects as, and changing the network needs root or CAP_NET_ADMIN: a task whose login lacks it fails with ip's own refusal, and become: true runs the task as root through sudo -n (see Writing runbooks). A check reads what a run reads and changes nothing.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `LinuxCapable`, `CommandExecCapable` |
| Transports | `ssh` |
| Requires elevation | no |
| Runs | on or against the target device; acts on its target device |
| Check mode | Supported: reports what it would change and changes nothing |
| Engine version | `>=0.3.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `destination` | `string` | yes | - | The destination prefix (10.2.2.0/24), or default. |
| `via` | `string` | no | - | The gateway address. Set via, dev or both. |
| `dev` | `string` | no | - | The outgoing interface. |
| `metric` | `int` | no | - | The route's metric; the kernel's default when unset. |
| `state` | `string` | no | `present` | present or absent. **Choices:** `present`, `absent`. |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `present` | `bool` | always | Whether the route is in the table now. |

## Undoing this

**Can be undone.** A run that changes something records the instruction that reverses it, as an `inverse` stat holding the method to call and the parameters to call it with, resolved from the state this run actually found. A run that changed nothing records no instruction, which is how it says that undoing it means doing nothing.

Its undo puts back what it found: a new route is removed, a replaced route is replaced by the old one, a removed route is added again.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `net.linux.address`
- `net.frr.config`

## Examples

Reach another LAN through the router:

```yaml
- name: h1 reaches LAN B through r1
  net.linux.route:
    destination: 10.2.2.0/24
    via: 10.1.1.1
  become: true
```


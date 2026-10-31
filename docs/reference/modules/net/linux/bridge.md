---
status: beta
---

# net.linux.bridge

Makes a Linux bridge exist with the ports given, or not exist.

Reads the bridge from /sys/class/net and changes only what differs: creates it, brings it up, and attaches each port in ports that is not on it. A device of that name that is not a bridge is refused, never replaced. Ports not listed are left alone unless remove_other_ports is set. Its undo puts back what it found: a bridge it created is removed, ports it attached are detached, and a bridge it removed is made again with the ports it held (partial: a port's own addresses and settings are not recorded). It runs ip as the account the task connects as, and changing the network needs root or CAP_NET_ADMIN: a task whose login lacks it fails with ip's own refusal, and become: true runs the task as root through sudo -n (see Writing runbooks). A check reads what a run reads and changes nothing.

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
| `name` | `string` | yes | - | The bridge's interface name. |
| `state` | `string` | no | `present` | present or absent. **Choices:** `present`, `absent`. |
| `ports` | `list of string` | no | - | Interfaces that must be attached to the bridge. |
| `up` | `bool` | no | `true` | Whether the bridge is administratively up. |
| `remove_other_ports` | `bool` | no | `false` | Detach interfaces attached to the bridge that ports does not name. |
| `detach_ports` | `list of string` | no | - | Interfaces to detach from the bridge. Set by an undo. |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `exists` | `bool` | always | Whether the bridge exists now. |
| `ports` | `list of string` | when it exists | Its ports now, sorted. |
| `up` | `bool` | when it exists | Whether it is up now. |

## Undoing this

**Can be undone.** A run that changes something records the instruction that reverses it, as an `inverse` stat holding the method to call and the parameters to call it with, resolved from the state this run actually found. A run that changed nothing records no instruction, which is how it says that undoing it means doing nothing.

Its undo puts back what it found: a created bridge is removed, attached ports are detached, detached ports are attached again, and a removed bridge is made again with the ports it held, which is partial: a port's own addresses and settings are not recorded.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `net.linux.address`
- `net.linux.route`
- `net.ovs.bridge`

## Examples

A switch of two ports:

```yaml
- name: br0 joins eth1 and eth2
  net.linux.bridge:
    name: br0
    ports: [eth1, eth2]
  become: true
```


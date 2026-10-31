---
status: beta
---

# net.linux.address

Makes an address present on a Linux interface, or absent.

Reads the interface's addresses (ip -o addr show) and adds or deletes only the one named, so a run that repeats it is unchanged. The interface must exist. Its undo is the other state of the same address. It runs ip as the account the task connects as, and changing the network needs root or CAP_NET_ADMIN: a task whose login lacks it fails with ip's own refusal, and become: true runs the task as root through sudo -n (see Writing runbooks). A check reads what a run reads and changes nothing.

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
| `interface` | `string` | yes | - | The interface (eth1, br0). |
| `address` | `string` | yes | - | The address with its prefix length, IPv4 or IPv6 (10.1.1.1/24). |
| `state` | `string` | no | `present` | present or absent. **Choices:** `present`, `absent`. |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `present` | `bool` | always | Whether the address is on the interface now. |
| `addresses` | `list of string` | always | The interface's addresses now. |

## Undoing this

**Can be undone.** A run that changes something records the instruction that reverses it, as an `inverse` stat holding the method to call and the parameters to call it with, resolved from the state this run actually found. A run that changed nothing records no instruction, which is how it says that undoing it means doing nothing.

Its undo is the other state of the same address.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `net.linux.bridge`
- `net.linux.route`

## Examples

Give an interface an address:

```yaml
- name: br0 has its address
  net.linux.address:
    interface: br0
    address: 10.1.1.1/24
  become: true
```


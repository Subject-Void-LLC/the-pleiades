---
status: beta
---

# net.ios.ping

Runs a ping from a Cisco IOS device and reports the result.

Answers a different question from net.ssh.ping, and the difference is the point: net.ssh.ping proves this platform can reach the device, while this method proves the DEVICE can reach somewhere else, which is the question that actually matters when a routing or ACL change is under review. Runs IOS's own ping from an interactive PTY session and parses its "Success rate is N percent (rx/tx)" line, including the trailing "round-trip min/avg/max = a/b/c ms" clause that IOS omits entirely when nothing came back. Nothing is changed on the device, so this always reports no change. Use state to turn the result into a gate: state present (the default) fails the task when every packet is lost, and state absent fails it when anything answers, so a runbook can assert reachability or its absence without a separate condition.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `CiscoIOSCapable` |
| Transports | `ssh` |
| Requires elevation | no |
| Engine version | `>=1.0.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `dest` | `string` | yes | - | The address or hostname to ping from the device. |
| `count` | `int` | no | `5` | How many echoes to send, passed to IOS as "repeat". |
| `source` | `string` | no | - | Source address or interface for the ping, passed to IOS as "source". |
| `vrf` | `string` | no | - | VRF to ping from, passed to IOS as "vrf". |
| `state` | `string` | no | `present` | "present" fails the task if the destination is unreachable (0 percent success); "absent" fails it if the destination answers at all. Set neither expectation by using a when condition on the returned facts instead. |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `packet_loss` | `string` | always | Percentage of packets lost, as a string with a trailing percent sign, e.g. "0%". |
| `packets_tx` | `int` | always | How many echoes the device sent. |
| `packets_rx` | `int` | always | How many replies the device received. |
| `rtt` | `map` | when at least one packet returned | Round-trip times in milliseconds: min, avg, max. Absent entirely when every packet was lost, because IOS prints no round-trip clause in that case. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

A ping is read-only: it sends echoes and reports what came back, changing nothing on the device, so there is nothing to undo.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `net.ssh.ping`
- `net.ios.facts`

## Examples

Assert the device can still reach its gateway:

```yaml
- name: Confirm the upstream gateway answers
  net.ios.ping:
    dest: 192.0.2.1
```

Record reachability without failing the run:

```yaml
- name: Measure reachability to a peer
  net.ios.ping:
    dest: 198.51.100.10
    count: 10
    state: absent
  register: peer
```


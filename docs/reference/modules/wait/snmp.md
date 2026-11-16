---
status: beta
---

# wait.snmp

Waits until the target's SNMP agent answers its credential with sysUpTime.0.

Tries the device's SNMP agent until a GET of sysUpTime.0 is answered with the SNMP family of the device's own credential, and fails when the timeout runs out first, through the same client the net.snmp.* methods and onboarding use: SNMPv3 unless the device's record opts into SNMPv1 or SNMPv2c (snmp_allow_cleartext_community), which is warned about. It is the task to put between one that starts or reconfigures a device and the first net.snmp.* task: a device answers a ping, and even SSH, before its agent runs, and wait.connection says nothing about SNMP. Each try is a new client, so a session from an earlier try is never taken for an answer. An SNMPv3 refusal ends the wait at once (an unknown user, a wrong authentication passphrase or protocol, a security level the user is not configured for, or a request the agent could not decrypt), since none of them changes by being asked again and a credential sent at every try can lock it out; so does a device record or credential the client refuses, such as a community for an SNMPv3 device or 3des privacy. An agent that does not answer within connect_timeout is tried again until the timeout, and that includes a wrong community: an SNMPv1 or SNMPv2c agent drops a request whose community it does not know without a word, so it reads exactly as an agent not up yet. A failed try is not an error, since it is what the task waits to see stop; the last one is quoted when the timeout ends the wait. The delay, backoff and steady parameters work as wait.connection's do. It changes nothing.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `SNMPCapable` |
| Transports | `snmp` |
| Requires elevation | no |
| Runs | on or against the target device; acts on its target device |
| Check mode | Not supported: a check run names this task as unchecked, since what it waits for is usually a device an earlier task starts or restarts, which a check never does, so a check would wait out its timeout and fail where the real run succeeds |
| Absence | No exists return: it is a wait: failing at its timeout is its purpose |
| Engine version | `>=0.4.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `timeout` | `int` | no | `600` | How many seconds to wait in total before giving up, counted from the start of the task, so the delay comes out of it. Must be more than 0. |
| `delay` | `int` | no | `0` | How many seconds to wait before the first try. Must be shorter than the timeout, which it is spent out of. |
| `sleep` | `int` | no | `5` | How many seconds to wait between tries, or with backoff: exponential before the second try, the gap doubling after each failed one. Must be more than 0. |
| `backoff` | `string` | no | `fixed` | How the gap between failed tries grows: fixed keeps it at sleep; exponential doubles it after each failed try, up to max_sleep, each gap a random point in its upper half. **Choices:** `fixed`, `exponential`. |
| `max_sleep` | `int` | no | `60` | With backoff: exponential, the longest gap between tries, in seconds. Not less than sleep; refused with backoff: fixed, where it would do nothing. |
| `steady` | `int` | no | `1` | How many tries in a row must be answered before the agent counts as ready. Must be at least 1, and the streak must fit in the timeout after the delay. |
| `steady_interval` | `int` | no | `10` | With steady above 1, the seconds between the answering tries. Must be more than 0; refused with steady 1, where it would do nothing. |
| `connect_timeout` | `int` | no | `20` | How many seconds one try may take, every retry of its GET included (the device's snmp_timeout and snmp_retries), before it counts as failed. Must be more than 0. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `elapsed` | `int` | always | How many whole seconds the task waited, including the delay. |
| `version` | `string` | always | The SNMP version the agent answered in: 3, 2c or 1. |
| `port` | `int` | always | The UDP port of the agent that answered. |
| `tries` | `int` | always | How many tries the task made, answered or not. |
| `failures` | `int` | always | How many of those tries did not answer. |
| `diff` | `dict` | always | The port that answered. Both halves are identical, since a wait changes nothing. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

This method waits for a device's SNMP agent to answer and changes nothing on it, so there is nothing to undo.

`pleiades rollback` and the Controller's rollback route replay a recorded instruction. It is recorded during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `wait.connection`
- `net.snmp.facts`
- `net.snmp.get`

## Examples

Wait for a device's SNMP agent after its configuration is applied:

```yaml
- name: Wait for the agent to answer its SNMPv3 user
  wait.snmp:
    timeout: 300
    sleep: 5
```


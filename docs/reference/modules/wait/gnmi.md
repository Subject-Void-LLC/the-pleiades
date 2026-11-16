---
status: beta
---

# wait.gnmi

Waits until the target's gNMI server answers its account with its Capabilities.

Tries the device's gNMI server until its Capabilities RPC answers with the device's own credential, and fails when the timeout runs out first, through the same client the net.gnmi.* methods use. It is the task to put between one that starts or restarts a network device and the first net.gnmi.* task: a device's gRPC port can accept connections before its gNMI service answers, and wait.connection, which logs in over SSH, says nothing about it. Each try is a new client on a new connection, so a connection kept open from an earlier try is never taken for an answer. The device's own TLS settings decide which certificate is trusted, and nothing here can weaken them: a certificate they do not trust ends the wait at once, before any call is sent, naming its subject and fingerprint and the tls_fingerprint or tls_ca_pem property that would trust it once checked; so does a server that requires a client certificate and was presented none, or one it does not accept (a TLS alert such as certificate required, which SONiC's server sends unless configured otherwise), naming the tls_client_certificate property; and so does a login the device refuses (UNAUTHENTICATED), since none of these changes by being asked again and a password sent at every try can lock the account. A server that refuses or resets the connection, sends no answer within connect_timeout, or answers with any other gRPC status (UNAVAILABLE while it starts, among them) is tried again until the timeout. A failed try is not an error, since it is what the task waits to see stop; the last one is quoted when the timeout ends the wait. The delay, backoff and steady parameters work as wait.connection's do. It changes nothing.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `GNMICapable` |
| Transports | `gnmi` |
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
| `steady` | `int` | no | `1` | How many tries in a row must open a session before the listener counts as ready. Each is its own login. Must be at least 1, and the streak must fit in the timeout after the delay. |
| `steady_interval` | `int` | no | `10` | With steady above 1, the seconds between the answering tries. Must be more than 0; refused with steady 1, where it would do nothing. |
| `connect_timeout` | `int` | no | `20` | How many seconds one try may take, from the connection to the Capabilities answer, before it counts as failed. Must be more than 0. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `elapsed` | `int` | always | How many whole seconds the task waited, including the delay. |
| `version` | `string` | always | The gNMI version the server named on the try that answered, such as 0.7.0; empty when it names none. |
| `tries` | `int` | always | How many tries the task made, answered or not. |
| `failures` | `int` | always | How many of those tries did not answer. |
| `diff` | `dict` | always | The version that answered. Both halves are identical, since a wait changes nothing. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

This method waits for a device's gNMI server to answer and changes nothing on it, so there is nothing to undo.

`pleiades rollback` and the Controller's rollback route replay a recorded instruction. It is recorded during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `wait.restconf`
- `wait.netconf`
- `net.gnmi.facts`
- `net.gnmi.config`

## Examples

Wait for a switch's gNMI server after it boots:

```yaml
- name: Wait for the switch's gNMI server to answer its account
  wait.gnmi:
    timeout: 600
    sleep: 5
```


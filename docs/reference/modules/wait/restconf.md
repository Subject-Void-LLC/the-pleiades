---
status: beta
---

# wait.restconf

Waits until the target's RESTCONF server answers its account with the capability list under its root.

Tries the device's RESTCONF server until an authenticated read under its root answers, and fails when the timeout runs out first: each try reads the origin's /.well-known/host-meta with no credential to find the root (taking the conventional /restconf when it answers 404 or a redirect, which is never followed), then, with the device's credential, the capability list ietf-restconf-monitoring keeps under it, through the same client the net.restconf.* methods use. It is the task to put between one that starts a network device and the first net.restconf.* task: a device's HTTPS can answer long before its RESTCONF does (a Catalyst 8000V running IOS XE 17.15.1a, measured, served its web server's own page at / over TLS 1.3 while every read under /restconf answered 502 for more than seven minutes after its licence reload), so an answer at / is not readiness, and wait.connection, which logs in over SSH, says nothing about it. Each try is a new client on a new connection, so a connection kept open from an earlier try is never taken for an answer. A read the device refuses with 409 Conflict, in-use, "Too many sessions." (IOS XE counts every RESTCONF request as a session until it expires) is waited out within the try's own time, from 2 seconds doubling to at most 30, and a try that time ends fails and is tried again on the wait's schedule. The device's own TLS settings decide which certificate is trusted, and nothing here can weaken them: a certificate they do not trust ends the wait at once, before any request is sent, naming its subject and fingerprint and the tls_fingerprint or tls_ca_pem property that would trust it once checked; so does a server that requires a client certificate and was presented none, or one it does not accept (a TLS alert such as certificate required, read on a connection of its own when the request's connection lost it to a reset), naming the tls_client_certificate property; and so does a login the device refuses (401, or 403 to the first read under the root), since none of these changes by being asked again and a password sent at every try can lock the account. A server that refuses or resets the connection, sends no answer within connect_timeout, answers 500, 502, 503 or 504, answers anything but the capability list, or answers the capability read under the conventional root 404 is tried again until the timeout. A host-meta answered 404 or a redirect is not a failed try in itself: IOS XE answers host-meta only to a valid login, and otherwise, a wrong password included, with a 302 to its WebUI's login page, which answers 404 (measured on 17.15.1a, 2026-11-15), so the credential is never sent there and the read under /restconf decides, where a wrong password is a 401 that ends the wait after one attempt. A failed try is not an error, since it is what the task waits to see stop; the last one is quoted when the timeout ends the wait. The delay, backoff and steady parameters work as wait.connection's do. It changes nothing.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `RestconfCapable` |
| Transports | `restconf` |
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
| `connect_timeout` | `int` | no | `20` | How many seconds one try may take, from the connection to the capability list's answer, before it counts as failed. Must be more than 0. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `elapsed` | `int` | always | How many whole seconds the task waited, including the delay. |
| `root` | `string` | always | The RESTCONF root on the try that answered, as an escaped path such as /restconf, or / for a root at the device's origin itself. |
| `root_source` | `string` | always | How root was learned: host-meta (its restconf link) or default (host-meta answered 404 or a redirect, as IOS XE answers it to anything but a valid login with a 302 to its WebUI's login page (404 there), and the read under the conventional /restconf answered). |
| `tries` | `int` | always | How many tries the task made, answered or not. |
| `failures` | `int` | always | How many of those tries did not answer. |
| `diff` | `dict` | always | The root that answered. Both halves are identical, since a wait changes nothing. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

This method waits for a device's RESTCONF server to answer and changes nothing on it, so there is nothing to undo.

`pleiades rollback` and the Controller's rollback route replay a recorded instruction. It is recorded during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `wait.netconf`
- `net.restconf.config`
- `net.restconf.facts`

## Examples

Wait for a router's RESTCONF server after it boots:

```yaml
- name: Wait for the router's RESTCONF server to answer its account
  wait.restconf:
    timeout: 900
    sleep: 10
```


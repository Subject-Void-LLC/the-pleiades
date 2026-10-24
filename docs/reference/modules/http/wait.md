---
status: beta
---

# http.wait

Waits, from the machine pleiades runs on, until a URL answers with the expected status, and optionally body.

Repeats http.check's request and its tests until they pass: the status is one of status_code, and the body contains body_contains or matches body_matches. A refused connection, a timeout or an answer that does not pass all mean not yet. It runs once with no target device, from the machine pleiades runs on, so it waits for a service published there (a container's port on 127.0.0.1) that wait.port, which looks from a device over SSH, cannot see. Each attempt is bounded by 10 seconds or by what is left of the timeout, and running out of time fails the task naming the last answer, or the last error. What it returns is http.check's, never the body: the last answer's status and final URL, how many attempts were made and how long the wait took. A URL carrying a user name or password is refused, a redirect from https to http is not followed, and certificates are verified unless validate_certs is false.

## Attributes

|  |  |
| --- | --- |
| Capabilities | - |
| Transports | - |
| Requires elevation | no |
| Runs | in the host process (the CLI or a Runner); acts on no device, so a task skips the runbook's hosts: and runs once |
| Check mode | Not supported: a check run names this task as unchecked, since what a wait waits for is usually an earlier task's change, which a check never makes, so a check would wait out its timeout and fail where the real run succeeds |
| Engine version | `>=0.2.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `url` | `string` | yes | - | The http or https URL to request. |
| `status_code` | `int or list of int` | no | `200` | The status, or statuses, that pass. |
| `body_contains` | `string` | no | - | Text the body must contain. |
| `body_matches` | `string` | no | - | A regular expression (RE2, at most 1024 bytes) the body must match. One of body_contains and body_matches. |
| `method` | `string` | no | `GET` | GET, or HEAD for the status alone. |
| `max_redirects` | `int` | no | `5` | How many redirects to follow, 0 to 10. |
| `validate_certs` | `bool` | no | `true` | Verify the server's certificate. False is set per task, never by a setting, and is visible in the runbook that sets it. |
| `timeout` | `int` | no | `300` | How many seconds to wait in total before giving up, counted from the start of the task, so the delay comes out of it. Must be more than 0, and at most 86400. |
| `delay` | `int` | no | `0` | How many seconds to wait before the first request. Must be shorter than the timeout, which it is spent out of. |
| `sleep` | `int` | no | `1` | How many seconds to wait between requests. Must be more than 0. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `status` | `int` | when the server answered | The last answer's status. |
| `final_url` | `string` | when the server answered | The URL that answered last, without query or fragment. |
| `attempts` | `int` | always | How many requests were made. |
| `elapsed` | `int` | always | How many whole seconds the task waited, including the delay. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

It sends a GET or a HEAD, which HTTP defines as safe, and changes nothing a later task could undo.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `http.check`
- `pleiades.builtin.wait.port`

## Examples

Wait for a service this machine runs to answer:

```yaml
- name: Wait for Batfish's API
  http.wait:
    url: http://127.0.0.1:9996/v2/version
    timeout: 180
    sleep: 3
```


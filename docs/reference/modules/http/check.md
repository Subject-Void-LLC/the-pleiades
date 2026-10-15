---
status: beta
---

# http.check

Checks that a URL answers with the expected status, and optionally body, without ever returning or logging the body.

Sends a GET (or a HEAD) to the URL, following at most max_redirects redirects and refusing one from https to http, since the check would then vouch for a page anyone on the path could have served. It fails when the status is not one of status_code, or when the body does not contain body_contains or match body_matches (RE2). The body is read, at most 4 MiB, only to match it: what is returned is the status, the final URL and each redirect (scheme, host and path, never a query or fragment, which can carry a token), the time taken, and the body's size and SHA-256, and a failure names the same. A URL carrying a user name or password is refused. Certificates are verified unless validate_certs is false, which is per task and loud. It only reads, so a check of it is the task itself, and it runs once with no target device.

## Attributes

|  |  |
| --- | --- |
| Capabilities | - |
| Transports | - |
| Requires elevation | no |
| Runs | in the host process (the CLI or a Runner); acts on no device, so a task skips the runbook's hosts: and runs once |
| Check mode | Supported: reports what it would change and changes nothing |
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
| `timeout` | `int` | no | `30` | Seconds the whole check may take, redirects included. |
| `validate_certs` | `bool` | no | `true` | Verify the server's certificate. False is set per task, never by a setting, and is visible in the runbook that sets it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `status` | `int` | when the server answered | The final answer's status. |
| `final_url` | `string` | when the server answered | The URL that answered last, without query or fragment. |
| `redirects` | `list of string` | when the server answered | Each redirect followed, in order, without query or fragment. |
| `elapsed_ms` | `int` | when the server answered | How long the check took, in milliseconds. |
| `body_bytes` | `int` | when the server answered | How many bytes of body were read, at most 4 MiB. |
| `body_sha256` | `string` | when the server answered | The SHA-256 of the bytes read, in hex. |
| `body_over_limit` | `bool` | when the server answered | Whether the body was longer than 4 MiB. |
| `matched` | `bool` | with body_contains or body_matches | Whether the body matched. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

It sends a GET or a HEAD, which HTTP defines as safe, and changes nothing a later task could undo.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `http.request`
- `wait.port`

## Examples

The release answers healthy:

```yaml
- name: Health
  http.check:
    url: https://app.example.com/healthz
    body_contains: "\"status\":\"ok\""
```


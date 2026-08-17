---
status: beta
---

# http.request

Makes an HTTP request and reports its status code and body.

Calls a URL from wherever the task runs, not from the target device, and records the status, body and response headers. A response whose status is not one of the expected ones fails the task, after recording what came back, since the body is usually the only thing that explains the failure. Certificates are verified unless a task says otherwise in its own text. Reporting changed follows the verb: GET, HEAD, OPTIONS and TRACE are read-only by HTTP's own definition and report no change, while any other verb reports a change, because what it did to the far side cannot be inspected from here. That is a deliberate difference from ansible.builtin.uri, which never reports changed at all. Four of that module's parameters are absent rather than accepted and ignored: body_format (the body is sent exactly as written, so set Content-Type in headers), return_content (the body is always recorded), follow_redirects (redirects are always followed), and the url_username and url_password pair (a credential belongs in the credential store, not in a runbook file).

## Attributes

|  |  |
| --- | --- |
| Capabilities | - |
| Transports | - |
| Requires elevation | no |
| Engine version | `>=1.0.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `url` | `string` | yes | - | The URL to call. It must be http or https: any other scheme is refused rather than attempted, since this method speaks one protocol and a file or ftp URL is a mistake in the runbook rather than a request this could make. |
| `method` | `string` | no | `GET` | The HTTP method. It is upper-cased before being sent, because HTTP method names are case sensitive and a server given get will answer 501 rather than doing what the author meant. |
| `body` | `string` | no | - | The request body, sent exactly as written. Set its Content-Type through headers: nothing here inspects the body or guesses a type for it. |
| `headers` | `dict` | no | - | Request headers as a mapping of name to value. Every value must be text, so a numeric one is quoted in the runbook. A Host header is honored as the request's real Host rather than added as an ordinary header, which is what makes name-based routing testable against an address. |
| `status_code` | `int or list of int` | no | `200` | The status code, or codes, that count as success. Anything else fails the task. Written as one number or a list of them, so an endpoint answering 200 or 201 depending on whether it created something can be accepted without a follow-up condition. |
| `timeout` | `float` | no | `30` | How long to wait, in seconds, for the whole request including reading the body. Fractions are allowed, unlike Ansible's whole-second version, since a health check with a half-second budget is a real thing to want. Zero and negative values are refused: neither is a wait. |
| `validate_certs` | `bool` | no | `true` | Verify the server's TLS certificate. Setting it false accepts any certificate, including one an attacker in the middle presents, so it belongs only on a target you have decided does not need it. There is no setting anywhere that changes the default: turning verification off is written in the task it applies to. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `status` | `int` | always | The status code the server answered with, recorded even when it is not one of the expected ones. |
| `content` | `string` | always | The whole response body as text. Held in memory, so this method is for calling an API rather than for fetching a large file. |
| `headers` | `dict` | always | The response headers, names lower-cased, with a header sent more than once joined by a comma and a space. |
| `url` | `string` | always | The URL the response actually came from, which differs from the one asked for when redirects were followed. |
| `elapsed` | `float` | always | How long the request took, in seconds, with its fraction kept. Ansible reports whole seconds here, which is zero for every call that went well. |
| `msg` | `string` | always | The status line, for example "404 Not Found", for a person reading a run log. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

One answer has to cover every verb, and no verb can produce an inverse. A read-only request (GET, HEAD, OPTIONS, TRACE) changed nothing, so there is nothing to undo. A writing request changed something inside a remote system this platform never observes: it cannot read what the state was before, cannot tell what the call altered, and cannot know which call would put it back, so any inverse would be a guess dressed as an instruction.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `exec.command`
- `pleiades.builtin.wait.port`

## Examples

Check that a service answers:

```yaml
- name: Wait for the health endpoint
  fqcn: http.request
  params:
    url: https://api.example.com/healthz
    timeout: 5
```

Post JSON to an API:

```yaml
- name: Register the release
  fqcn: http.request
  params:
    url: https://api.example.com/releases
    method: POST
    body: '{"version": "1.4.0"}'
    headers:
      Content-Type: application/json
    status_code:
      - 200
      - 201
```


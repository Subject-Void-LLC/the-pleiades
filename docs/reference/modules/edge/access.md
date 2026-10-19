---
status: beta
---

# edge.access

Checks a Cloudflare Access application's audience, issuer, settings and policies against what is expected.

Reads an Access application by id, or by its domain (one two applications share is refused, naming both), its policies, and the issuer of the tokens Access signs for it (https:// and the account's team domain). Each field expect names is compared: audience, issuer and domain (domain and issuer without regard to case), type and session_duration. Policies are compared by name: a policy expected and absent is missing; one whose decision, precedence or include, exclude or require rules differ is changed, rules compared as JSON with their keys sorted; and with exact_policies one present and not expected is extra. The task fails listing every difference. It reads through Cloudflare's API with the cloudflare_account device's own token (stored with add-credential --token-stdin, sent only to its api_base, no redirect followed), follows every list page by page up to 50 pages, retries a rate limit or a server error twice, and changes nothing, so a check of it is the task itself. A token without the permission a read needs is refused by Cloudflare (403), and the error says so.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `CloudflareAPICapable` |
| Transports | `https` |
| Requires elevation | no |
| Runs | in the host process (the CLI or a Runner); acts on its target device |
| Check mode | Supported: reports what it would change and changes nothing |
| Engine version | `>=0.2.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `app` | `string` | yes | - | The application's id, or its domain. |
| `expect` | `dict` | no | - | What the application must be: any of audience, issuer, domain, type, session_duration, and policies (a list of {name, decision, precedence, include, exclude, require}, each field optional but name). |
| `exact_policies` | `bool` | no | `false` | Fail on a policy the application has and expect.policies does not name. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `id` | `string` | success or difference | The application's id. |
| `domain` | `string` | success or difference | Its domain. |
| `audience` | `string` | success or difference | Its audience tag (aud), which an origin validating Access tokens must expect. |
| `issuer` | `string` | success or difference | The issuer of its tokens. |
| `policies` | `list of dict` | success or difference | Each policy's name, decision and precedence. |
| `differences` | `list of string` | when the reads succeed | Every difference from what was expected; the task fails when there is one. A read that fails (a refused token, a name that resolves to nothing) fails the task before there are differences to list. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

It reads the account through Cloudflare's API and changes nothing.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `edge.tunnel`
- `edge.dns`

## Examples

Only staff reach the admin site:

```yaml
- name: Access
  edge.access:
    app: admin.example.com
    expect:
      audience: 2b1d0a9c5f3e8d7a6b4c1e0f9d8c7b6a5e4d3c2b1a0f9e8d7c6b5a4f3e2d1c0b
      issuer: https://example.cloudflareaccess.com
      policies:
        - name: staff
          decision: allow
          include: [{email_domain: {domain: example.com}}]
    exact_policies: true
```


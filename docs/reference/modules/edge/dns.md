---
status: beta
---

# edge.dns

Checks a DNS record set in a Cloudflare zone against what is expected.

Reads a zone by id, or by name within the account (a zone of another account is refused, even where the token reaches it), and its records of one name and type. The set of contents is compared with expect.content, addresses in their canonical form and names in lower case without the final dot; proxied and ttl are compared on every record when expected; and absent requires there be none. With no expect, the task fails only when there is no such record. It reads through Cloudflare's API with the cloudflare_account device's own token (stored with add-credential --token-stdin, sent only to its api_base, no redirect followed), follows every list page by page up to 50 pages, retries a rate limit or a server error twice, and changes nothing, so a check of it is the task itself. A token without the permission a read needs is refused by Cloudflare (403), and the error says so.

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
| `zone` | `string` | yes | - | The zone's id, or its name. |
| `name` | `string` | yes | - | The record's full name (www.example.com). |
| `type` | `string` | yes | - | The record type. **Choices:** `A`, `AAAA`, `CNAME`, `TXT`, `MX`, `NS`, `SRV`, `CAA`, `HTTPS`, `SVCB`, `PTR`. |
| `expect` | `dict` | no | - | What the records must be: content (one value or a list, compared as a set), proxied, ttl (1 is automatic), or absent: true. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `zone` | `string` | success or difference | The zone's name. |
| `zone_id` | `string` | success or difference | The zone's id. |
| `name` | `string` | success or difference | The record name read, as the task gave it. |
| `type` | `string` | success or difference | The record type read. |
| `records` | `list of dict` | success or difference | Each record's content, proxied and ttl. |
| `differences` | `list of string` | when the reads succeed | Every difference from what was expected; the task fails when there is one. A read that fails (a refused token, a name that resolves to nothing) fails the task before there are differences to list. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

It reads the account through Cloudflare's API and changes nothing.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `edge.tunnel`
- `edge.access`

## Examples

www points at the two load balancers, proxied:

```yaml
- name: DNS
  edge.dns:
    zone: example.com
    name: www.example.com
    type: A
    expect:
      content: [192.0.2.10, 192.0.2.11]
      proxied: true
```


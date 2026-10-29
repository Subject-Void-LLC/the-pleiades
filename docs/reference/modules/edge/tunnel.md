---
status: beta
---

# edge.tunnel

Checks a Cloudflare Tunnel's status and how many of its connectors are up.

Reads a tunnel by id, or by name among the account's tunnels not deleted (a name two tunnels share is refused, naming both), and its connectors: each cloudflared process serving it, with its version, architecture and connections by colo. The task fails when the tunnel's status is not expect_status or fewer connectors than min_connectors have a live connection (one pending reconnect has dropped, and is only still tracked, so it does not count). It reads through Cloudflare's API with the cloudflare_account device's own token (stored with add-credential --token-stdin, sent only to its api_base, no redirect followed), follows every list page by page up to 50 pages, retries a rate limit or a server error twice, and changes nothing, so a check of it is the task itself. A token without the permission a read needs is refused by Cloudflare (403), and the error says so.

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
| `tunnel` | `string` | yes | - | The tunnel's id, or its name. |
| `expect_status` | `string` | no | `healthy` | The status the tunnel must have; any accepts every status. **Choices:** `healthy`, `degraded`, `down`, `inactive`, `any`. |
| `min_connectors` | `int` | no | `1` | The fewest connectors that must have a connection to Cloudflare's edge, 0 to 1000. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `id` | `string` | success or difference | The tunnel's id. |
| `name` | `string` | success or difference | The tunnel's name. |
| `status` | `string` | success or difference | healthy, degraded, down or inactive, as Cloudflare reports it. |
| `connectors` | `list of dict` | success or difference | Each connector's id, version, architecture, connection count and colos. |
| `connectors_up` | `int` | success or difference | How many connectors have a connection. |
| `connections` | `int` | success or difference | How many live connections the connectors hold in all, not counting those pending reconnect. |
| `differences` | `list of string` | when the reads succeed | Every difference from what was expected; the task fails when there is one. A read that fails (a refused token, a name that resolves to nothing) fails the task before there are differences to list. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

It reads the account through Cloudflare's API and changes nothing.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `edge.access`
- `edge.dns`

## Examples

The production tunnel is healthy on two connectors:

```yaml
- name: Tunnel
  edge.tunnel:
    tunnel: prod
    min_connectors: 2
```


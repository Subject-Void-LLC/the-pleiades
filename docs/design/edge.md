# Design note: the `edge` collection

Status: design. A run asks whether what an edge network serves is what the operator meant it to be. Three questions,
all of them reads:
- whether a tunnel is up, and on how many connectors;
- whether an access application authenticates for the audience, issuer and policies expected;
- whether a DNS record says what it should.

The provider is Cloudflare: its Tunnel, Access and DNS are what the methods read. Each method fails the task on any
difference from what was expected, and lists every difference.

## The account is a device

A `cloudflare_account` device. Its record says:
- `account_id`: 32 hex digits, required;
- `api_base`: optional, default `https://api.cloudflare.com/client/v4`, so a proxy or a test server can stand in;
- the `pkg/devicetls` settings: a pinned CA, and the name to check its certificate against.

Its API token is the device's own credential, held **by reference**:
- stored with `pleiades add-credential <account> --token-stdin`;
- sent only as a bearer header to `api_base`, with no redirect followed;
- never in a parameter, a stat, an error or the journal;
- a template's machine credential never becomes one (`record.RegisterOwnCredentialType`).

`api_base` must be https. A token is never sent in the clear: there is no plaintext opt-in here, because Cloudflare
has no plaintext endpoint to need one for.

The account starts `discovered`. Onboarding reads the account (`GET /accounts/{account_id}`) with the token, which
proves the token, the account and the TLS, and records the account's name. It grants `CloudflareAPICapable`,
bound to `account_id`, `api_base` and the TLS settings, so a repointed record is onboarded again before its token
goes anywhere new. A token scoped too narrowly for a method is refused by Cloudflare when that method reads (403),
and the method names the permission it needed.

## Methods

| Method | Reads | Fails when |
|---|---|---|
| `edge.tunnel` | A tunnel by id or name: its status and its connectors (each with its version, architecture and connections by colo). | The status is not `expect_status` (default `healthy`), or fewer connectors than `min_connectors` (default 1) are up. |
| `edge.access` | An Access application by id or domain: its audience tag, its issuer (`https://<team domain>`, from the account's Access organization), its session duration, and its policies (name, decision, include, exclude, require, precedence). | Any field `expect` names differs. Policies are compared by name: a policy expected and absent is missing, one present and not expected is extra (only with `exact_policies`), and one whose decision or rules differ is changed, its rules compared as JSON. |
| `edge.dns` | A zone by id or name, and the records of one name and type. | The set of contents is not the expected set, or `proxied` or `ttl` differ when expected. |

All three are read-only, so a check of each is the task itself. Each returns what it read, bounded, with the token
nowhere in it.

## Capabilities

`cloudflare_account` declares `NetworkAddressable` and, once onboarded, `CloudflareAPICapable`. That capability is
reached over `https` and carries the account id, the API base and the TLS settings. Every `edge` method requires
it. They run on the controller (`Site: controller`) against the target device, so `hosts: [prod-cf, staging-cf]`
checks two accounts.

## Check and Reversibility

| Method | Check | Reversible |
|---|---|---|
| `edge.tunnel`, `edge.access`, `edge.dns` | Same as Invoke: they only read. | Read-only. |

## Decisions, by edge case

- **Read, never write.** Changing a tunnel, an Access policy or a DNS record is a change to what the whole internet
  reaches, and Cloudflare's own tools (Terraform's provider, the API) do it with plan and apply. The goal named
  these methods read-mostly, and a verifier is useful the day it lands. Writing is a later decision, with its own
  check and undo.
- **A name resolves to exactly one.** A tunnel, an application or a zone named rather than given by id must
  resolve to one. Two (a deleted tunnel's name reused, two applications on one domain) is refused, naming both
  ids, rather than reading whichever came first.
- **Pagination is followed and bounded.** Every list Cloudflare pages is read page by page, up to 50 pages. Past
  that the method fails rather than judge on part of a list.
- **Rate limits.** A 429 or a 5xx is retried twice with backoff, honoring `Retry-After`; anything else is not.
  Every read is safe to repeat.
- **Untrusted text.** Names, rules and record contents come from Cloudflare and are returned as data, bounded and
  escaped by the run's output, and are never placed into a later request but by the parameters a task names.

## Reuse

`pkg/httpapi` (the base URL rules, joining), `pkg/devicetls`, and the device record and onboarding machinery (as
`ai_provider` and `kubernetes_cluster` use them). New: `pkg/cfapi`, a bounded, read-only Cloudflare API client (the
envelope, errors, pagination and retries), the `cloudflare_account` device type and its prober, and
`internal/catalog/edge`.

## Failure modes

| Failure | Answer |
|---|---|
| No token stored | Refused before contact, naming `add-credential --token-stdin`. |
| The token is refused (401), or lacks a permission (403) | Fails naming the status, Cloudflare's own error codes and messages, and the permission the method reads with. |
| A name resolves to none, or to more than one | Refused, naming what was found. |
| A list longer than 50 pages | Fails rather than judge on part of it. |
| Any difference from what was expected | Fails, listing every difference, with what was read returned. |
| An answer that is not Cloudflare's envelope, or over 4 MiB | Fails; nothing of a malformed answer is judged. |

## What is proved, and how

Each method's tests run against a real HTTP server in the test process that answers as Cloudflare's API documents:
the envelope, its pagination and its errors. No test reaches Cloudflare, and nothing here has been run against a
real account. The final report says so, and the sample runbook says what to change to point it at one.

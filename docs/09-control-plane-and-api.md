---
status: beta
---

# Control plane and API

This book covers the Crawl-tier control plane's HTTP API: what it looks like today,
how authorization works, and what is and is not built. See
[Start here](01-start-here.md) for the tier vocabulary and the honest summary of what
is real (the control plane's own data layer, event bus, locking, RBAC, and the job
dispatcher) versus what is still a stub (the distributed execution plane a `runner`
would carry out).

## Architecture

`cmd/controller` is the API Gateway composition root: one process, embedding SQLite
today (no Postgres migration path exists yet), talking to NATS JetStream for events
and log streaming, and serving the versioned API described below. A Front Controller
pattern owns request handling: every route passes through tracing, metrics,
structured logging, rate limiting, authentication, and scope authorization, in that
order, before its own handler ever runs.

## The route table, generated

The five real routes are generated documentation, not hand-maintained prose:
[the OpenAPI document](reference/schemas/openapi.json) (also served live at
`/api/v1/openapi.json`) and [the CLI reference](reference/cli.md) share the same
"generate from one source" discipline every other reference page in this set does.
Both `cmd/controller`'s real router and the generated OpenAPI document build from
`internal/apispec.Endpoints`, so a route's method, pattern, required scope, and
hypermedia relation can never drift between what the server actually serves and what
the generated document claims it serves.

| Method | Pattern | Scope | Relation |
|---|---|---|---|
| POST | `/jobs/dispatch` | `runbook:execute` | `execute` |
| GET | `/jobs/{id}` | `job:read` | `self` |
| GET | `/jobs/{id}/logs` | `job:read` | `logs` |
| GET | `/inventory/devices/{name}` | `inventory:read` | `self` |
| DELETE | `/inventory/devices/{name}` | `inventory:write` | `delete` |

Three more operational endpoints exist outside the versioned, authenticated
`/api/v1` prefix entirely, on purpose: `/healthz` and `/readyz` are orchestrator
contracts, and `/metrics` is a Prometheus scrape contract. Versioning or
authenticating any of the three would break every stock probe and scrape config for
no benefit, since none of them carries a payload schema that could ever need a v2.
`/.well-known/pleiades/*.json` (the generated JSON Schemas and module catalog) and
`/api/v1/openapi.json` sit alongside them for the identical reason: none of the four
documents carries any data about a specific deployment, only the product's own
static shape, so there is nothing to protect and no caller to turn away.

## Authorization

Two separate mechanisms answer two separate questions, and conflating them is the
most common way to misread this API.

**Which permission scope does this route require?** Every route's `Scope` (the table
above) is checked against the calling identity's JWT `Scopes` claim. A caller with
the `admin` role bypasses this check entirely, for every scope, unconditionally. A
caller without the `admin` role needs the specific scope a route declares, present
in their token, or `*` (a wildcard scope). There is no static "role X always gets
scope Y" table baked into the code: which scopes a non-admin identity's token
carries is a property of how that token was issued, not a fixed grant table this API
enforces.

**Who can be granted access at which target (a whole system, an organization, a
group, or one device)?** This is a separate, hierarchical RBAC layer
(`internal/auth.ScopeResolver`) with its own three-tier role ordering (`viewer` <
`operator` < `admin`, most permissive last) used to resolve *RoleBindings*: which
role a subject holds at which scope target. An explicit Deny at any level always
wins over an Allow at the same or a broader level. This layer decides who can be
bound to a role at all; the permission-scope check above decides what that identity's
resulting token can actually call.

## Errors

Every error response is `{"error": "<message>"}`, nothing else: no `_links` array, a
deliberate choice, since a caller who did not reach a resource has no affordances
against it worth naming, and enumerating relations on a 403 would hand an
unauthorized caller a map of what exists. The message itself is deliberately generic
for anything past a client mistake (a malformed ID, a missing parameter): an
underlying storage error is logged server-side, never echoed into the response body,
since it can name tables, columns, or filesystem paths an authenticated caller
holding only a narrow scope has no business reading.

## Hypermedia and link relations

A successful response embeds a `_links` array: every affordance the calling identity
is actually authorized to take against that resource right now, computed once by a
single builder shared between the `_links` array and the `Allow` header an `OPTIONS`
request returns, so the two can never disagree. Six relation names exist today:

| Relation | Meaning |
|---|---|
| `self` | This resource's own canonical URL. |
| `create` | Not used by any route in the table above yet. |
| `update` | Not used by any route in the table above yet. |
| `delete` | Retire this resource. |
| `execute` | Trigger an action against this resource (dispatch a runbook). |
| `logs` | Stream this resource's live progress. |

`create` and `update` are declared but unused: no route in the current table needs
them yet, and they exist so a future route does not need a new relation vocabulary
invented for it.

## Pagination

No route returns a paginated list today: `GET /jobs/{id}` and
`GET /inventory/devices/{name}` both read exactly one resource by ID. Nothing in
this API defines a pagination shape yet; a future list endpoint will need one, not
inherit an assumption from what exists today.

## The SSE job log stream

`GET /jobs/{id}/logs` streams a job's live progress over Server-Sent Events.
Precisely:

- `id` is validated as a UUID before use. This is a real, previously-fixed security
  boundary, not defensive styling: the job ID is concatenated directly into a NATS
  subject, and an unvalidated value of `>` (a NATS wildcard) once widened the
  subject filter to stream every job in the system to whoever asked.
- Each concurrent viewer gets a fresh, ephemeral JetStream consumer scoped to
  exactly the requested job ID, so two viewers watching two different jobs never see
  each other's events.
- Each SSE frame is `data: <json>\n\n`, where the JSON is the same per-task status
  event the engine publishes during a run (timestamp, status, host, task label, and
  a message), best-effort masked through every `register_mask`/`secret_mask` value
  known at the moment that specific event was published. See
  [Running in production](10-running-in-production.md)'s data handling section for
  the precise, non-retroactive scope of that masking: it is real, but it is not a
  guarantee that every byte on this stream is scrubbed.
- The stream never terminates on its own; the client closes it.

Today this stream carries real event traffic only for the Walk-tier CLI's execution
path. The Crawl-tier distributed path (a job dispatched to a `runner` over NATS)
does not yet run real tasks (see [Start here](01-start-here.md)), so a job dispatched
through this API has nothing substantive to stream yet either.

## MCP tool provider

Not built. No Model Context Protocol server or tool provider exists anywhere in this
codebase today; treat any mention of one as aspirational until this line is removed.

## Web UI

Mostly a mockup. Of its routes, one (a live SSE job log viewer) is real; the rest
show hardcoded placeholder content. Treat it as `experimental` and prefer the API
directly, or the CLI, for anything that matters today.

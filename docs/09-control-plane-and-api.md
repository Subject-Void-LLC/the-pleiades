---
status: beta
---

# Control plane and API

This book covers the Crawl-tier control plane's HTTP API: what it looks like today,
how authorization works, and what is and is not built. See
[Start here](01-start-here.md) for the tier vocabulary and the honest summary of what
is real (the control plane's own data layer, event bus, locking, RBAC, the job
dispatcher, and the distributed execution plane a `runner` carries out) versus the
limits that still apply to credential storage.

## Architecture

`cmd/controller` is the API Gateway composition root: one process, storing state in
either PostgreSQL or an embedded SQLite file (whichever `DB_DSN` names), talking to
NATS JetStream for events and log streaming, and serving the versioned API described
below. PostgreSQL is what a multi-user deployment runs; SQLite needs no server and
suits a single-process trial. Both go through the same versioned migrations and are
held to one shared conformance suite, so neither is a second-class path. A Front Controller
pattern owns request handling: every route passes through tracing, metrics,
structured logging, rate limiting, authentication, and scope authorization, in that
order, before its own handler ever runs.

## The route table, generated

The five real routes are generated documentation, not hand-maintained prose:
[the OpenAPI document](reference/schemas/openapi.json) (also served live at
`/api/v1/openapi.json`) and [the CLI reference](reference/cli.md) share the same
"generate from one source" discipline every other reference page in this set does.
Both `cmd/controller`'s real router and the generated OpenAPI document build from
the same `internal/apispec` endpoint values, so for a route that appears in both,
the method, pattern, required scope, and hypermedia relation cannot drift: the
router asks each endpoint for its own `Route`, which copies all four fields off the
exact value the document is rendered from.

**Which routes are on the list is not enforced.** The generator walks the
`apispec.Endpoints` slice, while `cmd/controller` names its five endpoints one at a
time in its own route table. Nothing compares the two sets. Add a sixth endpoint to
the slice, forget to register it, and the build says nothing: `go build` and `go vet`
both pass, no test notices, and the served document then advertises a route no
handler was ever mounted for. The request falls through to the router's not-found
path instead of reaching any handler. So trust a listed route's method, pattern,
scope, and relation. Do not read the list itself as proof that every route on it
exists; call a route against a running controller before you build on it.

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
- Each SSE frame is `data: <json>\n\n`, where the JSON is a full event envelope
  (`id`, `type`, `timestamp`) with the per-task status event nested under `data`:
  timestamp, status, host, task label, and a message. Nothing on this stream is
  masked. The `register_mask`/`secret_mask` machinery runs inside the engine and
  on the CLI's own printed output, and neither of this subject's two publishers
  goes through it. See
  [Running in production](10-running-in-production.md)'s data handling section for
  what masking does cover, and assume anything a publisher puts on this stream
  reaches the viewer unscrubbed.
- The stream never terminates on its own; the client closes it.

**This stream carries no real task results today, on any path.** The Walk-tier
CLI is not even one of its publishers: `pleiades run` builds its engine on an
in-process bus (`event.NewInProcessBus`, in `cmd/pleiades/run.go`), never opens a
NATS connection, and publishes its per-task status events under
`pleiades.events.workflow.<dag-id>.node.<node-id>`, while this handler only ever
reads `pleiades.jobs.logs.<job-id>`. The two subject spaces do not overlap, and
the two buses never meet.

Two things publish to `pleiades.jobs.logs.<job-id>`, and they differ in what they
mean. A `runner` that picks up a dispatched job runs it through
`internal/adapters/native`, which executes the runbook against the real device the
dispatch names and reports what actually happened, so those frames are genuine task
results. `cmd/demo` generates fake Ansible events to scaffold the web UI, and those
are not. Read the stream as real evidence only when a `runner` produced it; the demo
binary exists precisely so the UI can be developed without one.

## MCP tool provider

Not built. No Model Context Protocol server or tool provider exists anywhere in this
codebase today; treat any mention of one as aspirational until this line is removed.

## Web UI

Server-rendered, served by `cmd/controller` itself at `/ui`, same-origin, from assets
compiled into the binary. There is no separate front-end build, no Node toolchain, no
nginx image and no reverse-proxy seam between the two: browse to the controller's own
address and add `/ui`.

Sign in at `/ui/login` by pasting a token this control plane already accepts. It is
validated by the same evaluator the `Authorization: Bearer` path uses and exchanged
for a server-side session cookie, so the UI adds no second notion of who a caller is
and no password store. Interactive sign-in through an identity provider is deferred
to its own phase; token paste is the bootstrap and break-glass path.

That cookie is also what makes the SSE job log viewer work. An `EventSource` cannot
be given request headers -- its constructor takes a URL and a `withCredentials` flag,
nothing more -- so while `Authorization: Bearer` was the only credential this API
accepted, no browser could reach the stream at all. `/api/v1` now accepts a Bearer
header **or** a session cookie, resolved by one middleware into one identity, and the
log viewer at `/ui/jobs/{id}/logs` connects with the cookie alone.

Six views are registered. Which of them do anything is decided per view and stated on
the page rather than in this document:

| View | State | Notes |
|---|---|---|
| Dashboard | Real | Job-outcome counts over the most recent 200 dispatches, as a chart and as an equivalent table. |
| Inventories | Real | Full create, read, update and retire against the device repository. Device *properties* are deliberately not editable: they decrypt to real secrets, and the masking ruleset belongs to an unbuilt phase. |
| Jobs | Real | List, open, and dispatch a runbook. No cancel and no delete -- there is no `job:write` scope and no cancellation path in this build, so no button is offered for one. |
| Runbooks | Real | Read-only catalog. Runbooks come from `RUNBOOK_DIR` and from GitOps; a write path here would be a second, unversioned way to change what this platform executes. |
| Governance | Declared | Registered so the shape and the navigation are real. Nothing backs it, and the page says so. |
| Credentials | Declared | Unbuilt, and it will not list credential names when it is built: the set of names in a deployment is itself reconnaissance. |

A view registered as *declared* renders an explicit "declared, not implemented" panel.
That distinction is load-bearing: an empty table and an unimplemented view look
identical to a reader, and the difference between "nothing has happened yet" and "this
does not work" is exactly the one worth being told.

Accessibility is a build gate rather than a review item. Every registered view is
rendered through the real templates in the conformance suite and asserted against a
shared set of checks -- one `h1`, one `main`, a labelled control for every input, no
positive `tabindex`, no duplicate ids, no dangling `aria-describedby`, a skip link
first in the tab order. Colour contrast is computed from the stylesheet's own tokens
across every skin and theme combination. What no Go test can cover -- focus order
making sense, error text explaining anything, a screen reader's actual reading -- is
covered by the manual script in [the web UI page](12-web-ui.md).

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

Exactly two things publish to `pleiades.jobs.logs.<job-id>`, and neither one
touches a device. A `runner` that picks up a dispatched job runs it through
`internal/adapters/native`, whose `Execute` sleeps and fabricates a `"pong from
<device>"` line (see [Start here](01-start-here.md)). `cmd/demo` generates fake
Ansible events to scaffold the web UI. So a job dispatched through this API does
stream frames, and the UUID check and per-viewer consumer isolation above are
real, but the task results inside those frames are invented. Do not read this
stream as evidence that a device was reached.

## MCP tool provider

Not built. No Model Context Protocol server or tool provider exists anywhere in this
codebase today; treat any mention of one as aspirational until this line is removed.

## Web UI

A mockup, all six routes. Five render hardcoded content and issue no network request
at all. The sixth is an SSE job log viewer whose streaming code is real, and it
cannot reach this API for three independent reasons, each enough on its own:

- **It requests the wrong job.** `web/src/views/JobDetails.tsx` sets
  `const jobId = "123"` instead of reading the `:id` that its own `/jobs/:id` route
  declares, so every visit asks for job `"123"`. This endpoint requires a UUID (see
  [the SSE job log stream](#the-sse-job-log-stream) above), so even an authenticated
  version of that request answers `400 {"error":"job id must be a UUID"}`.
- **It calls the wrong address, and nothing forwards.** The URL is hardcoded to
  `http://localhost:8081`. `cmd/controller` listens on `:8080` unless `LISTEN_ADDR`
  overrides it, `web/vite.config.ts` declares no `server.proxy`, and `web/nginx.conf`
  serves static files with no `/api` location at all. Port 8081 belongs to `cmd/demo`,
  which wires the same authentication middleware this router does, so aiming there
  does not help either.
- **`EventSource` cannot authenticate.** Every route under `/api/v1` requires
  `Authorization: Bearer <token>`. An `EventSource` cannot be given request headers:
  its constructor takes a URL and a `withCredentials` flag, nothing more. This API
  reads a token from nowhere else either, no cookie and no query parameter, so the
  request arrives anonymous and is rejected 401 before any handler runs. That 401
  carries no `Access-Control-Allow-Origin` header, so a browser will not release the
  response to the page: the failure surfaces as a bare error event, not as the 401 it
  was. The `Access-Control-Allow-Origin: *` that the log handler does set is set
  inside the handler, which an unauthenticated request never reaches.

The endpoint itself is sound: an authenticated `GET` with a real job UUID answers
`200 text/event-stream` and an `event: init` frame. Nothing the UI sends gets past
any of the three problems above. Treat the UI as `experimental` and prefer the API
directly, or the CLI, for anything that matters today.

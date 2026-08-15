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

The route table is generated documentation, not hand-maintained prose:
[the OpenAPI document](reference/schemas/openapi.json) (also served live at
`/api/v1/openapi.json`) and [the CLI reference](reference/cli.md) share the same
"generate from one source" discipline every other reference page in this set does.
Both `cmd/controller`'s real router and the generated OpenAPI document build from
the same `internal/apispec` endpoint values, so the method, pattern, required scope,
and hypermedia relation cannot drift: the router asks each endpoint for its own
`Route`, which copies all four fields off the exact value the document is rendered
from.

**Set membership is enforced too, and that is a change from what this book used to
say.** It previously warned that nothing compared the two sets, so an endpoint added
to the spec and never mounted would be advertised and 404. It is now checked in both
directions at startup: the controller pairs every declared endpoint with a handler by
name and refuses to start if any endpoint has none, or if any handler is registered
under a name no endpoint declares. A route the document lists is therefore a route
the server serves.

Read the OpenAPI document for the full list rather than a table here, which is
exactly the drift this section is describing. What follows is the shape of it:
runbooks and templates (the saved definitions of what to run, where, and how), jobs
and their live log streams, the inventory (devices, groups and the shareable
Inventories above them), the access surface (organizations, teams, users, contacts
and role bindings), the activity stream, and operator announcements.

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

**Which permission scope does this route require?** Every route's `Scope` (in the
generated document) is checked against the calling identity's JWT `Scopes` claim. A caller with
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
request returns, so the two can never disagree. Seven relation names exist today:

| Relation | Meaning |
|---|---|
| `self` | This resource's own canonical URL. |
| `collection` | The collection this resource belongs to. |
| `create` | Create a new member of this collection. |
| `update` | Modify this resource in place. |
| `delete` | Retire this resource. |
| `copy` | Duplicate this resource. Its own relation rather than `create`, because a relation is unique per resource and the two would otherwise be indistinguishable on a page that offers both. |
| `execute` | Run this resource: launch a template, relaunch a job. |
| `logs` | Stream this resource's live progress. |

## Pagination

Every list route is keyset paged, never offset. An offset over a table being written
to skips and repeats rows, which on an inventory list means a device silently missing
from one page while another is shown twice. Pass the previous page's cursor back as
`?after=`: a job id for the job list, since job ids are UUIDv7 and therefore
time-ordered, and the last numeric id for everything keyed on one. The page size is
the server's to cap, so `?limit=100000` is not a supported way to ask it to hold an
entire fleet in memory.

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

Sign in at `/ui/login` with an email and password, or by pasting a token this control
plane already accepts. Either is exchanged for a server-side session cookie, and both
resolve to the same one notion of who a caller is: a password is verified locally and
its authority derived from the RoleBindings on its teams, while a token is validated
by the same evaluator the `Authorization: Bearer` path uses and carries its authority
as claims. Create the first account with `controller bootstrap-admin --email
you@example.com` on the host. Token paste remains the break-glass route, and the only
route for a deployment holding no local credentials.

That cookie is also what makes the SSE job log viewer work. An `EventSource` cannot
be given request headers -- its constructor takes a URL and a `withCredentials` flag,
nothing more -- so while `Authorization: Bearer` was the only credential this API
accepted, no browser could reach the stream at all. `/api/v1` now accepts a Bearer
header **or** a session cookie, resolved by one middleware into one identity, and the
log viewer at `/ui/jobs/{id}/logs` connects with the cookie alone.

Which views do anything is decided per view and stated on the page rather than in this
document. The ones worth calling out:

| View | State | Notes |
|---|---|---|
| Dashboard | Real | Job-outcome counts over the most recent 200 dispatches, as a chart and as an equivalent table. |
| Templates | Real | The saved definitions this platform launches: create, edit, copy, delete, and launch. What a template runs is chosen from a picker over everything the deployment can resolve, runbooks and playbooks in one list, never typed; the kind is derived from the choice, and a definition that does not resolve is refused at create rather than failing later as a failed job. The launch form renders only the fields the template being launched actually opened, plus its survey, because a control whose value is then ignored is an affordance that does nothing. |
| Inventories | Real | Full create, read, update and retire against the device repository. Device *properties* are deliberately not editable: they decrypt to real secrets, and the masking ruleset belongs to an unbuilt phase. |
| Jobs | Real | List and open. No launch form: a job is launched from a Template. No cancel and no delete either -- there is no `job:write` scope and no cancellation path in this build, so no button is offered for one. |
| Runbooks | Real | Read-only catalog. Runbooks come from `RUNBOOK_DIR` and from GitOps; a write path here would be a second, unversioned way to change what this platform executes. Its one action saves a runbook as a template rather than launching it, so the catalog stays a catalog and launching has one home. |
| Organizations, Teams, Users, Access | Real | The tenancy and RBAC surface, each with an Access section on the record itself. Organizations and Teams also carry a Contacts section. |
| Contacts | Real | Who is accountable for a tenant or a team, and how to reach them. The owner is chosen once from a single control listing organizations and teams together, so "both owners" and "neither owner" are not states a form can submit. |
| Activity | Real | Who changed which managed object, and when, written by a decorator over the store rather than by calls inside handlers. |
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

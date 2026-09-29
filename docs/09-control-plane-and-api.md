---
status: beta
---

# Control plane and API

This book covers the Walk-tier control plane's HTTP API: what it looks like today,
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
time-ordered, a schedule id for the schedule list, for the same reason, and the last
numeric id for everything keyed on one. The page size is
the server's to cap, so `?limit=100000` is not a supported way to ask it to hold an
entire fleet in memory.

## Dispatch: from a launch to a result

Every way of starting work ends in one path: `POST /templates/{id}/launch`,
`POST /templates/{id}/check` (the same launch, forced to a check), `POST /jobs/{id}/relaunch`,
a schedule firing, and the web UI's launch form. This section follows a job along it.

### The launch answers before anything runs

A launch resolves the template, stores a `pending` job, and publishes a `job.requested`
event. It answers `202 Accepted` with a `Location` header naming the job and a body of
`status`, `job_id`, `ignored_fields` (every value supplied but not applied, always present,
empty when nothing was refused) and `_links`. Nothing has reached a device yet, so a launch
cannot report a problem found later, such as a credential that cannot be resolved: that
shows up as a failed job. Each launch creates a new job; there is no idempotency key, so
retrying a launch whose answer was lost starts a second job.

### Fan-out: one row per device, whether it runs or not

A Controller replica claims the job (`pending` to `fanning_out`), resolves its bound
credentials once for the whole job, and walks the job's inventory. Only the credentials'
ids are stored on the job; their values are resolved here and ride on each device's
dispatch. Every device the fan-out considers gets a task row with an `outcome`:

- `dispatched`: handed to a Runner.
- `skipped`, with a `reason`: the device is not `active` (a check also admits
  `simulate-locked`; see [Running in production](10-running-in-production.md#how-a-device-gets-its-state)),
  lacks a capability the definition needs, or has no `host` property.
- `failed`: its dispatch could not be built or published.

If the fan-out cannot run at all (the definition or the inventory is missing, or a bound
credential cannot be resolved), the job ends `failed` with a `failure_reason` and no task
rows. Otherwise it moves to `running`, or straight to `completed` if no device was
dispatched. A job stuck in `fanning_out` for ten minutes, because the replica doing it died,
is picked up again by the leader.

### `forks`: how many devices run at once

With no `forks` launch field, every admitted device is dispatched during the fan-out, and
how many run at once is limited only by your Runners. With `forks: N`, at most N of the job's
devices run at once:

- The fan-out admits each device as above, but records an admitted device as `queued`
  instead of dispatching it, then dispatches the first N.
- Each time a device's result comes back, the next queued device is dispatched in its
  place. The leader also checks every minute for a job with room and queued devices, in case
  the replica that recorded a result stopped before dispatching the next one.
- A queued device is admitted again when its turn comes. One that left the inventory is
  `failed`, and one no longer `active` is `skipped`, each with its reason.
- Cancelling the job skips every queued device, with the reason "canceled before this
  device's turn".
- A launch that supplies credential inputs asked for at launch cannot also set `forks`,
  and is refused (422). Those inputs are held only while the job fans out and are never
  stored, so devices dispatched later could not have them. Bind a stored credential, or
  leave `forks` unset.

The limit holds across Controller replicas: each running device holds a numbered place, and
the database refuses a second device in the same place. Two things can still make it
inexact, and both are stated rather than hidden:

- **A lost result holds its place.** A device whose result never arrives (a Runner with no
  `RUNNER_WAL_DIR` that died before publishing, say) keeps its place, so the job's
  remaining devices wait. Such a job already never completes; with `forks` set, it also
  stops starting devices.
- **A failed run frees its place early.** A device whose run failed reports that failure
  and is then delivered again (see below). Its place is freed at the first report, so its
  retries can run beside the next device, and the job briefly has more than N running.

`forks` applies to both kinds. The `playbook` kind also passes it to `ansible-playbook`, but
each dispatch there is one host, so it is the window that limits how many run at once.

### What a Runner does with a dispatch

Each device's dispatch is published on its own subject, `pleiades.jobs.dispatch.<device>`,
or `pleiades.jobs.check.<device>` for a check, where only a Runner that understands checks
reads it, or `pleiades.jobs.rollback.<device>` for a rollback, where only a Runner that
understands rollbacks reads it. A Runner takes a per-device lease first, so two runs never touch one device at
once, and a dispatch for a busy device waits and is delivered again. It runs a `runbook` kind
through the native adapter and a `playbook` kind through the Ansible adapter, then publishes
the device's result, which the Controller records on the task as `result` (`succeeded` or
`failed`), `result_reason`, `unchecked` and `finished_at`. A Runner with `RUNNER_WAL_DIR`
set writes each result to disk before acknowledging the dispatch, so a result survives the
Runner dying before it could publish; without one, that result is lost.

**A run that fails is run again.** Any failed task fails the device's run, and a failed run
is delivered again, with backoff, until it has been delivered five times, and is then
dead-lettered. Each delivery runs the whole definition from the start, including tasks that
already changed something, so a definition that is not safe to repeat can make its change
more than once. (A single command sent over SSH is still never resent; see
[Running in production](10-running-in-production.md#a-command-is-never-retried-once-sent).)
This is planned to become a stated policy that is off by default.

### How a job ends

When every dispatched device has reported, the job moves from `running` to `completed`.
**`completed` means every device was handled, not that every device succeeded**: there is
no partial-failure state, and a job whose devices all failed is still `completed`. Read each
task's `outcome` and `result` for what happened on each device. `failed` means the fan-out
itself could not run, and `canceled` means somebody stopped the job.

`POST /jobs/{id}/cancel` (`runbook:execute`) records the cancellation first, then signals
the Runners. The fan-out stops at the next device, and at most one more device may already
have received its dispatch. A result that arrives afterwards does not bring the job back.
Cancelling a finished job answers 409.

Nothing times out a `running` job. With no Runner running, dispatches wait on the stream
for up to its retention (seven days with the default outage budget) and the job stays
`running` until one appears. A check job, once finished, also reports `check_complete`: true
only if every device was dispatched, succeeded, and left no task unchecked.

### Watching a job

`GET /jobs/{id}` (`job:read`) returns the job's `state`, `mode` (`execute` or `check`), the
`dispatched`, `skipped` and `failed` counts, `failure_reason`, and one entry per device
with its `outcome`, `reason`, `result`, `result_reason`, `unchecked` and `finished_at`.
`GET /jobs` pages through jobs without their devices. The live log stream is below.

## Schedules

A schedule is an RFC 5545 recurrence attached to something launchable: when automation
runs without somebody pressing launch. The full request and response shapes are in the
generated document; what follows is the part that is not obvious from a schema.

### What a schedule can point at

Two sorts of thing today, and one field names either:

| `unified_job_template_type` | What a run of it is | What it does |
|---|---|---|
| `job_template` | a job | Runs the template, exactly as pressing Launch does. |
| `project` | a project update | Fetches the project's source, exactly as pressing Sync does. |

`unified_job_template` is the id, and it is one id space across both: a schedule holds one
reference and needs to know nothing about which sort it points at. The names are AWX's own,
so an imported AWX schedule resolves without translation.

A schedule response also carries `unified_job_template_name` for rendering, and
`unified_job_template_type` above. Note the near-collision: on an occurrence,
`unified_job_type` says what sort of RUN was started (`job` or `project_update`), which is
a different question from what sort of thing was pointed at.

The older field `template`, which named a template id, still works on a write and is
deprecated. It resolves to that template's `unified_job_template`. Sending both fields
naming different things is refused rather than resolved one way, since a client updated by
halves must not silently repoint a schedule. Responses no longer carry `template`: a field
that would be absent for a project sync is one every reader has to special-case.

### Writing a schedule needs permission to launch what it launches

`schedule:write` decides who may arrange for things to run. It does not decide what may be
run: that is the scope the thing's own sort declares, `runbook:execute` for a job template
and `project:write` for a project, and the write path requires it as well.

This closes a real hole rather than adding ceremony. Before it, `schedule:write` alone was
enough, so a token that could not run a template by hand could arrange for it to run
repeatedly and unattended, attributed to the scheduler rather than to whoever arranged it.
A caller missing the scope gets a `403` naming which one, and the Schedules form offers only
things the person looking at it could launch themselves.

### A schedule that could never run is refused when you save it

Each sort of launchable gets to object before the schedule is stored, because the
alternative is a schedule that saves cleanly and then fails at whatever hour it was set
for, on a page nobody has open. Refused with a `400` naming the field:

- a template bound to a credential whose type prompts for an input at launch, and a saved
  configuration answering a survey password or a survey file: neither value is stored, so
  there is nobody to ask and nothing to replay;
- a saved configuration belonging to a different template, which carries answers that only
  mean something against the template whose questions produced them;
- a saved configuration on a project sync, which takes no launch-time overrides at all: what
  it fetches is the project's own record, so accepting one would be storing values that
  silently never applied;
- a project with no source to fetch.

### The object is deliberately not one `rrule` string

AWX folds `DTSTART` and `TZID` into the rule. Here they are three separate fields:

| Field | Why it is its own field |
|---|---|
| `rrule` | The recurrence, and nothing else. |
| `timezone` | An IANA name. It decides what the rule *means*, so it must be readable and queryable without parsing the rule. |
| `dtstart` | The anchor, RFC 3339. RFC 5545 takes from it every field the rule leaves unspecified, including the time of day, so it is part of the recurrence rather than a creation timestamp. |

`exclusions` is a list of `EXRULE` recurrences and `EXDATE` instants subtracted from
the rule. `dtend` bounds the schedule from outside the rule: an operator saying "stop after then"
without editing what an author wrote.

### The grammar is a bounded subset, refused at the write

Accepted: `FREQ` (`MINUTELY` through `YEARLY`), `INTERVAL`, `COUNT`, `UNTIL`, `WKST`,
`BYDAY` (including ordinals such as `-1FR`), `BYMONTHDAY`, `BYMONTH`, `BYHOUR`,
`BYMINUTE`, `BYSETPOS`.

Refused with a `400`: `SECONDLY`, `BYWEEKNO`, `BYYEARDAY`, `BYSECOND`, `RDATE`,
`INTERVAL=0`, a `COUNT` above the server's cap, and any rule that names a date which
never occurs (`FREQ=YEARLY;BYMONTH=2;BYMONTHDAY=30` parses cleanly and would otherwise
become a schedule that silently never fires).

The refusal happens when the schedule is saved, never when it runs. A recurrence is
operator-supplied input that the scheduler later expands inside its own loop, so an
unbounded rule reaching that loop is not one broken schedule but a stall affecting
every schedule in the deployment.

The error names the field at fault (`rrule`, `exclusions`, `timezone`, `dtstart`,
`dtend`), so a form can attach it to the control it came from.

### Preview and zoneinfo

`POST /schedules/preview` expands a recurrence without storing anything and returns
occurrences in **both** the named zone and UTC:

```json
{
  "timezone": "America/New_York",
  "occurrences": [
    {"local": "2024-03-08T09:00:00-05:00", "utc": "2024-03-08T14:00:00Z"},
    {"local": "2024-03-11T09:00:00-04:00", "utc": "2024-03-11T13:00:00Z"}
  ]
}
```

Both readings are returned because either alone hides the case worth checking. Across
a daylight saving transition a daily rule keeps its local hour and its UTC hour moves,
so the interval between two runs is 23 or 25 hours rather than 24. It is a `POST`
because a rule, its exclusions and its anchor do not fit unambiguously in a query
string; it writes nothing and requires only `schedule:read`.

`GET /zoneinfo` lists every zone a schedule may name, plus a shorter `common` list to
offer first. The list is generated from the same time zone archive the binary embeds,
so a zone it offers is a zone the server can load.

### Occurrences: what ran, and what did not

`GET /schedules/{id}/occurrences` returns one row per occurrence, most recent first,
with an `outcome`:

| `outcome` | Meaning |
|---|---|
| `fired` | A run was started. `job` names it and `unified_job_type` says which sort it is: a job's own id, or a project sync attempt's. |
| `skipped` | It did not run. `reason` says why: `missed_window`, `missed_window_truncated`, `launch_failed`, or `already_running`. |
| `claimed` | A controller won the right to run this occurrence and stopped before recording what happened. |

An occurrence that did not run is a **row**, not a gap. A missing row and a row reading
`skipped`/`missed_window` describe the same absence of a job and are completely
different answers, and only the second is auditable.

A `claimed` row is shown rather than hidden because only a person can safely resolve
it: re-running risks doing the work twice, abandoning it risks not doing it at all.

`already_running` is the one skip that is nobody's mistake: the thing the schedule launches
was still running from an earlier occurrence, which an hourly sync of a large repository will
eventually hit. It is a skip rather than a failure deliberately. A failure would be retried
and would keep failing for as long as the first run lasts, so a slow clone would produce a
row of failures; skipping advances the schedule and lets the next occurrence try.

### Missed runs are coalesced

If occurrences pass while no controller is running, exactly one run happens on
recovery, the most recent missed occurrence, and every earlier one is recorded as
`skipped`/`missed_window`. An hourly job that missed four hours launches once, not
four times.

A backlog larger than the server's cap collapses into one row with
`missed_window_truncated` and a `suppressed_count`, rather than writing half a million
rows and turning a recovery into an outage of its own. The count is there so the
truncation is visible: a silent cap would make an incomplete history look complete.

### Firing exactly once

Exactly one controller replica evaluates due schedules at a time, elected through the
same leader-election primitive the rest of the control plane uses. That bounds query
load; it is **not** what makes a schedule fire once. The lease is short and carries no
fencing token, so during a failover two replicas can briefly both believe they lead.

What guarantees single firing is a unique database index on the pair
(schedule, occurrence time). The occurrence is claimed by an insert *before* anything
is launched, so a second claimant loses on a constraint rather than on timing.

### Deleting something a schedule uses

`DELETE /templates/{id}` and `DELETE /projects/{id}` both answer `409` while any schedule
still launches the thing, naming the reason. A schedule is not a part of a template or a
project the way a survey or a sync history is: it is an independent object somebody created
and can see in its own list, so removing what it launches would silently stop automation
that is relied on.

Delete the schedule, or point it at something else, first. Disabling it is not enough and
the refusal used to say it was: a disabled schedule still holds the reference, so following
that advice produced the same `409`.

### What cannot be scheduled

A template bound to a credential whose type prompts for an input at launch is refused,
and so is a saved configuration answering a survey password or a survey file. Neither
value is one this platform will replay unattended, on every occurrence, under nobody's
decision, which is a larger version of the concern that already stops a relaunch from
doing it once. A prompted credential input is never stored at all; a secret survey
answer is stored encrypted, and the refusal is about replaying it rather than about
reading it back.

Note the scope: what is refused is a schedule whose saved configuration ANSWERS such a
question. A template carrying a password or file question can still be scheduled, so
long as the configuration the schedule holds leaves that answer out.

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
  timestamp, status, host, task label, and a message. Both Runner adapters mask what
  they publish: every secret the Controller attached to the device's dispatch, every
  value a bound credential injected, and whatever the run's `register_mask` and
  `secret_mask` marked are replaced before a frame is published. A value none of those
  name is not: a secret survey answer, for one, reaches the stream unmasked if a task
  echoes it. See [Running in production](10-running-in-production.md)'s data handling
  section for what masking covers.
- The stream never terminates on its own; the client closes it.

**The Crawl-tier CLI never publishes here.** `pleiades run` builds its engine on an
in-process bus (`event.NewInProcessBus`, in `cmd/pleiades/run.go`), never opens a
NATS connection, and publishes its per-task status events under
`pleiades.events.workflow.<dag-id>.node.<node-id>`, while this handler only ever
reads `pleiades.jobs.logs.<job-id>`. The two subject spaces do not overlap, and
the two buses never meet.

Three things publish to `pleiades.jobs.logs.<job-id>`, and they differ in what they
mean. A `runner` runs a dispatched job through its native adapter (a runbook) or its
Ansible adapter (a playbook), each against the real device the dispatch names, and
reports what actually happened, so those frames are genuine task results. `cmd/demo`
generates fake Ansible events to scaffold the web UI, and those are not. Read the stream
as real evidence only when a `runner` produced it; the demo binary exists precisely so
the UI can be developed without one.

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

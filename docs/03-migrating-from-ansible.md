---
status: beta
---

# Migrating from Ansible, AWX, and AAP

Ansible support is Pleiades' migration on-ramp, not its destination: a workload lands
unchanged, then converts to native typed collections at its own pace, one playbook at
a time. This book covers what carries over directly, what does not exist yet, and
what the exact vocabulary mapping is, so a migration decision can be made from facts
rather than guesses.

Read [Start here](01-start-here.md) first if you have not: its Implementation status
and Limitations sections are the honest baseline everything below builds on.

## What transfers, and what does not

**Transfers directly, same meaning:** `hosts:`, `block:`, `register:`,
`when:` (list form ANDs, same as Ansible), `pretasks:`/`tasks:`/`posttasks:` (Ansible's
`pre_tasks:`/`tasks:`/`post_tasks:`, same three-phase shape and ordering).

**Parses and validates, but never runs:** `rescue:` and `always:`. The schema accepts
both, `pleiades validate` checks both, and `pleiades run` prints both in the plan, but
the executor never reaches them. A `block:` whose child fails runs no rescue handler:
the run just fails. `always:` is the more dangerous of the two, because a `block:` that
succeeds still prints `run complete` and exits 0 while its `always:` tasks are skipped
in silence, so nothing warns you the cleanup did not happen. Keep cleanup and recovery
steps inside the `block:` itself, in order, until these are implemented.

**Genuinely new, no Ansible equivalent:** `when_or:` (list ORed instead of ANDed),
`when_cel:` (one raw CEL expression), `register_mask:` and `secret_mask:` (per-field
secrecy on a registered result, not just a whole-task `no_log: true`),
`lock_acquisition:` (per-device, per-task locking, not a job-level lock; the CLI's
locking is in-process only and does not exclude a second `pleiades run`, see
[Running in production](10-running-in-production.md#locking)), `parallel:`
(native fan-out/join).

**Does not exist yet, and a runbook cannot express it:** Jinja templating anywhere in
`params:` (a runbook's params map is always a literal value), `loop:`/`with_items:`,
`handlers:`/`notify:`, `become:`, `serial:`, `roles:`, `ignore_errors:`,
`changed_when:`/`failed_when:`, `group_vars:`/`host_vars:`, and inventory-level `vars:`
of any kind. See the keyword map below for the complete, itemized list.

None of these are secret gaps. They are the honest distance between "what Ansible
does today" and "what Pleiades does today," and closing them is most of the open
roadmap.

Some of them still convert. `pleiades forge migrate-playbook` (next section) unrolls a
loop over a list the playbook fixes and writes in a variable the playbook defines once,
so the runbook it writes holds only literal values; what it cannot do that way, it
refuses by name.

## Converting a playbook: `pleiades forge migrate-playbook`

```bash
pleiades forge migrate-playbook site.yml              # writes runbooks/site.yaml
pleiades forge migrate-playbook site.yml --json       # the same report, as JSON
pleiades validate                                     # then check the result
```

The command reads a playbook, and every `import_tasks` and `vars_files` file it names
inside the playbook's own directory, and writes one native runbook per run of plays on
the same `hosts:` into `--out` (default `runbooks/`). It never runs anything, never
contacts a device, and never overwrites a file unless `--force` is given. Beside the
runbooks it prints a **migration report**: every construct that did not convert
cleanly, once each, with the playbook line and column it came from and the runbook line
it landed on.

Each finding has an **outcome**:

- **converted**: the runbook says what the playbook said.
- **info**: dropped or resolved with no effect on a run (a `debug` task, a resolved
  variable).
- **review**: converted or dropped with a bounded difference a person should read
  (`notify` dropped, `become` dropped, a condition reading a registered result).
- **blocked**: not converted. The task becomes an unrunnable placeholder and the runbook
  cannot run until a person resolves it.

The rule behind every drop is that a construct is only ever dropped when its absence
makes the task do less or fail sooner. Anything that would make a converted task do more,
run somewhere else, or expose a value is **blocked** instead. A value that looks secret,
by its variable's name or its shape, and any vault-encrypted value, is never copied into
a runbook or printed in the report; the report names things and points at lines, and
never prints a value from the playbook.

**An incomplete conversion cannot run, on either tier.** When anything is blocked, the
runbook is written as `<name>.incomplete.yaml`, its first task is a guard, and each
blocked task is a placeholder calling `ansible.unconverted.<module>`, a namespace nothing
can register. `pleiades validate` and `pleiades run` refuse it and name each one, and a
Runner validates every runbook it is dispatched before running a task, so the same
refusal holds in the Walk tier. Placeholders carry none of the original task's
arguments, since those can hold secrets; the report links each one back to its line in
the playbook. To finish a conversion, resolve each blocked finding, delete the guard,
and rename the file to `<name>.yaml`.

The command exits 0 when nothing needs a person, and 3 when anything is a review or
blocked, so a script cannot mistake an incomplete conversion for a finished one.

**What converts and how:**

- **Modules** are mapped by the converter's own tables, generated into
  [Ansible module conversions](reference/ansible-modules.md): which native method each
  module and state becomes, which arguments map, which are dropped and which block. A
  module not on that page is blocked with `module.unmapped`. Each converted task gets a
  **class**: *asserted* (a desired state the method compares first), *computed*,
  *imperative* (a command, whose effect is known only by running it) or *observe* (a
  read), declared by the table rather than guessed.
- **Variables** resolve only when the playbook gives one exactly one literal value, and
  each resolution is listed in the report by name and line, never by value. A fact, a
  magic variable, a `-e` extra variable, or anything set at run time blocks the task that
  reads it.
- **Loops** over a list the playbook fixes become one task per item. A loop over a list
  known only at run time blocks.
- **Conditions** in a bounded Jinja subset (comparisons, `and`/`or`/`not`, `in`,
  `is defined`, and the `bool`, `int` and `length` filters) become CEL. A condition over
  a registered result holds only when it holds on every device the earlier task ran on,
  because a native condition is evaluated once per task, not once per host; the report
  asks you to confirm each one.
- **`import_tasks`** is inlined as a block. `include_tasks`, roles, `import_playbook`
  and `template` are blocked.
- **`meta: reset_connection`** becomes `pleiades.builtin.connection.reset`, which closes
  the SSH connection a run keeps open to the device so the next task logs in again, as
  Ansible's does. `flush_handlers`, `noop`, `refresh_inventory`, `clear_facts` and
  `clear_host_errors` are dropped with an info finding; any other `meta` is blocked.
  Connections persist between a device's tasks by default, like Ansible's
  `ControlPersist`; see
  [Connection persistence](10-running-in-production.md#connection-persistence).

**The report is a worklist for an editor too.** `--json` prints the same model the text
view renders, described by the generated
[migration report schema](reference/schemas/migration-report.json). Every finding has a
stable `code` from a closed set (listed on the
[module conversions](reference/ansible-modules.md#finding-codes) page), its playbook
position `at`, and its runbook position `emitted`, so a finding can be shown on the
runbook line it concerns and linked to the source task.

**How fast a converted runbook runs.** [Performance compared with Ansible](15-performance.md)
runs one playbook and its conversion on 1 to 200 hosts. The runbook finished 24 to 41 times
sooner, and at 200 hosts it used about 57 times less CPU on the machine running it.

## Worked example: a real playbook and runbook, side by side

[`examples/upgrade_ios/`](../examples/upgrade_ios/) has the same Cisco IOS-XE upgrade
written twice: once as a real Ansible playbook, once as a Pleiades runbook (in both
its explicit and module-as-key-sugar forms). It is the best single artifact for
seeing every point in this book applied to one real scenario at once, including the
exact `pleiades validate` output the runbook produces today (declared, not yet
implemented, the same honest refusal [Start here](01-start-here.md) describes).

## Playbook to runbook keyword map

| Ansible playbook key | Pleiades runbook key | Notes | What `migrate-playbook` does |
|---|---|---|---|
| `hosts:` | `hosts:` | Same meaning: a default target. A task's own `params.target` (or module-as-key sugar's bare `target:`) still wins when set. | Kept: one device name or tag. `all` and `localhost` are reviewed; a pattern, a list or a template is blocked. |
| `pre_tasks:` | `pretasks:` | Same phase, no underscore. | Converted. |
| `tasks:` | `tasks:` | Same. | Converted. |
| `post_tasks:` | `posttasks:` | Same phase, no underscore. | Converted. |
| `block:` | `block:` | Same grouping. | Converted; a `when:` on it is pushed down onto each task inside. |
| `rescue:` | `rescue:` | Accepted and validated; the Crawl-tier executor does not run rescue handlers yet (see [Start here](01-start-here.md)). | Converted, with a review finding: it does not run yet. |
| `always:` | `always:` | Same status as `rescue:` above: accepted, not yet executed. | Converted, with a review finding: it does not run yet. |
| `register:` | `register:` | Same idea: name a result for a later task to read. Addressed as `stat.<name>[<deviceID>].<field>` in `when_cel`, not as a bare Jinja variable. | Kept. |
| `when:` (single or list) | `when:` | A list ANDs, same as Ansible. Pleiades evaluates CEL underneath, not Jinja, but a plain comparison reads identically in both. | Translated to CEL for comparisons, `and`/`or`/`not`, `in`, `is defined` and the `bool`/`int`/`length` filters; anything else is blocked. |
| none | `when_or:` | New. A list ORed instead of ANDed. | - |
| none | `when_cel:` | New. One raw CEL expression, for a condition `when`/`when_or` cannot express. | - |
| none | `register_mask:` | New. Masks a field of this task's own registered result the instant it registers. | - |
| none | `secret_mask:` | New. Retroactively masks a named, already-registered result's fields. | - |
| none | `lock_acquisition:` | New. `per_device_as_reached` (default) or `all_at_plan_time`. | - |
| none | `parallel:` | New. Native fan-out/join; mutually exclusive with `fqcn:`/`block:`. | - |
| `name:` | `name:` | Same, free-form label. | Kept. |
| module name as a task key (e.g. `ansible.builtin.copy:`) | `fqcn:` + `params:`, or module-as-key sugar | See [Two ways to write a task](../examples/upgrade_ios/README.md#two-ways-to-write-a-pleiades-task). | Mapped by the [module tables](reference/ansible-modules.md); a module they do not list is blocked. |
| `vars:` (play or task level) | *(not supported)* | No template rendering exists; see [What transfers](#what-transfers-and-what-does-not). | A variable with exactly one literal value is written into the runbook, and listed in the report; anything else blocks the tasks that read it. |
| `{{ jinja }}` anywhere in a module's args | *(not supported)* | `params:` is always a literal value. | Resolved when every variable it reads has one literal value, with a small set of filters; otherwise blocked. |
| `loop:` / `with_items:` | *(not supported)* | A task runs once per its target device, never once per list item. | Unrolled into one task per item when the list is fixed in the playbook; a list known only at run time is blocked. |
| `handlers:` / `notify:` | *(not supported)* | No handler mechanism exists. | Dropped, with review findings: a handler is not converted, so run its task explicitly where it was notified. |
| `tags:` | `tags:` | Same meaning, on a runbook (a play's tags), a block or a task, and `pleiades run --tags`/`--skip-tags` select by them with Ansible's own rule, `always`, `never`, `all`, `tagged` and `untagged` included. A task tagged `never` does not run unless a run names one of its tags. A tag no task carries is refused rather than silently matching nothing. The Controller does not take a tag filter yet, so a dispatched job runs everything but `never` tasks. | Kept, except `all`, `tagged`, `untagged` or a template, which block. |
| `become:` / `become_user:` | *(not supported)* | No privilege-escalation directive; a Collection method's own `ExecutionContext.RequiresElevation` states this instead, as data, not as a runbook key. | `become` is dropped with a review finding; a `become_user` other than root is blocked. |
| `serial:` | *(not supported)* | No batched-rollout control. | Blocked: the runbook would change every device at once. |
| `roles:` | *(not supported)* | No role mechanism yet; it is on the open roadmap. | Blocked. |
| `ignore_errors:` | *(not supported)* | A failed task fails its run; no per-task override. | Dropped with a review finding: a failure ends the run. |
| `changed_when:` / `failed_when:` | *(not supported)* | A Collection method's own `Result.Changed` is the only changed signal; no runbook-level override. Read the change forward with `when_cel` instead, the way [the worked example](../examples/upgrade_ios/README.md) does for its checksum gate. | `changed_when` is dropped; `failed_when: false` is treated as `ignore_errors`; any other `failed_when` is blocked. |
| `group_vars/` / `host_vars/` directories | *(not supported)* | No inventory-level variable layering of any kind. | Not read, with a review finding: put per-device values in inventory properties. |

## Borrowed vocabulary: exact semantics

A term reading the same in both files does not always mean the same guarantee
underneath. Where it matters:

- **`when:`** is CEL, not Jinja, underneath. A simple equality or substring check
  reads identically; anything relying on a Jinja filter does not port and needs
  rewriting against CEL (or `when_cel:` for anything past a bare comparison).
- **`register:`** in Ansible is read back with `{{ result.stdout }}` templating. In
  Pleiades there is no templating at all: a later task's `when_cel:` reads it
  directly (`stat.result['deviceID'].stdout`), and nothing else can reference it.
- **`block:` conditions.** Ansible's block-level `when:` gates every task inside at
  once. Pleiades evaluates a task's own condition once per task; a block task's own
  condition is never walked by the executor. The equivalent "skip everything" effect
  today means repeating the same `when_cel:` on every task inside the block.
- **Credentials.** Ansible reads `{{ vault_* }}` variables from an `ansible-vault`
  file referenced from `vars:`. Pleiades has no vars or vault mechanism: `pleiades
  add-credential <device> --username <user>` prompts for a secret once and writes it
  to a local AES-256-GCM encrypted store. Neither a runbook nor `inventory.yaml` ever
  contains a password.
- **`no_log: true`** is whole-task secrecy: nothing about that task appears in output.
  `register_mask:`/`secret_mask:` are field-level and apply to the registered
  *result*, not the task's own invocation; a password field is scrubbed from every
  later printed line of the run, not just the task that read it.

## Module to FQCN map

**The modules the converter maps, and exactly how,** are on the generated
[Ansible module conversions](reference/ansible-modules.md) page: which native method
each module and state becomes, which arguments map, which are dropped and which block.
It is built from the converter's own tables, so it cannot drift from what
`migrate-playbook` does. The native side of each method, including the capabilities its
manifest declares, is the generated [module catalog](reference/modules/index.md).

Every native method on either page is `implemented` except `file.template`,
`net.junos.config` and `net.eos.config`, which are `declared`: registered, validated,
and refused at call time with an explicit "not implemented yet" error rather than a
silent no-op. See [Implementation status](reference/implementation-status.md).

**The Capability column below is documentation, not a check.** It records the
capability each method's manifest declares it needs. Nothing compares that to your
inventory: `pleiades validate` checks capabilities only for the two legacy action names
`ssh_exec` and `ios_backup`. Point any FQCN at a device that lacks the listed capability
and `validate` still reports no issues; the mismatch surfaces during the run instead.
See [Start here](01-start-here.md#implementation-status).

The rest of this section covers the native methods whose Ansible counterparts the
converter does not map yet, so a task using one is blocked and written by hand, and
where each kind of method runs.

**Networking, by hand**

| Ansible | Pleiades FQCN | Capability |
|---|---|---|
| `junipernetworks.junos.junos_config` | `net.junos.config` (declared) | `JunosCapable` |
| `arista.eos.eos_config` | `net.eos.config` (declared) | `AristaEOSCapable` |
| `cisco.dnac.*_info` | `net.catalyst.device_facts`, `.reachability`, `.site_facts`, `.tag_facts` | `CatalystAPICapable` |

The four `net.catalyst.*` methods are controller-side and read-only, against Cisco
Catalyst Center's REST API, verified against Cisco's public DevNet sandbox.

**Extended infrastructure**

| Ansible | Pleiades FQCN | Capability | Intended side (not enforced) |
|---|---|---|---|
| `ansible.posix.firewalld` with `port:` or a zone form | `fw.firewalld.allow`, `.deny`, `.reload` | `FirewalldCapable` | target side |
| `ansible.windows.win_feature` | `win.feature.install`, `.remove` | `WindowsFeatureCapable` | target side |
| `community.general.archive` | `archive.create` | `POSIXFileSystemCapable` | target side |
| `community.docker.docker_container` | `container.docker.run`, `.stop`, `.remove` | `DockerCapable` | hybrid |
| `amazon.aws.ec2_instance` | `cloud.aws.ec2.create`, `.terminate` | `AWSAPICapable` | controller side |
| `amazon.aws.s3_bucket` | `cloud.aws.s3.create_bucket`, `.delete_bucket` | `AWSAPICapable` | controller side |

The two cloud entries are the clearest controller-side cases: they call an API, not
a device over SSH, and their manifests declare no transport at all.

**The "Intended side" column is hand-written prose, not a manifest field.** It
records where each method is meant to run. Nothing in the code stores that value,
reads it, or checks it. `pkg/collection.ExecutionContext` is the only manifest field
that sounds like it would, and it holds exactly one boolean, `RequiresElevation`.
Two places in the shipping code read that boolean, and both are documentation
renderers: `pleiades doc` and the generated
[module catalog](reference/modules/index.md) pages. The dispatcher does not read it,
and neither does `pleiades validate`. The table above proves the point:
`fw.firewalld.*` ("target side") and `container.docker.*` ("hybrid") carry the same
`executionContext` value, the same transport, and the same status, and differ only
in a capability name. So do `archive.create` ("target side") and `archive.extract`
(which the converter maps from `ansible.builtin.unarchive`, "hybrid"). Identical manifests cannot produce two different column values, because
the column is not generated from them.

**Execution side is decided at run time, from one thing only: whether the task ends
up with a target.** `TaskTarget` (`internal/engine/action.go`) takes the task's own
`params.target` when it is a non-empty string, and otherwise falls back to the
runbook's `hosts:`. An empty result means the task runs once, against no device. A
non-empty one means it runs once per device that target resolves to. With two hosts
tagged `webtier`, a task that sets no target of its own runs twice under
`hosts: webtier` (`blast radius: 2 devices`) and once, against no device, when
`hosts:` is absent (`blast radius: 0 devices`).

**So a runbook that sets `hosts:` cannot mark one task controller-side.** A task has
no `delegate_to` key, no `context` key, and no `run_once` key. Adding one is a build
error, not a hint: the runbook fails to load with `sets both fqcn: and an
unrecognized key "delegate_to"`. Writing `params.target: ""` does not help either,
because an empty string falls back to `hosts:` exactly like an absent key, so the
task still fans out per device. To keep a task controller-side today, leave `hosts:`
off the runbook and give every target-side task its own `params.target`. Ansible's
`delegate_to: localhost` has no Pleiades equivalent yet.

**Gating and facts**

| Ansible | Pleiades FQCN | Capability | Intended side (not enforced) |
|---|---|---|---|
| `ansible.builtin.uri` | `http.request` | none | controller side |
| `ansible.builtin.wait_for` with `path:` or `search_regex:` | `wait.path`, `wait.search` | `NetworkAddressableCapable` | hybrid |
| `ansible.builtin.set_stats` | `set_metadata` (an engine builtin, not a Collection method) | none | controller side |

`uri` is not converted because the two run in different places: `uri` calls the URL
from the device, and `http.request` calls it from wherever the task runs, so which side
should make the call is a person's decision. The converter maps `wait_for`'s port form
to `pleiades.builtin.wait.port` and `setup` to `facts.gather` itself.

`pleiades.builtin.wait.port` and `set_metadata` are not 1:1 ports of an Ansible
module; they are native to Pleiades, which is why the first lives under a reserved
`pleiades.builtin.` namespace and the second is an engine builtin reachable by its
bare name rather than a Collection FQCN at all. `wait.path`/`wait.search` have not
been renamed under `pleiades.builtin.` yet, an intentional inconsistency rather than
an oversight.

**Not yet in the catalog at all:** `ansible.builtin.assert`, `ansible.builtin.fail`,
`ansible.builtin.pause`, `ansible.builtin.git`, `ansible.builtin.get_url`,
`ansible.builtin.stat`, `ansible.builtin.cron`, and anything from a Galaxy collection not listed
above (`ansible.posix.sysctl` among them). The
converter drops `ansible.builtin.debug`, which only prints, with an info finding. A missing row here is either a gap to fill
in a future phase, or a case for `forge new-collection` to add it yourself; see
[Extending Pleiades](reference/index.md).

## AWX / AAP object map

Pleiades' Walk tier (a Controller, a Runner, and NATS; see
[Start here](01-start-here.md#the-crawl-walk-and-run-tiers)) is the layer that
corresponds to AWX at all. The dispatcher, RBAC, and job model are real and tested;
several AWX concepts below have no Pleiades equivalent yet, which this table states
plainly rather than implying a rough match exists.

| AWX / AAP object | Pleiades equivalent | Status |
|---|---|---|
| Job template | A Template, launched via `POST /api/v1/templates/{id}/launch` | `beta`: the API, the dispatcher, and the runner's execution against a real device are all real (see [Start here](01-start-here.md) for credential-handling limits). A template names what to run, the inventory to run it against, the values it runs with, which of those a launch may override, and a survey; a template of kind `playbook` reaches the second execution adapter, which runs an *unconverted* playbook unmodified inside a fresh container. See [Running an unconverted playbook](#running-an-unconverted-playbook) below |
| Inventory | `inventory.yaml`, or a synced inventory via a sync plugin | `beta` (static), `experimental` (sync plugins; only `catalyst_center` exists beyond the built-in `static_yaml`, and it registers as `implemented`, not `declared`: an authenticated, paged REST sync against Cisco Catalyst Center) |
| Credential | A Credential of a declared type, bound to a template | `beta`: types, the injector engine, binding and injection at dispatch are all real. Six AWX types ship under their own namespaces; sixteen more are recognised and not implemented. See [Migrating credentials](#migrating-credentials) below |
| Credential type | A Credential Type, defined as data over the API or imported from an AWX export | `beta`: an AWX export decodes with no translation layer. `env` and `file` injectors run on the Ansible path only |
| Workflow (a DAG of job templates) | A single runbook's own `block`/`parallel` DAG | `experimental`: a runbook is itself a DAG, but chaining multiple independent runbooks the way an AWX workflow chains job templates does not exist |
| Survey | A Survey on a Template, authored from its Survey section | `beta`: AWX's seven question types, character for character, with per-type validation, an authored order, and encrypted answers. One addition AWX has no name for: a `file` question carrying a text file's content, bounded at 32 KiB, treated as secret, and refused if it opens with an interpreter line unless both the deployment and the question permit program content |
| Approval node | none | `design`, not built |
| Schedule (RRULE) | A Schedule, attached to anything launchable | `beta`: RFC 5545 recurrence with exclusion rules, time zones and a preview endpoint, proven against AWX's own recurrence library. Attaches to a job template or a project, as AWX's does; an inventory source, a workflow and a system job do not exist here to attach to. See [Migrating schedules](#migrating-schedules) below |
| Notification template | none | `design`, not built |
| Execution environment | none | `design`, not built. The static binary is the point; see [Start here](01-start-here.md)'s FAQ |
| Instance group | none | `design`, not built. No capacity/admission control exists yet |
| RBAC (organizations, teams, roles) | The control plane's own RBAC | `beta`, real and tested, but the object model has not been checked against AWX's own for parity |

## Migrating schedules

An AWX schedule and a Pleiades schedule are the same object with the parts unpacked.
AWX stores one `rrule` string with `DTSTART` and `TZID` folded inside it; Pleiades
stores the recurrence, the anchor and the zone as three fields, so each is queryable,
editable in a form, and visible without parsing the rule.

Given an AWX schedule:

```json
{
  "name": "nightly patching",
  "rrule": "DTSTART;TZID=America/New_York:20240308T020000 RRULE:FREQ=DAILY;INTERVAL=1",
  "unified_job_template": 42,
  "extra_data": {"limit": "edge-*"}
}
```

the equivalent here is:

```bash
curl -X POST https://controller.example.com/api/v1/schedules \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
        "name":                 "nightly patching",
        "unified_job_template": 42,
        "rrule":                "FREQ=DAILY;INTERVAL=1",
        "timezone":             "America/New_York",
        "dtstart":              "2024-03-08T02:00:00-05:00"
      }'
```

Three mechanical rules cover the conversion:

- **Split the `rrule`.** Everything after `RRULE:` becomes `rrule`. The `TZID` becomes
  `timezone`. The `DTSTART` becomes `dtstart`, as RFC 3339 rather than iCalendar basic
  format.
- **`extra_data` becomes a saved launch configuration.** It is the same bundle this
  platform already stores for relaunch. Create it against the template, then name it as
  the schedule's `saved_config`.
- **`unified_job_template` stays `unified_job_template`.** It means the same thing here: the
  id of the thing being scheduled, in one id space across every sort of thing. The id itself
  will differ, since it is this deployment's rather than the source instance's. A job template
  and a project are both scheduled this way, as in AWX; the sorts AWX has and this platform
  does not yet (an inventory source, a workflow, a system job) have nothing to convert into.

Exclusions are a separate field rather than extra lines in the rule:

```json
{"exclusions": ["FREQ=WEEKLY;BYDAY=SA,SU", "EXDATE:20241225T000000Z"]}
```

### What is refused, and why

The recurrence grammar is a deliberately bounded subset, validated when the schedule is
saved rather than when it runs. `FREQ`, `INTERVAL`, `COUNT`, `UNTIL`, `WKST`, `BYDAY`
(including ordinals such as `-1FR`), `BYMONTHDAY`, `BYMONTH`, `BYHOUR`, `BYMINUTE` and
`BYSETPOS` are accepted. `SECONDLY`, `BYWEEKNO`, `BYYEARDAY`, `BYSECOND` and `RDATE` are
refused, as is any rule that names a date which never occurs — 30 February parses
cleanly and would otherwise become a schedule that silently never fires.

A rule outside the set is refused at the write with a message naming the part, so an
import surfaces the problem while somebody is still looking at it.

### Checking a converted schedule before saving it

`POST /schedules/preview` expands a recurrence without storing anything and returns the
next occurrences in both the named zone and UTC:

```bash
curl -X POST https://controller.example.com/api/v1/schedules/preview \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"rrule":"FREQ=DAILY","timezone":"America/New_York","dtstart":"2024-03-08T09:00:00-05:00"}'
```

Both readings are returned because either alone hides the case worth checking. Across a
daylight saving transition a daily rule keeps its local hour and moves its UTC hour, so
two consecutive runs are 23 or 25 hours apart rather than 24. That is the same behaviour
AWX has — the recurrence engine is tested against vectors generated from `dateutil`, the
library AWX itself schedules on — and the preview is how you confirm it before a
schedule goes live.

`GET /zoneinfo` lists every zone a schedule may name.

### Differences worth knowing before you migrate

- **Missed runs are coalesced, not replayed.** If no controller was running when
  occurrences passed, exactly one run happens on recovery, for the most recent missed
  occurrence, and every earlier one is recorded as `skipped` with the reason
  `missed_window`. An hourly job that missed four hours launches once, not four times,
  and `GET /schedules/{id}/occurrences` shows what did not run.
- **A prompted credential input cannot be scheduled.** The value is never stored, so
  there is nothing to replay unattended. Such a schedule is refused rather than left to
  fail at 3am.
- **A saved survey password is not replayed either**, for the same reason a relaunch
  will not replay one.

## Migrating credentials

This is the part of an AWX migration that decides whether anything runs, because a
customer's playbook reads the environment variables their credential type injects. A
type that does not import is a playbook that does not run.

Check before you commit to a window. Export your credential types from AWX and run:

```bash
curl -sH "Authorization: Bearer $TOKEN" \
  https://awx.example.com/api/v2/credential_types/ > credential_types.json

pleiades import awx-credential-types credential_types.json
```

It runs offline, needs no controller, and reports one line per type saying whether
Pleiades would import it, already ships it, or cannot run it yet and exactly why. It
exits non-zero when something would not import, so it works as a gate in a migration
script rather than only by eye. `--out ./types` writes each importable type as a
document ready to post to `/credential-types`.

Four outcomes, and each means something different for you:

**Importable.** A custom type whose injector document Pleiades understands. Post it and
create your credentials against it. This is the common case for the types your own
team wrote, which are also the ones your playbooks actually depend on.

**Already shipped.** A namespace Pleiades ships itself: `ssh`, `vault`, `net`, `aws`,
`controller` or `hcp_terraform`. Reuse it. Do not recreate it as a custom type: it
would work at first and then silently stop tracking the shipped one when a later
release corrects it.

**Not implemented.** A type AWX manages and Pleiades does not, reported with the
reason. Most are one of three things: AWX builds the type's environment in Python
rather than in an injector document, so there is no document to import (`gce`,
`azure_rm`, `openstack`, `vmware`, `kubernetes_bearer_token`, `terraform`); the
document uses Jinja control flow, which this platform's renderer refuses rather than
passing through as text (`insights`, `rhv`); or the type feeds a subsystem that does
not exist here, such as project source-control sync, webhooks, execution-environment
pulls or content signing (`scm`, `github_token`, `gitlab_token`,
`bitbucket_dc_token`, `registry`, `galaxy_api_token`, `gpg_public_key`,
`satellite6`).

**Refused.** The type is malformed against its own schema, most often an injector
template naming an input the type does not declare. This is a problem in the export
rather than a gap here, and the message names the template and the input.

Two behaviours are worth knowing before you compare a Pleiades run against an AWX one.
An injected variable whose input was left blank is SET to the empty string, not
omitted, which is what AWX does; the `aws` type is the exception, because AWX skips
its session-token variables entirely when no token is configured and setting them to
empty would fail authentication rather than being ignored. And a boolean input renders
as `True` or `False`, matching AWX's Python capitalisation, so a playbook comparing
against the literal string keeps working.

Credential values themselves do not migrate. AWX will not export them, and neither
platform has a way to read one back out, which is the property you want. Recreate the
values against the imported types.

### Credential input sources

AWX lets a credential field be filled from another credential rather than stored,
through a `CredentialInputSource` row: the field is linked to a source credential of a
type like `hashivault_kv`, plus metadata saying which secret path and key to read.
Pleiades models this the same way, so the shape of your export carries over rather than
needing to be redesigned.

Export them alongside the types:

```bash
curl -sH "Authorization: Bearer $TOKEN" \
  https://awx.example.com/api/v2/credential_input_sources/ > input_sources.json
```

There is no importer for this file yet. Recreate each row against the credentials you
have already created, with `PUT /api/v1/credentials/{id}/input-sources`, whose body
takes the same three fields AWX's own row has: the target's `input_id`, the
`source_credential`, and the `metadata` that addresses the secret inside it.

Four differences to plan around, none of which change the shape of the data:

- **The source must exist first.** A binding names a credential, so create the source
  credentials before the ones that read through them. Where a target's required input
  has no stored value at all, send its bindings in the same request that creates it:
  the credential and its bindings are one write, because a required input with neither
  a value nor a source would otherwise have to be refused.
- **Chains are bounded at four hops.** AWX allows exactly one: a credential's source
  may not itself read from a further source. Pleiades allows a source whose own token
  is external, up to four links, and refuses past that by name. Any AWX export is well
  inside this.
- **`hashivault_kv` is the only external source implemented.** An imported row
  pointing at HashiCorp Vault resolves for real, and its `metadata` fields carry over
  unchanged: `secret_backend`, `secret_path`, `secret_key` and `secret_version` mean
  here what they mean in your export. A row pointing at any of the other seven is
  stored faithfully and fails with an explicit error naming that source, which is the
  same honest-failure convention the "not implemented" types above follow.
- **A source can also be an ordinary credential, which AWX does not do.** Where AWX
  requires the source to be an external secret source, Pleiades also lets an input be
  filled from another credential's own field: name that field in the binding's
  `source_field` metadata. Nothing in an AWX export uses this, so it changes no
  imported row. It is worth knowing about because it is how one stored password serves
  several credentials without being typed in twice, and because it is what a
  certificate bundle's passphrase is bound to.

## Running an unconverted playbook

Ansible interop's execution half is real: given a target device and an unconverted
playbook, a second execution adapter (distinct from the one that runs native runbooks)
generates a single-host Ansible inventory from that device's own capabilities and
tags, runs the real, unmodified playbook inside a fresh, single-use container against
the real device over SSH, and translates the captured output back into this platform's
own job log events. Capabilities and tags reach the playbook two ways: as real Ansible
group membership (so `group_names` works with no extra configuration) and as explicit
`pleiades_capabilities`/`pleiades_tags` hostvars, so a task can read either.

**Read this before assuming it is reachable today.** There is no CLI flag or API field
yet that selects this adapter for a real dispatch: the Runner process a real deployment
runs still only ever composes the adapter that executes native runbooks, because no
job-kind registry exists yet to choose between the two per job. The proof this works is
this repository's own test suite, not a command you can run against a live deployment
yet: `cmd/runner`'s own Ansible Release Gate test dispatches a real job through a real
message broker to a real `runner.Agent`, holding this adapter directly, which spins up
a real container running `ansible-playbook` against a real, independently-implemented
SSH target, including a negative case proving a wrong credential genuinely fails to
authenticate rather than reporting success anyway.

It also carries the same limits [Start here](01-start-here.md#implementation-status)
states: one device per dispatch rather than a whole play's own host list, a batch parse
of the finished run's output rather than a live stream, no GitOps-driven playbook
discovery or Galaxy/pip dependency caching, and host key verification disabled inside
the container (it has no source yet for a target's known host key).

## Inventory migration

`pleiades init` scaffolds an empty `inventory.yaml`. `pleiades add-host <name> --type
<type> [--set key=value ...] [--tags ...]` adds one device at a time by hand;
`pleiades inventory sync --plugin <name>` pulls devices from an external source. Run
`pleiades inventory plugins` for the current list and for what each one needs;
`docs/reference/plugins.md` is the same list generated from the registry. A plugin
needing a per-deployment value takes it as `--set key=value`, so reading an AWS account
is `pleiades inventory sync --plugin aws --set region=us-east-1`, and one that
authenticates resolves its credential from the project credential store by its own name
unless `--credential` names another. Nothing reads an
Ansible dynamic inventory script or a Galaxy inventory plugin directly. There is no bulk import path
from an existing AWX inventory today: migrating one means walking its host list and
issuing one `add-host` per device, or writing a new sync plugin
(`pleiades forge new-plugin`) against wherever AWX's inventory source actually is.

## Try it yourself

```console
$ cd examples/upgrade_ios/pleiades
$ pleiades validate runbooks/upgrade_ios_xe.yaml
$ pleiades validate runbooks/upgrade_ios_xe_sugar.yaml   # same findings, different syntax
```

```console
$ cd examples/upgrade_ios/ansible
$ ansible-galaxy collection install cisco.ios ansible.netcommon   # once
$ ansible-playbook -i inventory.ini upgrade_ios_xe.yml --syntax-check
```

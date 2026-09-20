---
status: beta
---

# Start here

## What Pleiades is, and is not

Pleiades is an object-oriented, strongly typed automation mesh that also runs
Ansible. It targets the same problem AWX and Ansible Automation Platform do: run
automation against real infrastructure, with RBAC, an audit trail, and a scheduler,
across a whole team rather than one laptop. Ansible support exists as a migration
on-ramp, not the destination: a workload lands unchanged, then converts to native
typed collections at its own pace, one playbook at a time.

It is not a finished AWX replacement yet. Read
[Implementation status](#implementation-status) and [Limitations](#limitations) below
before deciding what to build against it today.

## Concepts in ten minutes

These are the nouns used everywhere else in this documentation. Skim once, come back
when a term is unfamiliar.

| Term | Meaning |
|---|---|
| **Runbook** | The native YAML automation format: `id`, `hosts`, `tasks`, and a few more top-level keys. Never called a "playbook": that word is reserved for a real Ansible file. |
| **Task** | One step in a runbook. Names an action by FQCN and passes it `params`. |
| **FQCN** | Fully-qualified collection name, `<namespace>.<method>`, e.g. `net.catalyst.device_facts`. Always namespaced; a bare, undotted name is rejected at registration time. |
| **Collection** | A namespaced Go package implementing one FQCN. Declares what capability and transport it needs. |
| **Capability** | What a device *can do*, not what it *is*. A task requires a capability (e.g. `AptCapable`); a device advertises one by structurally implementing the matching Go interface. Only two legacy action names are checked before a run; see [Implementation status](#implementation-status). |
| **Inventory item** | A managed device: a name, a type, a set of properties, a lifecycle state, a version, and a history of changes. |
| **Lifecycle state** | Where a device sits in its own lifecycle (`active`, `quarantined`, and six others). Only `active` devices can execute a task. |
| **Transport** | How a task's command actually reaches a device. Selected automatically from the device's capabilities; a runbook author never writes `connection: local` or picks a transport by hand. |
| **register** | Names a task's result so a later task's `when_cel` can read it. |
| **when / when_or / when_cel** | Three ways to gate a task: `when` (Ansible-compatible, ANDed list), `when_or` (ORed list, no Ansible equivalent), `when_cel` (a raw CEL expression, for logic the other two cannot express). |
| **Credential** | A username plus a password or SSH key, stored locally in an AES-256-GCM encrypted file. Never written into a runbook or the inventory file. |

For the full, generated reference on any of these, see
[the runbook and task key reference](reference/task-keys.md) and
[the capability vocabulary](reference/capabilities.md).

## The Crawl, Walk, and Run tiers

Pleiades is meant to be adopted incrementally. Each tier is a strict superset of the
one before it, and nothing is gated behind a higher tier that does not need it.

| Tier | What it adds | Infrastructure required |
|---|---|---|
| **Crawl** | The `pleiades` CLI. Scaffold a project, manage a static inventory, store credentials, validate and run runbooks. | None. A single binary, no server, no database, no broker. |
| **Walk** | A Controller and a Runner talking over a real API, plus a web UI that does not reach that API yet. | A NATS JetStream broker and a datastore for the Controller. |
| **Run** | GitOps-synced platform config, promotion gates, and the full Ansible interoperability layer (auto-discovery, Galaxy/pip dependency caching, Kubernetes container groups). A minimal, real slice of unconverted-playbook execution already exists at the Walk tier; see [Implementation status](#implementation-status). | Everything Walk needs, plus a Git-backed config repository. |

Today, Crawl is the tier that works end to end. See the next section for exactly what
that means at Walk.

## Implementation status

This section states plainly what is real and what is not, so nobody has to read Go
source to find out. The module-by-module breakdown is generated directly from the
same registries the engine reads: see
[the full implementation status matrix](reference/implementation-status.md) for every
FQCN by name. The narrative below is not auto-generated and is accurate as of the
change that most recently touched it.

**The Crawl-tier CLI executes for real.** `pleiades run` genuinely connects over SSH
and runs commands against real devices. This is not a claim taken on faith: see
[`examples/webserver_lab/`](../examples/webserver_lab/) for three real Ubuntu
containers, onboarded and driven entirely through the real CLI, with captured
transcripts in that directory's `captures/`.

**The control plane is real and tested.** The data layer, the event bus, distributed
locking, leader election, envelope encryption, the inventory factory, RBAC, the CEL
engine, the workflow DAG builder, the HATEOAS API gateway, and the job dispatcher are
all built, backed by real integration tests against real NATS and SSH containers, not
mocks.

**The distributed execution plane reaches real devices.** A job dispatched through
the Controller and picked up by a `runner` process over NATS now resolves the runbook
to a real compiled DAG and runs it against the device the dispatch names, over the
same real SSH transport the CLI uses. A Collection method runs inside a per-task child
process, so the credential it needs crosses a process boundary on standard input,
never on a command line or in an environment variable. This is proven end to end
against a real NATS broker and a real SSH server, including a negative case where a
wrong credential genuinely fails to authenticate.

Two limits are worth knowing before you rely on it. The Controller resolves a device's
credential and attaches it to the dispatch message, so a secret is present in the
message broker's storage until that message ages out; plan your broker retention
accordingly. Credential types make this **larger in volume and identical in kind**: a
template bound to a cloud credential and two file-generating credentials puts several
more values on the same message, including whole PEM bodies. The full secret manager
described in [Running in production](10-running-in-production.md) is closer than it was:
credentials, devices and saved survey answers all rotate under a new master key, and a
credential input can be read from HashiCorp Vault. PFX and PKI bundle handling is still
not built.

**Credential types and injectors are real.** An administrator can define a credential
type as data, over the API, with an input schema and an injector document, exactly as
they would in AWX; a real AWX export decodes into it with no translation layer.
Credentials of that type are bound to a template, resolved at the moment a job fans
out rather than when it was queued, and injected into the run as environment
variables, extra variables and generated files. A machine credential bound to a
template authenticates every device in the fan-out, and the per-device credential
store remains the fallback, so nothing that worked before still needs changing.

Four limits are worth knowing.

`env` and `file` injectors work on the Ansible path only. The native Go execution path
keeps the stricter rule that a secret never enters a process environment or a file on
the runner's own disk, so it refuses a credential type using either, at the moment you
bind it and again if a dispatch reaches it another way. Extra-variable injection works
on both paths.

One external secret source is implemented: files, which is how a Kubernetes projected
volume, a Vault Agent sidecar and the External Secrets Operator all deliver secrets.
Eight more are named after their AWX equivalents and return an explicit
"declared but not implemented" error rather than resolving to nothing.

A credential input prompted at launch is never stored, which means a job launched with
one cannot be relaunched: the platform says so and points at the launch endpoint
rather than silently repeating the run without it.

The one-credential-per-kind binding rule, with vault credentials exempted while each
carries a distinct identifier, is enforced by the application and not by the database.
A writer going straight to SQL can still violate it.

**The scheduler is real.** A schedule is an RFC 5545 recurrence attached to anything this
platform can launch, carrying its own IANA time zone, its own anchor, and any number of
exclusion rules. Two sorts of thing can be scheduled today: a job template, and a project,
whose run fetches its source. Both travel one code path, so adding a third sort later is not
a change to the scheduler. Writing a schedule requires permission to launch the thing it
names, not merely permission to write schedules. Exactly one controller replica evaluates due schedules at a time, and a
scheduled run reaches devices through the identical dispatch path a person pressing
Launch goes through: the same template resolution, credential binding, durable
JetStream delivery, per-device locking and audit trail.

Recurrence matches AWX rather than approximating it. The engine is tested against
occurrence vectors generated from `dateutil`, the library AWX itself schedules on,
across daylight saving transitions in both hemispheres, a half-hour-offset zone, leap
days, month-end rules, ordinal weekdays and exclusion rules that straddle a transition.
`POST /schedules/preview` expands a rule without saving it and returns each occurrence
in both local and UTC time, so intent can be confirmed before a schedule goes live.

Three limits are worth knowing.

The recurrence grammar is a bounded subset, refused at the write rather than at the
run: `SECONDLY`, `BYWEEKNO`, `BYYEARDAY`, `BYSECOND` and `RDATE` are not supported, and
neither is a rule naming a date that never occurs. The refusal is deliberate — an
unbounded or impossible rule reaching the scan loop would affect every schedule in the
deployment, not just its own.

Occurrences missed while nothing was running are coalesced rather than replayed:
exactly one run happens on recovery, and every earlier missed occurrence is recorded as
skipped with a reason. That avoids a recovery launching sixteen jobs at once, at the
cost of not running work whose moment has passed. A very large backlog is collapsed
further into a single counted record rather than written row by row.

A schedule fires at most once per occurrence, and the guarantee comes from a unique
database index rather than from leader election, which cannot provide it: the lease has
no fencing token, so two replicas can briefly both believe they lead. Three real
controller processes against one shared database are what proves it.

**Six credential types ship with the platform, and sixteen more are named as gaps.**
Machine, Vault, Network, Amazon Web Services, Red Hat Ansible Automation Platform and
HCP Terraform are installed on every controller start under the same namespaces AWX
uses, so an import reuses them rather than recreating them. That is fewer than it may
sound like it should be, and the reason is a fact about AWX rather than about this
platform: most of AWX's own managed types build their environment in Python rather
than in an injector document, so there is no document to copy. Sixteen are recognised
and reported as not implemented with the specific reason for each, and `pleiades
import awx-credential-types` tells you which of them your own export actually
contains, offline, before a migration window. See
[Migrating credentials](03-migrating-from-ansible.md#migrating-credentials).

**The web UI reads the credential surface and does not author it.** Credentials and
credential types are listed, a type's injectors can be tested against sample values
you supply, a template's page offers a control for what it runs as, and its launch
form prompts for inputs that are asked at launch and never stored. Creating and
editing a credential type stays on the API deliberately: an injector document decides
what environment the customer's playbook runs with, which is closer to code than to
configuration.

**An unconverted Ansible playbook can also really run, against one device at a time,
once something wires the adapter in.** A second execution adapter now exists alongside
the native one: given a dispatched device and a legacy playbook, it spins up a fresh,
single-use container running a real `ansible-playbook`, generates its inventory from
the dispatched device's own capabilities and tags, and translates the completed run's
captured output back into the same job log events a native runbook run produces. This
is proven end to end in this repository's own test suite, against a real target
container over a real SSH connection, including a negative case where a wrong
credential genuinely fails to authenticate; see
[Migrating from Ansible](03-migrating-from-ansible.md#running-an-unconverted-playbook)
for exactly what that proves and does not yet prove.

**A real dispatch can now reach it.** The `runner` binary composes both adapters and
routes each dispatch on the launch kind it carries, resolved from an open registry: a
template of kind `runbook` reaches the native adapter and one of kind `playbook`
reaches this one. Two conditions apply. The legacy adapter is composed only when the
deployment supplies `PLAYBOOK_DIR` and `ANSIBLE_RUNNER_IMAGE`, so a deployment that has
never run Ansible simply has no playbook kind to launch; and a dispatch naming a kind
this Runner cannot run is reported on the job rather than retried forever.

Four more limits are worth knowing before you rely on it. It runs
against exactly one device per dispatch, never a whole play's own host list, unlike a
real Ansible run. Events are parsed from the container's captured output after the
playbook finishes, not streamed live task by task. The fuller Run-tier vision described
above is not built yet: no GitOps auto-discovery of playbooks in a synced repository, no
`requirements.txt`/Galaxy dependency caching, no Kubernetes-backed container groups
(this container runs on plain Docker, wherever the `runner` process itself has a daemon
socket). And host key verification is disabled inside the container, since it has no
source for a target's known host key yet.

**The module catalog has 81 registered methods across 16 namespaces; 78 are
implemented and 3 are declared but not implemented.** Every FQCN is registered and
reachable through the real dispatcher, and the short, decision-relevant list is the one
that is NOT implemented, all three of them: `file.template`, `net.junos.config` and
`net.eos.config`. Calling one of those produces an explicit
`"declared but not implemented"` refusal rather than a silent no-op or a fabricated
success, whether the call comes from the CLI, the Controller, or a runner, and the
dispatcher in fact refuses any method whose status is not `implemented` before its body
is ever entered. By namespace, implemented: `svc` 16, `net` 12, `file` 10, `pkg` 9,
`identity` 6, `cloud` 4, `container` 4, `exec` 3, `fw` 3, `archive` 2, `fs` 2, `wait` 2,
`win` 2, `facts` 1, `http` 1, `pleiades` 1. These counts come from the generated
[module catalog schema](reference/schemas/module-catalog.json), which is authoritative
over any hand-written tally including this one. `exec.command` is the first method
that changes anything: it runs a command with no shell interpreting it, and
`creates`/`removes` are what make a task built on it idempotent. `exec.shell` is the same
method with a shell, so a pipe or a redirect behaves as typed. The `file.*` methods read
the device's state before acting, so a second run against a converged device reports no
change. Every implemented method also answers whether it can be undone, and a run that
changes something records the concrete instruction that would reverse it, resolved from
what that run actually found. Nothing performs a rollback yet; see the
[module catalog](reference/modules/index.md) for that per method. See the
[module catalog](reference/modules/index.md) for every method, by namespace.

**Check mode is real, for the methods that declare it.** `pleiades run --mode check`
reports what each task would change and changes nothing. Sixty-nine of the seventy-eight
implemented methods can answer it. Fifty-seven predict a change by reading the device and
comparing through the same code path their real run takes: every `svc.*` method (systemd,
Windows and the generic five), every `file.*`, `pkg.*`, `identity.*`, `fw.firewalld.*`,
`fs.*` and `archive.*` method, `win.feature.*`, `container.docker.run`, `stop` and `remove`,
and the AWS `ec2` and `s3` methods. A prediction leaves out what only the device decides (a
new directory's mode, a version the package manager picks, a new instance's ID) rather than
guessing it. Eight only ever read, so a check runs them for real: `facts.gather`,
`net.ssh.ping`, `net.ios.facts`, `net.ios.ping` and the four `net.catalyst.*` methods.
`net.ios.save` predicts the change a save always reports, without saving. Three check only
some calls: `exec.command` and `exec.shell` when a `creates` or `removes` guard says what
their having run looks like, and `http.request` for a GET, HEAD, OPTIONS or TRACE, which it
sends. A call a method cannot check is named as "could not check" with the method's
reason, and so is a call whose inputs are missing when the check runs (an archive's
source, a bucket that is not yet empty), since an earlier task in the same run may be
what provides them. The nine methods that cannot be checked at all say why on their
reference pages: an arbitrary command or script (`exec.winrm.shell`,
`container.docker.exec`, `net.cli.command`), a configuration change only the device's own
parser decides (`net.cli.config`, `net.ios.config`, `net.netconf.config`), and the three
waits, since what they wait for is usually an earlier task's change, which a check never
makes. A check with any task it could not check ends non-zero rather than reporting a clean result it did not earn. A check writes
no run journal, never records an undo instruction, and is the one kind of run a
simulate-locked device (the state a read-only sync source gives every device it adds)
accepts, and only from a built-in method: a check from an external Collection program is
reported unchecked there, since nothing has proven it only reads. A runbook can also ask for it with Ansible's own `check_mode: true`, on the
whole runbook, a block or a task; `check_mode: false` is refused. The Controller runs
checks too: a template's check route (`POST /api/v1/templates/{id}/check`), or its launch
form's mode, makes a job whose every device is checked by a Runner that changes nothing,
and the job records and shows that it was a check, and whether it was complete: every
device checked with no task left unchecked. A check needs `runbook:check`, which
`runbook:execute` implies, so drift checks can be granted without granting changes. See
[Running in production](10-running-in-production.md#safety-versus-dry-run).

**External Collections load and run.** A Collection method can now be built as a
separate program with the public `pkg/external` SDK, outside this repository, and
loaded from the directory `PLEIADES_COLLECTIONS_DIR` names, by `pleiades` and by the
Runner. Its methods are validated, documented and dispatched like built-in ones, and it
runs as a child process beside Pleiades, never on a managed device. It receives, on
stdin, exactly the credential a built-in method would: the one the credential manager
resolved for the task (the template's bound machine credential, or the device's own). This is proven against a real SSH server with a real
program built from [`examples/external_collection`](../examples/external_collection/).
The honest limits: nothing verifies who built a program (the directory's ownership and
permissions, an approval of each exact build with `pleiades collection approve`, and a
SHA-256 checked before every run are the whole trust decision), there
is no registry to publish or install one from, and loading needs Linux with Landlock,
which confines every program to its own directory and the system files it needs, so
it cannot read the credential store or your SSH keys. See
[External Collections](11-extending-pleiades.md#external-collections).

**Plan-time capability checking covers two legacy action names, not the catalog.**
`pleiades validate` compares a task's required capability against its target device
for exactly `ssh_exec` and `ios_backup`. Those are the only two entries in a
hand-written table (`internal/engine/action_capability.go`), and every other FQCN is
skipped. Registration checks that a method's declared capability names are real names
in the vocabulary, but no validator compares them to a device. So a runbook calling
`net.catalyst.device_facts` against a `linux_server` host prints
`validate: no issues found` and exits 0, then fails partway into the run with
`device "web1" does not have CatalystAPICapable`. The Controller's dispatcher builds
its capability check from that same two-entry table, so it is blind the same way.
Treat a capability mismatch as an error you find by running, not one `validate` finds
for you.

**The whole web UI is a mockup, including its job log viewer.** Five of its six
routes render hardcoded content and make no network request at all. The sixth, an SSE
log stream viewer, does hold real streaming code, but three separate defects stop it
from reaching a real Controller: it requests the hardcoded job ID `"123"` instead of
the one in its own URL, it points at port 8081 while the Controller listens on 8080
by default and nothing proxies between the two, and it connects with the browser's
`EventSource`, which cannot send the `Authorization` header every `/api/v1` route
requires. See [Control plane and API](09-control-plane-and-api.md#web-ui) for the
specifics. To watch a job today, call the API with `curl`, or use the CLI.

## Limitations

Things a real Ansible user will look for and not currently find:

- No Jinja templating in task parameters. A runbook's `params:` map is a literal
  value, never rendered.
- No `loop` / `with_items`. A task runs once per its target device, never once per
  list item.
- No `handlers` / `notify`, no `tags`, no `become`, no `serial`, no `roles`, no
  `ignore_errors`, no `changed_when` / `failed_when`.
- No `group_vars` / `host_vars`, and no inventory-level `vars` at all.
- `check_mode:` narrows only. `true` works on a runbook, a block or a task, and `false`
  is refused, since it would run a task for real inside a check. Nine methods cannot
  answer a check, and each says why.
- No notifications, no webhooks, no approval workflows, no execution
  environments. Surveys ARE built: a template can ask a launching operator for
  typed values that merge into extra variables, authored from the template's own
  Survey section, with AWX's seven question types plus a `file` question that
  carries a text file's content.
- No Vault, KMS or other external secrets manager as a first-class integration.
  Credential types can read an input from a file on the Controller, which covers a
  Vault Agent sidecar or an External Secrets Operator, and the eight named external
  sources are declared and not implemented.

None of these are secret. They are the honest gap between "what AWX does today" and
"what Pleiades does today," and closing them is the bulk of the open roadmap.

## FAQ

**Is this ready to replace AWX in production?** Not yet. The Crawl-tier CLI is real
and useful for scripted, single-operator automation today. The distributed,
multi-user control plane does dispatch real jobs to real devices, both native runbooks
and unconverted Ansible playbooks; see [Implementation status](#implementation-status)
above. What still stands between it and AWX is mostly the list under
[Limitations](#limitations): a runbook has no loops, handlers, privilege escalation,
roles or templated parameters yet, and an unconverted playbook runs against one device
per dispatch rather than across a whole play.

**Why does a module I need say "declared but not implemented"?** It is registered in
the catalog with the right capability and manifest metadata, so `pleiades validate`
and editor tooling already know about it, but nobody has written its real
implementation yet. That is deliberate: a stub that silently reported success would
be worse than one that refuses loudly.

**Can I write my own module?** Yes. Build it as an
[external Collection](11-extending-pleiades.md#external-collections): a separate program
using the public `pkg/external` SDK, dropped into the directory `PLEIADES_COLLECTIONS_DIR`
names. `pleiades forge new-external <namespace.method>` scaffolds a working one. Device
types and sync plugins still live under `internal/`, so adding one of those still means
contributing to this repository or a fork of it.

**Why "runbook" and not "playbook"?** "Playbook" is reserved for a real Ansible
artifact. A Pleiades runbook mirrors a lot of a playbook's shape on purpose, but it is
never the same file, and calling it by the same name would blur a distinction that
matters the moment someone tries to run one as the other.

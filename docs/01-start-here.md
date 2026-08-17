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

## The Walk, Crawl, and Run tiers

Pleiades is meant to be adopted incrementally. Each tier is a strict superset of the
one before it, and nothing is gated behind a higher tier that does not need it.

| Tier | What it adds | Infrastructure required |
|---|---|---|
| **Walk** | The `pleiades` CLI. Scaffold a project, manage a static inventory, store credentials, validate and run runbooks. | None. A single binary, no server, no database, no broker. |
| **Crawl** | A Controller and a Runner talking over a real API, plus a web UI that does not reach that API yet. | A NATS JetStream broker and a datastore for the Controller. |
| **Run** | GitOps-synced platform config, promotion gates, and the full Ansible interoperability layer (auto-discovery, Galaxy/pip dependency caching, Kubernetes container groups). A minimal, real slice of unconverted-playbook execution already exists at the Crawl tier; see [Implementation status](#implementation-status). | Everything Crawl needs, plus a Git-backed config repository. |

Today, Walk is the tier that works end to end. See the next section for exactly what
that means at Crawl.

## Implementation status

This section states plainly what is real and what is not, so nobody has to read Go
source to find out. The module-by-module breakdown is generated directly from the
same registries the engine reads: see
[the full implementation status matrix](reference/implementation-status.md) for every
FQCN by name. The narrative below is not auto-generated and is accurate as of the
change that most recently touched it.

**The Walk-tier CLI executes for real.** `pleiades run` genuinely connects over SSH
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
more values on the same message, including whole PEM bodies. And the full secret
manager described in [Running in production](10-running-in-production.md) is still not
built: no key rotation for credential rows, no PFX handling.

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

**The module catalog has 76 declared methods across 16 namespaces; 22 are
implemented.** Every FQCN is registered and reachable through the real dispatcher:
calling one produces an explicit `"declared but not implemented"` refusal rather than
a silent no-op or a fabricated success, whether the call comes from the CLI, the
Controller, or a runner. The four `net.catalyst.*` methods, against Cisco Catalyst
Center's REST API, plus `net.ssh.ping`, `exec.command` and `exec.shell`, against any
SSH-reachable device, plus most of the `file.*` namespace and the read-only `wait.*`,
`facts.gather` and `http.request` methods, are real today. `exec.command` is the first method
that changes anything: it runs a command with no shell interpreting it, and
`creates`/`removes` are what make a task built on it idempotent. `exec.shell` is the same
method with a shell, so a pipe or a redirect behaves as typed. The `file.*` methods read
the device's state before acting, so a second run against a converged device reports no
change. Every implemented method also answers whether it can be undone, and a run that
changes something records the concrete instruction that would reverse it, resolved from
what that run actually found. Nothing performs a rollback yet; see the
[module catalog](reference/modules/index.md) for that per method. See the
[module catalog](reference/modules/index.md) for every method, by namespace.

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
- No scheduler, no notifications, no webhooks, no surveys, no approval workflows, no
  execution environments.
- No Vault, KMS or other external secrets manager as a first-class integration.
  Credential types can read an input from a file on the Controller, which covers a
  Vault Agent sidecar or an External Secrets Operator, and the eight named external
  sources are declared and not implemented.

None of these are secret. They are the honest gap between "what AWX does today" and
"what Pleiades does today," and closing them is the bulk of the open roadmap.

## FAQ

**Is this ready to replace AWX in production?** Not yet. The Walk-tier CLI is real
and useful for scripted, single-operator automation today. The distributed,
multi-user control plane is built and tested but cannot yet dispatch a real job to a
real device end to end; see [Implementation status](#implementation-status) above.

**Why does a module I need say "declared but not implemented"?** It is registered in
the catalog with the right capability and manifest metadata, so `pleiades validate`
and editor tooling already know about it, but nobody has written its real
implementation yet. That is deliberate: a stub that silently reported success would
be worse than one that refuses loudly.

**Can I write my own module?** Not from outside this repository yet. Every collection
method lives under `internal/`, which Go's own visibility rules make reachable only
from inside this module or a fork of it. A real third-party extension mechanism is
planned but not built.

**Why "runbook" and not "playbook"?** "Playbook" is reserved for a real Ansible
artifact. A Pleiades runbook mirrors a lot of a playbook's shape on purpose, but it is
never the same file, and calling it by the same name would blur a distinction that
matters the moment someone tries to run one as the other.

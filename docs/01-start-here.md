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
| **Capability** | What a device *can do*, not what it *is*. A task requires a capability (e.g. `AptCapable`); a device advertises one by structurally implementing the matching Go interface. Checked at plan time, before anything runs. |
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
| **Crawl** | A Controller, a Runner, and the web UI, talking over a real API. | A NATS JetStream broker and a datastore for the Controller. |
| **Run** | GitOps-synced platform config, promotion gates, and the Ansible interoperability layer for running unconverted playbooks. | Everything Crawl needs, plus a Git-backed config repository. |

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

**The distributed execution plane is a stub.** A job dispatched through the
Controller and picked up by a `runner` process over NATS does not yet reach a real
device. `internal/adapters/native/adapter.go`'s `Execute` simulates three steps with
`time.Sleep` calls and a fabricated `"pong from <device>"` response. This is
completely separate from the Walk-tier CLI's own execution path above, which is real
and unaffected by this gap.

**The module catalog has 75 declared methods across 16 namespaces; 4 are
implemented.** Every FQCN is registered, capability-checked, and reachable through the
real dispatcher: calling one produces an explicit `"declared but not implemented"`
refusal rather than a silent no-op or a fabricated success, whether the call comes
from the CLI, the Controller, or a future runner. Only the four `net.catalyst.*`
methods, against Cisco Catalyst Center's REST API, are real today. See the
[module catalog](reference/modules/index.md) for every method, by namespace.

**The web UI is mostly a mockup.** Of its six routes, one (a live SSE job log viewer)
is real; the rest show hardcoded placeholder content.

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
- No Vault, KMS, or other external secrets manager: credentials live in a local,
  encrypted file only.

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

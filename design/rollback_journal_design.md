# Runbook Rollback via a Per-Run Journal (Design Note)

**Status: SUPERSEDED IN PART, 2026-08-25. Read this header before the design below, because the
central mechanism changed and the rest of the note was never updated.**

This note was a design arrived at through discussion and deferred. Work done since has answered two
of its three layers differently, and better, so the note is kept as history rather than as a plan.

**Layer 3 (how rollback executes) is already decided, and not the way this note proposes.** The note
describes a STATE-RESTORATION model: store old and new values, and have a rollback engine interpret
them back into actions. What actually shipped is TASK-SHAPED. `sdk.RecordInverse`
(`pkg/sdk/inverse.go`) records an `Inverse{FQCN, Params, Description}`, an already-parameterized and
directly runnable task, captured by the forward run, which is the only thing that can know the values
an undo needs. A rollback engine is therefore a loop feeding recorded inverses back through the
existing dispatcher, with no per-method reconstruction logic at all. Thirty-five call sites across
twenty-nine files already cover every method declaring `Reversible: true`.

**Layer 2 (the two capabilities) is withdrawn.** `JournaledCapable` and `RollbackCapable` cannot be
built as described, because a Collection method has no way to declare or provide a capability, and
they are not needed: `collection.Reversibility` already answers whether a method is safe to reverse,
per method and enforced at registration, and `sdk.RecordDiff` already produces structured old and new
value diffs universally, without a capability. `pkg/collection/manifest.go` records the note's own
approach as the mistake worth remembering: "The true inverse is almost never a property of the
METHOD. It is a property of the RUN."

**Layer 1 (the journal) is still real, still unbuilt, and is what Phase 40 is now about.** Two of its
details here are wrong: there is no OpenTelemetry trace correlation to reuse, and it cannot be built
on `inventory.Revision`, whose `History()` returns hardcoded `nil` on the Walk tier and which carries
no task, FQCN, target or outcome. The journal is a new entity written from `engine.NodeResult`.

The note is also silent on the largest safety question in the phase: it proposes storing old and new
device property values and never mentions masking, redaction or encryption, while those values reach
a reader as plaintext.

What remains accurate and worth keeping is the reasoning below for REJECTING an auto-generated
reverse DAG, and the fallback rule that a module which cannot be mechanically reversed still gets a
journal entry and requires an author-written rollback artifact.

**Internal design material, not user documentation.** Cited from the roadmap as rationale for an
unbuilt feature, never published as a description of current behavior.

## The problem

A runbook can declare a `rollback` action: a compensating step to run if the runbook fails partway
through. When that trigger fires, how does the orchestrator actually execute the compensating action?

## Rejected approach: auto-generating a reverse DAG

Automatically computing the inverse of whatever already ran was considered and rejected. It would
require every module to have a knowable semantic inverse, and most real operations (a firmware upgrade
plus reload, a service restart) do not have one: the "old value" recorded somewhere is data, not a safe
procedure back to it. Ansible has no equivalent concept either; most Ansible modules are idempotent, not
reversible. Computing a wrong inverse of a partially executed run risks leaving a device worse off than
the original failure, which is exactly the kind of inferred correctness this project's RULE 0 (nothing is
"done" unless verified the way a real user would experience it, never faked or inferred) argues against.

## Adopted design, in layers

**1. Journal (universal, always produced).** Every task execution writes a journal entry: what ran,
against what target, when, and whether it succeeded, tagged with the run's OpenTelemetry trace ID. This
reuses trace correlation already present elsewhere in the platform design rather than inventing a new
identifier scheme. The journal is a troubleshooting and audit artifact on its own, independent of
rollback, and every module produces one regardless of capability.

**2. Two separate capabilities, not one.** Both are new `pkg/capability` entries, declared and
structurally verified the same way every other capability in that package is (the existing binding rule:
`capability.Implements` checks a real interface, never an unchecked boolean claim).

- `JournaledCapable`: the module reports its changes as structured old and new value diffs, reusing
  `InventoryItem`'s existing `Revision{Version, ChangedAt, Field, OldValue, NewValue}` and `History()`
  shape, which already exists for tracking property drift and is repurposed here as an undo log. This
  capability is only about producing good structured audit data.
- `RollbackCapable`: the module's changes are actually safe to mechanically reverse. Kept separate from
  `JournaledCapable` on purpose, since the two are not the same claim: a module can produce clean
  structured diffs for an operation that still should not be auto-reversed (sending a notification,
  allocating a resource with side effects elsewhere). `RollbackCapable` without `JournaledCapable` is
  meaningless, since there would be no structured journal to replay against, so validation should treat
  `RollbackCapable` as requiring `JournaledCapable` too, the same kind of paired-dependency check already
  planned for `rescue`/`always` requiring `block` in the task schema. In practice this realistically only
  fits declarative, idempotent modules (a config attribute such as a VLAN ID or a hostname), not
  procedural, multi-step operations.

**3. Rollback execution, gated on both capabilities above.** For a module declaring both
`JournaledCapable` and `RollbackCapable`, rollback is mechanical: fetch the failed run's journal by trace
ID and replay its Revisions in reverse. No author-written rollback step is needed. For any other module
(including one that is `JournaledCapable` but not `RollbackCapable`), the journal entry still exists
(useful to a human troubleshooting the failure), but rollback requires the explicit, author-written
`rollback` artifact: a task list or a single FQCN action, dispatched as a fresh job through the normal
dispatch path (the same Trigger Engine, Lock Manager, and RBAC scope check any other runbook goes
through), not a separate "reverse execution" subsystem. Validation should eventually be able to flag a
runbook whose metadata implies rollback but whose modules cannot back it, the same way the existing
capability rule already flags a capability mismatch between a task and its target device.

## Summary of the fallback rule

A module declaring both `JournaledCapable` and `RollbackCapable` gets automatic rollback for free. Every
other module still gets a full journal entry for troubleshooting, but rollback for it must be explicit,
authored by hand, never a silent best-effort guess.

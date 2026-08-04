# Runbook Rollback via a Per-Run Journal (Design Note)

**Status: not yet implemented.** This is a design arrived at through discussion, deferred the same way
the DAG executor (Part 0 Phase W5) and a real SSH transport (Phase W6) are deferred. It depends on the
task/block/rescue/always runbook shape and does not change anything about that shape itself. Recorded here
so a future implementation session does not have to re-derive it.

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

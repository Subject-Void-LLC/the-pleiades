# Desired State, Plan Mode, and Drift (Design Note)

**Status: not implemented, and mostly rejected.** This note records a design conversation about whether
Pleiades should adopt OpenTofu and Terraform style declarative state behavior. Most of it was rejected,
for reasons worth keeping so nobody re-derives them. A narrow slice survives and is worth building. A
blocking prerequisite was found along the way and matters more than anything else here.

**Internal design material, not user documentation.** Cited from the roadmap as rationale for an
unbuilt feature, never published as a description of current behavior.

## The question

The inventory is not a flat host list. Items carry a per field revision history
(`Revision{Version, ChangedAt, Field, OldValue, NewValue}`), a lifecycle state, a version, and a
declared authoritative source. That looks like the state store underneath Terraform, and arguably a
better one, since Terraform keeps a single whole object snapshot per resource and this keeps field
level lineage. If the substrate is already there, how much of Terraform's behavior comes with it?

The estimate under discussion was two thirds. It is not close to that.

## First, the correction that reframes everything

**What exists today is a state vocabulary, not state machinery.** Every type this argument rests on is
correct Go with no behavior behind it. Verified at commit 3099288:

- `Revision` (`pkg/inventory/item.go:81`) has exactly two writers, `Base.AddInfo` and `Base.RemoveInfo`
  in `internal/inventory/record/record.go`, and **zero non test callers**.
- `internal/ent/schema/device.go` holds `name` and a JSON properties blob. There is no revision table,
  no version column, and no lifecycle column. Nothing has anywhere to be stored.
- `inventory.Repository` (`internal/inventory/iterator.go:31`) has one method, `GetGroup`, and it
  reads. **There is no write path from a hydrated item back to storage.**
- `NewBase` does not restore `version` from the record, so every hydrated item is version 0 forever.
- `SourceAuthority.SyncedAt` is never set. `Source()` has one caller, and it is a test.
- The scoped multi source authority model in `PLAN.md` Section 11 does not exist in code. The Go type
  is a single unscoped owner.

So comparing this to `terraform.tfstate` compares a design to a scar. Terraform's state file is bad in
production, under load, at three in the morning. Ours has never been written to disk. That asymmetry
inflates every favorable comparison, and it should be named before any of them are believed.

**Honest arithmetic:** weighted by how much of Terraform's value each mechanism carries, roughly 13
percent exists in built code, and roughly 30 percent if every design document is granted as though it
had shipped. The substrate claim is around 60 percent true as a design. The engine claim is around 7
percent, and three of its seven parts are not merely unbuilt, they are built the other way.

## The structural reason, which is not a gap to be filled

**The inventory holds the wrong side of the diff.**

Terraform's state is intent shaped. It records what Terraform itself wrote, so a difference from
reality has exactly one meaning and exactly one correct response. Two parties, one comparison.

The Pleiades inventory is observation shaped, and specifically it is an observation of a **third
party**. `PLAN.md` Section 11 gives the sync plugin authority and casts this platform as an enricher.
That produces three parties, not two: NetBox says X, the device is Y, the runbook declared Z. A three
way disagreement has no total order, and `SourceAuthority` ranks only two of the three. It ranks
sources of documentation against each other. It never ranks documentation against reality.

`PLAN.md` Section 11 shows five versions of one item written by five different actors: a NetBox sync, a
discovery pass, an admin edit, an agent check in, and an allow list change. **A store with five writers
cannot be an ownership ledger.** "This differs from what I last wrote" stops meaning anything the
moment more than one thing writes. That is the schema working as intended.

A diff engine built on the inventory as it stands would converge NetBox's documentation, never the
network, and would never open an SSH session to find out.

## Rejected: a declarative resource model

Adopting resources with derived actions would reverse four decisions that were each made separately and
each correct on its own terms:

1. **No saga.** `PLAN.md` Sections 27 and 30 say a partially applied run is corrected by running again,
   not by compensating backward.
2. **No mechanical inverse.** `docs/rollback_journal_design.md` rejected reverse DAG generation because
   most real operations, such as a firmware upgrade or a service restart, have no safe inverse.
   Terraform destroy is exactly a reverse graph walk.
3. **The verb is in the module name.** `docs/hephaestus.md` settled on `pkg.apt.install` rather than
   `pkg.apt` with a `state` parameter. A diff engine cannot choose between `svc.start` and `svc.stop`,
   because the author already chose. Verb per action naming and diff derived action are in direct
   tension, and the naming decision is the better one.
4. **No provider schema exists.** `engine.Task.Params` is `map[string]interface{}`, and the only key
   any code reads is `target`. Terraform can diff because providers declare typed attribute schemas.
   There is nothing to diff against here, in code or in specification.

## Rejected: a general plan mode over runbooks

The most attractive idea in the conversation, and the one that fails hardest. The proposal was to split
every idempotent module's existing compare step from its act step, so plan and apply fall out of the
ordinary task list without any new language:

```go
type Plannable interface {
    Plan(ctx, target, params) (Plan, error)  // NoChange | WillChange([]Revision) | Unknown
}
```

It is appealing because Ansible modules already run read, compare, act, report internally, so the
comparison logic is genuinely already written. Two things kill it.

**Unknowns are contagious through `when`.** Folding predicted revisions forward into a shadow item
handles an unknown *value*. It cannot handle an unknown *action*. A task carrying
`when: result.rc == 0` may not run at all, and everything after it inherits that uncertainty. Terraform
never meets this problem because its graph is data flow. A runbook is control flow. One `register` plus
`when` pair renders the remainder of the runbook unknown, which is the whole runbook in practice.

**A hybrid plan cannot be rendered honestly.** Terraform has unknown values, never unknown actions. An
imperative task such as `exec.shell` must plan as either no change, which makes a run that will
absolutely change something look like it will not, or as always changing, which is unreviewable noise.
There is no third rendering. The plan artifact is the entire point of plan mode, and it degrades to
noise in exactly the hybrid case this platform is built for.

## Adopted: declared baselines, compared read only

What survives is smaller and reverses nothing. This platform exists to configure devices that already
exist, and the configuration management third of Terraform's value is real and reachable.

A **baseline** is a declared set of expected field values for a target. Comparison is a pure function
against recorded observations. It produces a delta and nothing else.

```
Baseline{Target, Requires, Expect}  ->  Compare  ->  Delta{Status: Match | Differ | Unobserved}
```

- The delta reuses `inventory.Revision` as its shape, so plan output, journal entries, and drift
  reports are one type viewed at three times.
- Remediation is an ordinary imperative runbook dispatched through the existing trigger engine, which
  already specifies debounce, cooldown, circuit breaking, blast radius, and a simulation gate. No new
  execution path.
- This is `terraform plan -refresh=false` over a better store.

**Boundaries, which a future contributor will be tempted to cross:**

- No `--fix`. Comparison never applies anything.
- No mapping from an expected value to a module. `internal/baseline` must not import `internal/engine`,
  and an import graph test should enforce it.
- A field absent from `Expect` is **no opinion**, never a deletion. The day an unexpected field is
  treated as a deletion, the baseline silently becomes an ownership claim and every failure above
  fires.
- `Compare` never writes.

**The RULE 0 gate:** a delta with no observation must report `Unobserved`. It must never print "no
changes." Nothing in this repository has yet opened an SSH connection, so `Unobserved` is currently the
only honest answer for every field, and the implementation must be able to say so.

## Deletion without an ownership record

One idea from the conversation does hold, and it is worth keeping separate from the rejections above.

Terraform knows what to destroy because it has an ownership record. There is a second route to the same
answer that needs no such record: **if a module can completely enumerate a bounded scope, then anything
in that enumeration not covered by an assertion is by definition unmanaged.** This is how Puppet's
`purge`, Ansible's `authorized_key: exclusive=yes`, and `kubectl apply --prune` all work, and none of
them keeps a state file.

This relocates ownership from an implicit record of the past to an explicit declaration of intent. The
runbook states what it owns, that statement lives in Git, it is reviewable in a pull request, it cannot
drift, and it needs no locking.

It requires two things that fact gathering alone does not provide:

- **Completeness must be structural.** A module that cannot guarantee a complete enumeration of its
  scope must not claim the capability. Partial enumeration returns an error and never a short list,
  because a short list is indistinguishable from a small world and deletes things that were merely not
  observed.
- **Scope comes from the module, not the author.** "Remove every package not declared" against a Linux
  host removes the kernel. Each module defines its own natural bounded scope, such as one directory,
  one user's authorized keys, or one firewall zone, and the author only opts in with `exclusive: true`.
  A module with no natural scope is not enumerable, and `exclusive: true` on it is a validation error
  rather than a silent no op.

This is a per module property useful for scoped replacement. It is **not** a route to a whole graph
plan, and it does not rescue either rejected design above.

## The cheapest thing on the list, which is not blocked by anything

Terraform's inferred dependency graph is the one mechanism here that needs **no state file, no refresh,
and no ownership claim**. It only requires that ordering be data shaped: task B depends on task A
because B references A's output. That is independent of everything else in this note, and it is the
second most valuable mechanism Terraform has.

The uncomfortable part is how close this repository already is, and how deliberately it stops short:

- `DAG.Adjacency` is a real graph structure, `map[string][]EdgeConfig`.
- `internal/engine/topology.go` implements Kahn's algorithm.
- `hasCycle` does a real depth first search, and its own comment notes that cycles are "structurally
  unreachable" in what it is given.
- `EdgeConfig` carries a compiled CEL condition per edge.
- `Task.Register` is parsed off the wire.

And then `synthesizeChain` (`internal/engine/tasktree.go`) chains each task's exit to the next task's
entry in list order, giving every node out degree one and in degree one. **The graph is a linked list.**
`Task.Register` has zero readers outside tests, verified by grep.

So the full cost of the mechanism has been paid, and none of the benefit collected. Reading `Register`
and inferring an edge where a later task references an earlier task's registered result would turn the
existing machinery on, without touching state, ownership, or the write path. It is the highest value
change per unit of work in this entire analysis, and unlike everything else here it is not waiting on
anything.

Two honest caveats. Inference must be **additive**: authored list order stays the default, so a runbook
that references nothing behaves exactly as it does today. And an inferred graph makes the control flow
problem above worse rather than better, because a task that may not run is now a dependency that may
not resolve. Inferring ordering is safe. Inferring *actions* is what was rejected.

## The blocking prerequisite for the state work

**Wire the write path before any of this.** `Repository` is read only, there is no revision table,
`version` does not survive a load, and no lifecycle column exists. Every claim in the opening section
of this note rests on types that have nowhere to be stored.

Concretely: a `Save` on `Repository`, a revision table in ent, `version` restored in `NewBase` and used
as an optimistic concurrency token, and a lifecycle column. This unblocks the most value per unit of
work, requires reversing nothing, and is where the differentiation against `terraform.tfstate` actually
banks. Until it exists, the inventory cannot record anything, and a design note about comparing
recorded observations has nothing to compare.

## Two specification defects found while writing this

- `PLAN.md` Section 13 states "Agent/polling provides drift detection, no state comparison needed."
  That contradicts Section 22's fact store and drift detection design in the same document, and no
  phase funds a fact store. One of the two has to go.
- The same section implies `plan` reports drift. It cannot. Planning contacts nothing.

## Three things this platform already does better, worth stating so the comparison is fair

The rejections above are not a verdict that Terraform's model is superior. Three rows favor this
design, and two of them favor it in built code rather than in intent:

- **Per device locking beats whole workspace locking.** `internal/lock` locks one device.
  Terraform locks the entire state blob, which is the documented root of the split state spiral. This
  is a real, shipped advantage.
- **Facts are encrypted at rest by default**, through the ent hook in `internal/crypto`. Terraform has
  no equivalent, and OpenTofu only gained state encryption in 1.7.
- **The identity binding problem is dissolved rather than solved.** Devices arrive with pre-existing
  identity from a sync plugin, so Terraform's single largest brownfield cost, importing existing
  resources one at a time, never appears here at all.

The field level `Revision` shape is also genuinely better than a whole object state snapshot, since it
carries per field provenance and history rather than a flat last known value. That advantage is real
but currently theoretical, for the reason in the previous section.

## Summary

The inventory is the wrong side of the diff, it has five writers, and it is an observation of a third
party, so it cannot be an ownership ledger. A declarative resource model and a general plan mode are
both rejected, the second because unknown actions are contagious through `when` and a hybrid plan
cannot be rendered honestly. What survives is a read only comparison of declared baselines against
recorded observations, remediated by ordinary runbooks, and that work waits on a write path that does
not exist.

Separately and not waiting on any of it: the DAG is a linked list with a topological sort bolted to it,
and `Task.Register` is parsed and ignored. Inferring edges from registered references is the cheapest
real improvement available, and it needs nothing from this note's state discussion.

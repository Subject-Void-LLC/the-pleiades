# Design note: `rescue:` and `always:` that run

Status: built for the Crawl tier and for a Runner's one device (sections 1 to 4, and the rollback order of section
5). Not built yet: `rollback_on_failure` (section 5) and the Walk tier across devices (section 6). Before this, the
schema, `pleiades validate` and the plan view accepted `rescue:` and `always:` and the executor never ran them: a
block whose task failed ran no rescue, and a block's `always:` was skipped in silence while the run exited 0. This note settles how they run in an engine whose
contracts are a check that predicts, a journal that records undos, and a rollback that replays them. Where Ansible
treats `rescue:` as a jump, here it is a state transition, and every rule below follows from that.

## What exists to build on

Checked against the code, 2026-10-20:

- **Tasks.** `Task.Rescue` and `Task.Always` are parsed and validated, block-only (`validateTask` refuses them on a
  parallel group). They are nodes of the DAG (`DAG.Nodes`), so every validation rule already sees them, but they are
  not in `DAG.Adjacency`, and `Executor.Run` stops its whole walk at the first failure.
- **Edges.** `EdgeTypeOnFailure` and `EdgeTypeAlways` are declared, and no executor reads them.
- **Rollback.** The planner (`internal/rollback`) already replays undos newest first, in the reverse of the order the
  run made its changes, so the last-in-first-out rule needs no new mechanism. A task's authored `rollback:` list wins
  over its recorded undo; `rollback:` inside `rescue:`/`always:` is refused today only because they never run. An
  earlier, partial rollback of a run is tracked (`Plan.AlreadyUndone`) and resumed rather than repeated.
- **Failed tasks.** A failed task contributes the undo its method recorded before failing, and otherwise an effect
  nobody can name (`Plan.Unknown`), which a rollback refuses until accepted with `--allow-unknown` (FAILURE_PATTERNS
  452, 487, 507). The planner's own doc comment still says an undo is recorded only on success; that comment is to be
  checked and corrected while building.
- **Reports.** A task's status is one of ok, changed, would_change, checked_only, failed, held, skipped, unchecked; a
  run's outcome is complete, failed, incomplete, invalid or error. A failed task whose device never answered carries
  `unreached`.
- **Not Ansible's.** `run_once`, `delegate_to` and `context` are build errors today.

## Semantics

- **`always:`** runs after the block on each device the block ran on, whether the block succeeded, failed, or was
  rescued.
- **`rescue:`** runs on a device only when a task of the block failed there. A rescue that succeeds recovers the block:
  the run goes on, and the run's outcome says it was rescued (below). A rescue that fails fails the run; `always:`
  still runs first.
- **What triggers a rescue.** A failed task, an unreached one included (the rescue can tell, below). Not a skipped task,
  and not a held one: a result held for a person's decision is a pause, not a failure, so the block stops, `always:`
  runs, and the run ends held.
- **A run that has to end still cleans up.** When a failure ends the run (one nothing is left to handle, or a failure
  of a task itself: its condition, its parameters, its target), the run still runs the `always:` of every block it is
  inside, on every device, and nothing else, before it stops. One device's failure never skips another device's
  cleanup.
- **Scope.** Per device. A block's failure on one device rescues that device; the others carry on with their own
  block. Outside a block, a failure still stops the run as it does today: nothing changes for a runbook without
  `rescue:`.
- **Conditions.** A block whose `when:` is false is skipped whole, its `rescue:` and `always:` with it.

## 1. A recovered failure is visible

- **Run outcome `rescued`.** A run in which any block was rescued and nothing else failed ends with
  `outcome.status: "rescued"` and exit 0, never `complete`. The `--json` document and the journal say which tasks
  failed and which rescue answered each, so a program or an agent can tell "the desired state was reached" from "the
  primary path failed and the fallback ran". (Your note proposed `succeeded_with_rescue`; the existing outcomes are
  single words, so `rescued`.)
- **`--fail-on-rescue`.** On `pleiades run` (a Walk-tier launch field comes with section 6): a run a rescue answered
  exits 4, a status of its own (the owner's decision), the outcome still `rescued`, so a gate never passes through a
  fallback and can tell one from a failure without reading the report.
- **The failed task keeps `failed`,** and gains `rescued_by` naming the rescue's first task.
- **Both faults survive.** A block failure followed by a rescue failure reports both, the block's first: the run's
  message names the root cause and then the rescue's failure, and neither error replaces the other.

## 2. The rescue knows what failed

- **An `error` root,** readable by templates and conditions inside `rescue:` only: `error.task` (node id),
  `error.name`, `error.method`, `error.device`, `error.message` (masked, like every report text), `error.unreached`
  (the device never answered). There is no `error.undo_recorded`: a failed task never journals an undo, so it would
  always be false.
- **Immutable and scoped.** Built once when the rescue starts, from the failing task's result. `validate` refuses
  `error.` anywhere else, alongside the existing check that a template reads only `vars`, `nodes` and `result`
  (`TemplateProblems`) and the condition compiler's own roots.
- **Untrusted.** `error.message` holds device output. In command text it is held to `| quote` or `| cli_token` like
  any value, and in a URL it cannot choose the host, by the rules every template already meets.

## 3. Registers across a rescue

- **A register written only inside the block** may not exist after a rescued block. `validate` refuses a task after
  the block that reads it (`result.<name>`, `nodes.<name>`, a condition's `stat.<name>`) unless the rescue registers
  the same name too, or the read handles absence (`| default(...)`, `has()`).
- **Types.** Requiring both paths to register the same *type* waits for typed data between tasks (each method's
  declared returns compared at validation), which is its own piece of work. Until then the check is by name; at run
  time an undefined register fails the render loudly, so absence is never silent.

## 4. Check mode

- **`rescue:` is not predicted.** A check cannot know a block will fail, and running a fallback's check against a
  healthy device predicts nothing true. Each rescue task reports `status: "conditional_skip"`, reason "runs only if
  the block fails", and does **not** count toward an incomplete check (exit 3): it is not a gap in the prediction, it
  is a path the prediction does not take.
- **Statically checked all the same.** Every `validate` rule already runs on rescue and always tasks (they are DAG
  nodes): transports, parameter types, command and URL templates, capabilities. A test is added that holds every
  registered rule to seeing them, so a rule that walks only the happy path fails it.
- **`always:` is predicted** against the block succeeding, and its check result says so in its reason, since a step
  that depends on what the block made partway can only be predicted on that assumption.
- **Your note's scoped plan hash** has no counterpart to change here: a check's exemption is the task's status, and
  the runbook's version (`DAG.Version`) already covers `rescue:` and `always:`.

## 5. Rollback

- **Order.** Rescue and always steps are journaled at the level they ran, after the block's, so the planner's existing
  newest-first replay undoes `always`, then `rescue`, then the block. Example: B1 succeeds, B2 fails, R1 succeeds,
  A1 succeeds; the journal holds B1, B2 (failed: its recorded undo or an unknown effect), R1, A1; a rollback runs
  undo(A1), undo(R1), undo(B2) if it recorded one, undo(B1). A test holds that order.
- **`rollback:` inside `rescue:` and `always:`** is allowed once they run, with the same rules as any task's.
- **The double-undo hazard.** A rescue that compensates by hand (R1 puts back what B1 changed) is undone by a later
  rollback, which then re-breaks what R1 fixed before undoing B1. The answer is to let the engine do the
  compensation:
  - **`rollback_on_failure: true` on a block.** When a block task fails, the engine undoes the block's completed
    tasks on that device, newest first, before the rescue starts, and journals those undos as a rollback of those
    nodes, so a later `pleiades rollback` finds them already undone (the planner's `AlreadyUndone`) and does not
    repeat them. The rescue starts from where the block began.
  - **Refused unless every undo is exact.** `validate` refuses `rollback_on_failure` on a block containing a task that
    cannot be undone exactly (a method with no undo and no `rollback:`, an undo that withholds a value, a partial
    one), the same reasoning the `reversible:` rule already applies.
  - **An undo that fails** leaves the device in a state nobody chose: the rescue does not run, `always:` does, and the
    run fails reporting the block's failure and the undo's.
  - **Without the key,** a hand-written compensating rescue remains possible. A rollback then needs `--leave` for the
    rescue's steps, which the docs will say beside the key.

## 6. The Walk tier (after Phase 117b's segmented dispatch)

A `rescue:` or `always:` task whose target differs from its block's is a later segment, so this part waits for
segments and comes as its own change. When it does:

- **Liveness first.** Before a block starts, the Controller checks that every device its `rescue:` and `always:` name
  answers and accepts its credential, so a fallback host is not found unreachable twenty minutes into the block. A
  device that fails this stops the run before the block runs.
- **Fan-in, explicitly.** A block over several devices that fails on some runs a rescue per failed device by default.
  A rescue aimed at shared infrastructure (a load balancer, a ticket) needs an explicit key to run once for all the
  failed devices, with `error` listing them, so a group's failure does not stampede the shared host. The key is to be
  named: `run_once` is refused today as an Ansible keyword, so either adopt it with exactly these semantics or choose a
  native name.
- **One block, one id.** Journal entries carry a `block_execution_id` linking a block's tasks on one device to its
  rescue and always tasks on another, so a run-level rollback interleaves the devices' undos in the reverse of the
  order they committed. The Crawl tier records it too, for one model.

## The graph and the executor

As built, which is simpler than the edges first sketched here: an edge chooses one path for every device at once, and
a block's handling is per device.

- **One chain.** The builder splices a block's own tasks, then its rescue tasks, then its always tasks, into the chain
  where the block sits (internal/engine's chain.go), and records for every task the blocks enclosing it and its part
  in each (`DAG.Scopes`), and for every block the tasks of each part (`DAG.Guards`). `EdgeTypeOnFailure` and
  `EdgeTypeAlways` stay unused.
- **Per-device handling.** Each device keeps a stack of the failures its blocks are handling (handlers.go). A task asks,
  per device, whether it runs there: a block task not where an earlier task of its block failed, a rescue task only
  where its block failed and its rescue has not, an always task unless an earlier always task failed there. A task the
  handling keeps from a device is journaled as skipped (`block_failed`, or `not_failed` for a rescue with nothing to
  answer).
- **When a failure ends the run.** At once, as before, when no rescue or always is left that would run for it, which is
  every failure in a runbook without them; otherwise when the walk has passed the block and the failure stands. A
  rescue that runs to its end without failing answers it, and every failure it carried (an inner rescue's, an always
  task's).
- **Rendering.** A guarded task's devices come from its authored target, so a device it will not run on renders and
  evaluates nothing; a rescue task renders and evaluates its condition per device, with that device's `error`.
- **Proof.** The executor is compared with an independent recursive interpreter of these semantics over hundreds of
  generated nestings with random failures, on one device and on two failing independently, and by the fuzzer; a
  release gate runs a block that fails for real against a real `sshd`.

## Order of work

1. **`validate` warned** on every `rescue:` and `always:` while they did not run. Done, and removed by step 3.
2. **This note.** Done.
3. **Executor and graph,** with the oracle, the fuzz test and the release gate. Done.
4. **Journal and rollback:** the order and `rollback:` inside rescue and always are done; `rollback_on_failure` is not.
5. **The Walk tier,** after Phase 117b.
6. **The lab.** Each `# TODO(always):` task in the lab project moves into an `always:`, proven by failing a task in its
   block through Pleiades.

## Decisions

Settled by the owner, 2026-10-20: `--fail-on-rescue` exits with a status of its own, 4; `always:` does not read `error`
(rescue only, as in Ansible); a check that fails reports the failure, as before, and never routes to a rescue.

Open: the run-once key's name for a Walk rescue aimed at shared infrastructure (`run_once` is refused today as an
Ansible keyword, so either adopt it with these semantics or choose a native name).

# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**Branch `feature/Brutalist-UI-Scaffold`. Directive: close the rest of
`.SPECIFICATION/AWX_PARITY_ROADMAP.md` Section 3b.1 ("launch fields never reached execution"), the
two remaining hops after the prior session captured `Resolved.Fields`/`ExtraVars` onto the job
record. Everything below is uncommitted, held per standing instruction (a commit message is
prepared but no commit was made).**

**Section 3b.1 is now CLOSED.** Both remaining hops built and tested against real infrastructure,
no shortcuts. Full file:line detail lives in `.SPECIFICATION/AWX_PARITY_ROADMAP.md` Section 3b.1's
own "Status: CLOSED" writeup; the summary:

1. **The wire.** `pkg/wire.DispatchPayload` gained `Fields`/`ExtraVars map[string]any` (additive,
   `omitempty`, following the `Kind`/`Interruptible` precedent), and
   `internal/dispatch/worker_devices.go`'s payload build site now reads `job.Fields`/`job.ExtraVars`
   onto it.
2. **The legacy adapter.** `internal/adapters/legacy/argv.go` (new) replaces the hardcoded
   `ansible-playbook -v -i ...` literal with real `--limit`/`--forks`/`--tags`/`--skip-tags`/`-e`
   construction and per-run `timeout` (via a context deadline around the container run, since
   `ansible-playbook` has no run-timeout flag of its own). Proven against a **real Docker
   container**: `cmd/runner/ansible_release_gate_test.go`'s new
   `TestAnsibleReleaseGate_LaunchFieldsReachRealInvocation` dispatches with `forks: 1` and
   `job_tags: [deploy]`, asserts the exact argv the real Docker daemon started the real container
   with (a new `observingOrchestrator` test double wraps the real `DockerOrchestrator`, changing
   nothing about what executes), and asserts behaviorally that `--tags` really excluded an untagged
   task from a real two-task play.
3. **The native adapter.** `engine.Executor` did **not** already have any variable-override or
   per-task-timeout plumbing (confirmed via `gopls references` on its two production call sites, not
   assumed) — this was new engine work, not just adapter work. `ExtraVars` now reaches a runbook's
   `when_cel` conditions through a new `"vars"` CEL root (`engine.WithVariables`); `timeout` is now
   a genuine **per-task** abort (`engine.WithTaskTimeout`, wrapping each device's own `ctx` inside
   `runOne`, not the whole `Run` call) — the runbook kind's FieldSpec text says "per task", the
   opposite of the playbook kind's "per run" reading of the same field name, and both adapters now
   honor their own kind's stated semantics correctly. **`forks`/`limit` are deliberately NOT wired**
   for the native adapter: `singleDeviceResolver` always resolves every task to the one device this
   Runner invocation already got dispatched, and every device-targeting task takes an exclusive
   per-device lock before running, so there is no concurrency dimension within one `Execute` call for
   `forks` to bound — wiring it into `maxConcurrency` anyway would have been a real parameter set to
   a real value with a provably zero effect, forever. This is `LESSONS_LEARNED.md` #106, found and
   documented, not shipped as a bug.

**`make ci` is green, verified more rigorously than a single pass, because the first attempts
weren't clean and each failure needed to be run down rather than dismissed.** `go build`, `go vet`,
`gofmt`, `gosec` (8 pre-existing waived findings, none new), `govulncheck` (0), `coverage-check`
(146 packages measured, none below floor), `docs-lint`, `docs-gen-check`, and `templ-gen-check` all
passed cleanly on the first try. `test-race` and `test-integration` (`go test -race ./...` and
`go test -tags integration -race ./...`, the whole module) each flaked inside `make ci` itself, and
each flake was run down individually rather than assumed benign, per standing instruction:

- `test-race` failed twice, on `TestCLI_RunExecutesSSHTransport` (`cmd/pleiades`) and
  `TestAgent_FailedExecutionEventuallyDeadLetters`/`TestAgent_ReleaseGate_
  PullsFiveDispatchesWithoutDuplicating` (`internal/runner`) across the two attempts — all Docker
  port-mapping races (`port "X/tcp" not found`), none in a package this session touched. All three
  passed cleanly in an isolated serial rerun. A full, uninterrupted `go test -race -timeout 20m ./...`
  run (not stopped at the first failing package the way `make`'s own chained targets are) then
  completed with exit 0 and zero `FAIL` lines across the entire module.
- `test-integration` failed three times across three attempts, on three **different** tests each
  time: `TestGrandIntegration_EachKindReachesItsOwnAdapter` (`tests/e2e`, 913s before failing, "job's
  log stream never mentioned 'native execution'", with the runner subprocess's own log showing a
  redelivery/device-lock-contention loop), then a clean pass, then
  `TestControllerLeaderElection_ReleaseGate` (`cmd/controller`, "SPLIT BRAIN DETECTED"). Different
  package losing the race each run is `FAILURE_PATTERNS.md` #61's own named signature for resource
  contention under this sandboxed environment's full parallel `-race` load, not a code defect, and
  that entry names `TestGrandIntegration` specifically as a repeat offender. The first failure was
  serious enough (a trivial single-`noop`-task runbook hanging) to verify past what the standing
  instruction technically requires: `git stash -u` reverted every uncommitted change from this
  session, the identical isolated test command was run against that clean baseline (pass, 12.91s),
  the stash was restored (`git stash pop`), and the identical command was run again against this
  session's own code (pass, 14.19s) — proving the hang was not reachable from this session's diff at
  all. Both later flakes (a clean full run, then the unrelated leader-election split-brain) were each
  reconfirmed passing in isolation the same way. No fix was needed or made for any of these; they are
  documented here because "make ci is green" should mean something more specific than "it printed
  PASS eventually."

**Next step.** Nothing blocking. Section 3b.2 (a job's "completed" state describing fan-out, not
execution) is next in the roadmap's own dependency order, and is explicitly a separate,
design-then-build phase (PLAN.md Sections 16-17 first) — do not start it assuming this session's
work belongs to the same commit. Tranche B's own remaining scope (B2's expandable row summary, the
real multi-badge activity strip) is still open and was not touched this session either.

**Files changed this session:** `pkg/wire/{dispatch.go,dispatch_test.go}` (Fields/ExtraVars),
`internal/dispatch/{worker_devices.go,worker_test.go}` (payload build site + tests),
`internal/adapters/legacy/{adapter.go,argv.go (new),argv_test.go (new),adapter_test.go}` (argv
construction, run timeout, tests), `internal/engine/{executor.go,cel.go,executor_variables_test.go
(new)}` (`ExecutorOption`, `WithVariables`, `WithTaskTimeout`, `"vars"` CEL root), `internal/adapters/
native/{adapter.go,fields.go (new),fields_test.go (new),adapter_test.go}` (ExtraVars/timeout wiring,
forks/limit doc comment, tests), `cmd/runner/ansible_release_gate_test.go` (`observingOrchestrator`,
new real-container test), `.SPECIFICATION/AWX_PARITY_ROADMAP.md` (Section 3b.1 closed),
`LESSONS_LEARNED`/`LESSONS_LEARNED_ARCHIVE` (#106).

---

Full session-by-session history (every `## Previous session: ...` and `## Files changed in the ... session` entry) lives in [`HANDOFF_ARCHIVE.md`](HANDOFF_ARCHIVE.md), kept out of this file so it stays cheap to read every session. Read the archive only when you need a specific past session's detail.

When Current Status above is superseded, move the outgoing text into `HANDOFF_ARCHIVE.md` as a new `## Previous session: ...` entry at the top of that file (before its current first entry), then overwrite Current Status here. Never delete a past entry.

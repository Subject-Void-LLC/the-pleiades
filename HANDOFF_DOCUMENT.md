# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**Branch `feature/job-cancel`, off `main` at `227fc9e` (the merge of PR #31). Item I, job
cancel, built end to end, plus the `running` job state and the result aggregation that state
had to stand on. Seven commits, NOT pushed.** Nothing is blocked.

### What a cancel now does, and what it honestly does not

Two halves with different guarantees, and the difference is stated in the endpoint
description, the UI and the commit messages rather than glossed over.

- **Authoritative:** the record and the fan-out. `JobStore.Cancel` is a compare-and-swap out
  of `pending`/`fanning_out`/`running` into `canceled`, stamping `canceled_at`/`canceled_by`.
  The fan-out loop learns of it because `RecordTask` refuses a canceled job, on a row it had
  already read for the fence, so it costs no extra query on a ten thousand device loop.
- **Best effort:** work already running on a device. A per-job core-NATS control subject
  reaches whichever Runner holds the job and cancels the `execCtx` `executeWithLease` already
  builds. Gated on `payload.Interruptible` exactly like the two existing abort triggers, so an
  un-abortable task finishes, per PLAN.md Section 16.

One device can still be dispatched to after a cancel lands, because the publish precedes the
`RecordTask` that refuses. The window is inherent rather than open: a cancel can arrive between
any publish and its write. The test asserts the overshoot at exactly one device.

### Two corrections to the plan's own sizing, both verified

- **There was no SQLite table rebuild.** Neither dialect constrains `jobs.state`; the enum is
  a Go-side validator. Widening it needed no DDL, and the migration carries only new columns.
- **`running` was the expensive half, not cancel.** Nothing consumed
  `pleiades.jobs.results.>`, and the Runner only published onto it when `RUNNER_WAL_DIR` was
  set. See FAILURE_PATTERNS #217.

### The bug worth not re-learning

`SubscribeCancel` returned before the broker had registered the subscription. A cancel is a
core publish with no queue, so one arriving in that window was dropped forever rather than
delivered late, and the window sat across the start of a run. FAILURE_PATTERNS #216,
LESSONS_LEARNED #181. The thing that corrected a wrong "it is just container flake" diagnosis
was RAISING the deadline: it then failed identically at thirty seconds.

### Where it stands

`make push-gate` was run to completion. Both test phases pass: the `-race` pass and the
integration pass each report passed, with their only failures confined to packages
`flaky-packages.json` already names and each warned rather than blocking.

**The one remaining blocker is environmental and cannot be fixed from the code.**
`coverage-check` reports two regressions, `internal/catalog/cloud/aws/ec2` at 49.2% against a
96.7% floor and `internal/catalog/cloud/aws/s3` at 48.0% against 98.0%. Both packages skip
their LocalStack-backed tests when `LOCALSTACK_AUTH_TOKEN` is unset, which accounts for the
whole of each drop; neither is touched by this branch. Set the token and they should return to
their floors.

That exposes a real gap in the gate itself, worth a decision separately from this work.
`tools/testgate` learned to tell "the test failed because its infrastructure was not there"
apart from "the test failed", which is what `flaky-packages.json` is. `tools/coverage-check`
never learned the equivalent distinction: it reads a percentage and nothing else, so a package
whose tests all skipped for want of a token is indistinguishable from one that is genuinely
untested. Until it can tell those apart, any environment without every piece of optional
infrastructure fails the ratchet for reasons that have nothing to do with the diff.

Negative controls were run for the assertions most worth doubting, and each fails when its
guard is removed: the fence-beats-canceled ordering, the fan-out stopping on a cancel, the
`interruptible: false` gate, the three HTTP client properties, and the result consumer's
subject.

### Next steps

1. A dogfood pass. The suite cannot substitute for it (LESSONS_LEARNED #174): launch a real
   job against a multi-device group, cancel it mid-fan-out, and confirm the remaining devices
   were never dispatched, the record page stops polling, and the badge reads canceled.
2. The legacy container adapter's cancellation was not verified. `internal/adapters/native`'s
   path is proven to tear down an in-flight SSH command (`pkg/remoteexec/conn.go` closes the
   session out from under `ssh.Session.Run`); whether cancelling the legacy adapter's `runCtx`
   actually stops a running container is unchecked, so a playbook job's cancel may reach the
   Runner and not the work.
3. **A job can now get stuck in `running`, and nothing reaps it.** This is the one real
   gap this work introduces rather than inherits. A job leaves `running` only when every
   dispatched device reports, so a dispatch that is dead-lettered after exhausting
   `MaxDeliver` produces a result that never arrives and a job that waits forever. The
   ordinary Runner death self-heals, because JetStream redelivers the dispatch to another
   Runner after `AckWait` and that one reports; it is exhaustion, not a single crash, that
   strands a job. `dispatch.Reaper` does not cover it: it looks only at `fanning_out`
   (`ListStaleFanOuts`), which is correct for what it was built for and means a `running` job
   is invisible to it. The shape of a fix already exists in that reaper, a leader-gated sweep
   over jobs whose `updated_at` is older than a bound, and the honest question to answer
   first is what a stranded job should become: `failed` naming the devices that never
   reported is the obvious answer, and it should not be `completed`.
4. **Nothing consumes dead letters, which is FAILURE_PATTERNS #217's shape a second time.**
   Found by sweeping every subject `internal/topology` declares for a subscriber, which is the
   check that finding should have left behind. `event.HandleDeliveryFailure` publishes to
   `topology.DeadLetterSubject(...)` (`internal/event/dlq.go`), and no consumer exists anywhere
   in the module; the grants carry publish rights and no subscribe. So a message that exhausted
   `MaxDeliverDefault` redeliveries, a job that could never be fanned out or a journal batch the
   store keeps refusing, lands on a subject nobody reads and is gone at `MaxAge`. Nothing
   alerts. `topology.go`'s own comment describes "one operator-facing consumer" watching all of
   them with a trailing wildcard, which is aspirational in the way the result subject's was
   until this session. Left as a decision rather than improvised: where a dead letter should
   land and who is told are product questions, and the answer is probably a durable consumer
   writing to a table the UI can show, next to the run journal.
5. A job whose every device's run failed still ends in `completed` with the tallies telling the
   story. That is unchanged from before this work rather than introduced by it, and it is worth
   a decision now that per-device results exist to base one on.

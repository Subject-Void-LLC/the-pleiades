# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**Branch `feature/section-row-actions`, 31 commits, off `main` at `227fc9e`, NOT pushed.**
`feature/job-cancel` is rebased underneath it, so the two are one linear history and that branch
is independently correct at `749c117`.

The session set out to continue the work list and did. The larger result is that **item I was
shipping a total outage, and five consecutive gate runs called it flake.** Read that first.

### The outage

Every job on `feature/job-cancel` hung in `running` forever, in every deployment rather than only
in tests. The Controller stamps a dispatch with the JetStream message id `"<jobID>:<deviceID>"`;
the Runner published that device's result under the byte-identical id. JetStream's duplicate
window is scoped to the STREAM, not the subject, and `internal/topology` puts every subject in one
stream, so every result collapsed onto the dispatch that had caused it.

It was silent in three independent ways. A suppressed duplicate returns `PubAck{Duplicate: true}`
with a NIL error; `internal/event` discarded that ack, so a dropped publish and a delivered one
were the same value everywhere above it; and the WAL then acknowledged the entry, destroying the
only retry. The selection it produced is why it read as flake: the window is `min(budget, 5m)`, so
FAST jobs hung and jobs slower than five minutes completed.

**The gate hole is the more transferable finding.** `tests/e2e` was waived per PACKAGE while its
waiver's own prose named exactly one test. Four tests failed on this in five consecutive
push-gate runs and every run printed `testgate: passed (warnings above)`. The previous session's
handoff recorded, truthfully and misleadingly, that both test phases passed. FAILURE_PATTERNS #222
and #224, LESSONS_LEARNED #185 and #186.

### What was fixed, and what each one cost to find

1. **The message-id collision** (`b119578`). One line, plus the log line that would have made it
   visible on day one, plus the real-broker test whose absence let it ship: both existing dedup
   tests published to a SINGLE topic, so both were equally consistent with per-subject and
   per-stream dedup, and they passed beside three comments asserting the wrong model.
2. **A second permanent hang** (`bbeb891`), masked by the first. A job whose devices all reported
   before its fan-out finished was parked in `running` with nothing left to end it.
3. **The flake gate** (`749c117`). An entry may now name its tests and then tolerates only those.
4. **The e2e harness** (`65b2d55`). It decoded every poll into one reused value, so an `omitempty`
   field plus an unordered array put one device's skip reason on another device's row; three
   investigations blamed the fan-out. It also asserted nothing the Runner reported, which is the
   capability this branch exists to add.
5. **A task list that reshuffled between identical reads** (`b697279`).
6. Item B's own three, recorded as FAILURE_PATTERNS #219, #220 and #221.

### Item B: the section write path's row half

`view.RowAction`, declared on a Section and addressed at `/{resource}/{id}/{action}/{row}`, with
the credential type as its consumer: an input and an injector can now be taken back out, where
both tabs were one-way doors and the only route back was the JSON API or the database.

**What it deliberately does not do, which is the rest of item B.** A row action does not prompt.
Removing needs no form; EDITING a row in place needs a form prefilled from that row, and
`RecordAction`'s form prefills nothing. **Item C's edit half waits on the same seam**, so it is
worth doing once, and it is the next piece of work.

### Where the gate stands

`make ci` was run to completion. `build`, `vet`, `fmt`, `tidy-check`, `test-race`, `test-repeat`,
`test-integration`, `gosec` and `govulncheck` all pass, and `tests/e2e` is clean. `docs-lint`,
`docs-gen-check`, `helm-lint` and `templ-gen-check` were run separately and pass, because `make`
stops at its first failure and would otherwise have left them unobserved.

`coverage` failed, on four container-provisioning races inside its own fourth full test pass:
`cmd/runner`, `internal/lock` and two in `internal/topology`. Every one is the documented
`connection refused` to an already-mapped container port, all four landed inside a 40-second
window, and **each was rerun in isolation and passes**, in 0.66s to 7.1s against 11s to 18s of
connect-retry under load. That last step is the discriminator this session exists to teach: the
e2e failures looked the same and reproduced five times out of five.

### Decisions left, not improvised

1. **A `running` job has no watchdog.** A single lost result is still an unrecoverable hang with
   no log line anywhere. `dispatch.Reaper` sweeps `fanning_out` only. The two known ways to lose
   a result are fixed; the class is not closed.
2. **A result can beat its own task row.** `worker_devices.go` publishes the dispatch before
   writing the row, so a result arriving in between hits `RecordResult`'s not-found path, which
   warns and ACKs, destroying the outcome permanently. Writing the row first would close it, but
   `worker.go`'s cancel path already reasons from the current ordering, so it is a deliberate
   decision rather than a swap.
3. **Eighteen flaky-packages entries are still unnarrowed.** Narrowing each is real work against
   real evidence, not a mechanical edit.
4. **Two further gate rules were proposed and not taken.** Never tolerate a package-level kill
   (a hang is not contention, though this repo has recorded a hang that genuinely was one), and
   re-run a warned test in isolation and escalate if it fails again. The second is the rule that
   would have caught this outage, and it changes the gate's wall-clock.
5. Everything the parked job-cancel branch left open is still open, including the dead-letter
   consumer and the dogfood pass. See the entry below this one in `HANDOFF_ARCHIVE.md`.

### Next step

The action-form prefill seam, which finishes item B and unblocks item C's edit half.

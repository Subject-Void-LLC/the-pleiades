# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**Branch `feature/Phase-40-Run-Journal`. Phase 40's twenty build-order steps of
`.SPECIFICATION/PHASE40_MASKING_DECISION.md` Section 8 are ALL DONE, including step 20's human
dogfood pass. `make ci` now clears every stage except the last test-running one: read "Where
`make ci` actually stands" below before claiming it passes.** Pushed and raised as a pull
request against `main`.

The dogfood pass was not a formality. It found two real defects that every existing gate had
been passing over, and the `make ci` work found three more things nobody was looking for.

### What step 20 actually did

A real Crawl-tier project (`pleiades init`, four hand-written runbooks, a real sshd container)
and a real Walk-tier deployment: the real `cmd/controller` and `cmd/runner` binaries, real NATS
with JetStream, org/inventory/device/template created through the HTTP API, jobs launched with
`curl`, rows read back through a plain `sqlite3` connection that never touches ent.

**Two defects found, both fixed, both with a test that fails without the fix.**

1. **`FAILURE_PATTERNS.md` #209: a multi-device job silently lost skipped-task journal rows.**
   A row is identified by `(job_id, device_id, attempt, node_id)`, which assumes every entry
   names a device. A skipped task, a controller-side task and the synthetic parallel marker
   name none, so every dispatch of one job produced the identical key and the store discarded
   all but the first as "already recorded". Four identical two-device launches gave 5, 6, 6 and
   5 rows: completeness depended on whether JetStream happened to redeliver, since a different
   attempt separates the keys. Fixed in `internal/adapters/native/journal.go`, beside the
   `JobID` and `Attempt` the publisher already stamps.
2. **`FAILURE_PATTERNS.md` #210: one run spelled "no keys" two ways.** `normalize` exists to
   prevent exactly that and was applied at the file sink only, so the Walk tier stored the JSON
   scalar `null`. SQLite hides it. The columns are `jsonb`, and PostgreSQL refuses
   `jsonb_array_length('null')` outright, measured against a real server, so an operator's query
   failed on precisely the rows where a task recorded no keys, which is every failed task.

`LESSONS_LEARNED.md` #174 is the generalization: a suite is blind to any defect needing two of
its conditions at once, because a fixture isolates one and neutralizes the rest. Defect #209
needs more than one device AND a node that resolves none. Every gate dispatches one device and
no gate's runbook has a `when:`, so nothing held both.

Three claims the pass CONFIRMED rather than broke, worth not re-deriving: SIGINT to a real
`pleiades run` mid-level lost the terminal's entire output and kept every completed level in
the journal file; Book 10's documented `jq` recipe works verbatim; and five sentinels planted
through four routes reached the device and `--verbose` and neither journal, with 102 Walk rows
re-scanned clean afterward.

### The `make ci` blocker, which was not what the last handoff said it was

**The two packages the previous handoff named are fine.** `internal/catalog/pleiades/builtin/wait`
and `pkg/remotefile` passed every run. No `flaky-packages.json` entry was warranted or added.

The real blocker was the gate's own parallelism. `go test` defaults `-p` to GOMAXPROCS, 20 on
this host, and `DOCKER_DEPENDENT_PACKAGES` names 22 packages, so one Docker daemon was asked to
start twenty packages' containers at once. Seven different packages failed across two runs and
every failure was the daemon's own: a `containers/<id>/json` inspect exceeding its deadline
after 553 retries, a published sshd port answering connection refused, a NATS container never
reachable. All seven were already in `flaky-packages.json`, which `make ci` deliberately
ignores, so the waiver path could not have produced a clean run even in principle.

Fixed in the Makefile: `test-race` and `test-integration` run the container packages at
`DOCKER_TEST_PARALLELISM`, which is 1. Each half is an intersection with the tag-appropriate
`go list`, because naming a package explicitly is not the same as matching it with `./...`
(`tests/e2e` and `internal/ent/migrate/gen` are `[setup failed]` when named).

**`make ci` now takes noticeably longer.** The container packages run in sequence rather than
together. That is the price of a gate that can pass at all.

### One security finding, taken rather than filed

`govulncheck` flagged three vulnerabilities this module's code actually reaches, all in
`golang.org/x/crypto/ssh` at v0.54.0: GO-2026-6355 and GO-2026-6354 (DoS on a deadlocked SSH
channel, reached from `realDial`'s `ssh.NewClientConn`, which is every SSH connection this
platform makes) and GO-2026-6303. Bumped to v0.56.0, which is clean. It raises the `go`
directive from 1.25.0 to 1.26.0 because x/crypto and the x/ modules it pulls forward declare
it; `toolchain go1.26.6` was already pinned, so nothing about building here changed.

This is the one failure CLAUDE.md says a local run cannot predict: the advisory database is
live, so the gate can fail tomorrow on a tree nobody touched.

### The one thing left open, deliberately not waived

`pkg/remoteexec`'s `TestConnect_HopChain_StressManyConcurrentSessions` failed ONCE, during one
`test-repeat` sweep, with 1 of 300 concurrent sessions reporting
`ssh: unexpected packet in response to channel open: <nil>`. That `<nil>` is `%T` of a nil
message, which is what a receive on a closed mux yields, so that session's connection died
between handshake and channel open.

What is established: it is load dependent (five isolated `-count=3` runs pass), it is not a
`-count=3` state bug, and it is not a shared-client race, because `Run` calls `Connect` per
session and each of the 300 has its own bastion connection. What is NOT established is whether
it is ours or x/crypto's. The two deadlock advisories above are literally about concurrent SSH
channels and were fixed in the version now taken, which is a plausible match and not a proven
one.

**It is deliberately not in `flaky-packages.json`.** The diagnosis is unfinished, and a waiver
written on an unfinished diagnosis is the blind entry that file's own header warns produces a
package nothing checks anywhere. If it recurs, that is real evidence; treat a recurrence as a
reason to investigate rather than to waive.

### Where `make ci` actually stands, precisely

**Green, measured, repeatedly:** `build`, `devtools`, `vet` (both tag sets), `fmt`,
`tidy-check`, `test-race`, `test-repeat`, `test-integration` (`tests/e2e` included, 278s),
`gosec` (9 findings, all waived) and `govulncheck` (clean after the bump). Every one of those
had failed or been unreachable on this branch before.

**Still red: `coverage`.** It is the one stage the parallelism fix above does NOT reach.
`tools/coverage-check` runs its own fourth full `go test ./...` with no `-p` of its own, so
the daemon saturates exactly as `test-race` used to, and three container packages
(`internal/lock`, `internal/runner`, `internal/topology`) failed there with the same
10.8-second container-start shape. `make ci`'s coverage stage is deliberately non-tolerant, so
a listed package failing still stops it.

This was left undone on purpose rather than bolted on at the end of a long session. The fix is
a real design decision with three parts that want review: where the container package list
lives if a Go tool needs it too (the Makefile's own comment argues hard for ONE named list and
against a derived one), whether the Makefile may hand that list to a tool that feeds it to
exec.Command (`tools/coverage-check` currently refuses to read even its timeout from the
environment, citing G204 taint), and how two coverage maps merge. `flakegate.RunGoTestJSON`
hardcodes `./...` and has two callers, so its signature changes too.

**The fourth failure in that same stage was NOT contention and is fixed:**
`FAILURE_PATTERNS.md` #211, a stress test racing a fixed 50ms sleep. It appeared immediately
after the x/crypto bump and looked exactly like a regression; a worktree pinned to the old
v0.54.0 reproduces it identically at `-count=200`, so the bump is innocent. 500 repetitions
pass after the fix.

### Also this session

`.AGENTS/AGENTS.md` gained a **Security Findings** section, at the user's request: report at
the "could be" threshold rather than "proven", as a named finding with five specifics, and
never disclose a third-party vulnerability outside this repository on your own. That file is
gitignored, so it is not in the pull request.

### Commits

Seven on top of the previous handoff: three for the dogfood defects and their documentation,
one for the sshd readiness race in `cmd/pleiades`'s gate helper, one for the Makefile
parallelism split, one for the x/crypto bump, one for the stderr stress-test race.

**The seven `c5ddb20` through `554da39` UI commits are still an unrelated side quest** on this
branch (the web UI's appearance system). Flag them separately if this branch is ever split.

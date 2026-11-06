# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**2026-11-06. Branch `fix/docker-desktop-lost-ports`, from `main` at `4f26e95d` (PR #119 merged), in the worktree
`/home/noot/auto-roboto-sweep`. Two commits: `e16a1337` (a published port lost to a socket of ours fails at once) and
`4d2d0cb4` (`make doctor` says when the ephemeral range overlaps Docker Desktop's ports). Not yet through pr-check,
push or a pull request.**

### What works

- **FAILURE_PATTERNS 470, 471 and 481 are diagnosed and fixed**, and 483's local sightings are the same cause. Docker
  Desktop's engine picks host ports from about 32768 up in a network namespace of its own; its WSL-side forwarder then
  cannot bind a port a socket of the tests' own holds (the kernel's ephemeral range, 32768 to 60999), logs one
  warning on the Windows side, and never retries. Proof: with 8,000 even ports held, 8 of 8 containers on held ports
  refused and 32 of 32 others answered; none recovered within 130 s. CI's native engine re-picks, so it cannot happen
  there. Full detail in the archive entry for 470; the first proof read wrong because of 942.
- **The machine setting.** The owner applied `/etc/sysctl.d/60-pleiades-docker-ports.conf`
  (`net.ipv4.ip_local_port_range = 20000 32767`). Afterwards 40 of 40 containers answered with 8,000 sockets open.
- **The fast failure** (`e16a1337`, `internal/testsupport`): a refused host port held by a socket of this user fails
  the greeting wait at once with `ErrPublishedPortLost`, naming the holder and the fix; `ForPublishedPort` runs in
  `PostgresReady`. 6 of 6 real testcontainers containers on held ports failed in about 200 ms with the cause, against
  two minutes before. Tests: `TestSocketsOwnedOn`, `TestGreeting_FailsAtOnceOnAPortASocketOfOursHolds`,
  `TestForPublishedPort`.
- **`make doctor`** (`4d2d0cb4`, `checkPortRange` in `tools/doctor`, tested in `tools/doctor/checks_test.go`) reports a
  fix on Docker Desktop on Linux when the range reaches 32768.

### Current blocker

None. Next is pr-check for this branch.

### Open

- (a) Readiness paths without the fast failure (fp-reviewer, 2026-11-06): `ForHTTP` waits (toxiproxy, LocalStack,
  Batfish, GNS3, Vault, the ticket and ServiceNow gates), `ForListeningPort`-only sites (telnetd, k3s, the gRPC gate,
  the e2e WSS mesh) and testcontainers' Ryuk reaper. Not made worse, not helped; `make doctor` names the machine fix.
  FAILURE_PATTERNS 602 (GNS3 API silent once) is a plausible repeat, unproven. The ephemeral range is now 12,768
  ports (20000-32767) rather than about 28k, which raises 474's exhaustion odds under parallel load; watch for it.
- (b) `stash@{0}` ("WIP on ssh-sweep-b"): awaiting the owner, its own PR or drop it (detail in the previous entry).
- (c) Carried from Phase 46's run settings (previous entry's (a) to (c)): the CLI journal does not record which
  `--setting` a run used; no end-to-end test takes `method_settings` from a launch or the rollback route to a Runner;
  a run setting on a concrete method behind a generic `svc.*`/`pkg.*` dispatcher is never filled.
- (d) FAILURE_PATTERNS 483's CI sighting stays open; it is not this cause.
- (e) The local failure ledger's sightings of 470, 471, 481 and 483 are not yet annotated with their pattern numbers
  (the ledger lives in the main clone).
- (f) Carried: the owner question on dating a value `facts.Resumed` writes back (before 41d), and the items in the
  archive's previous entries.
- API credit: $62.39 left, expiring 2026-11-08.

### Next, in order

1. `make pr-check` through the monitor, push, `make branch-containers`, the pull request. Merge only when green,
   `record-actions`, pull `main` and clean.
2. The facts churn PR, sweep items 4, 5, 6 and 10.
3. Phase 41, 41b to 41g, then 117b, 117c, 105, then release 0.4.0 (no tag until the owner says).

### Files changed on this branch

`internal/testsupport/` (`greeting.go`, `hostport.go` new, `hostport_internal_test.go` new, `readiness.go`),
`tools/doctor/` (`main.go`, `parse.go`, `checks_test.go`). Records (uncommitted): `FAILURE_PATTERNS.md` and its
archive (470, 471, 481, 483 resolved or updated; 942 new), `LESSONS_LEARNED.md` and its archive (401),
`CI_LESSONS.md` (28), `HANDOFF_DOCUMENT.md`, `HANDOFF_ARCHIVE.md`.

Machine: changed. The owner added `/etc/sysctl.d/60-pleiades-docker-ports.conf`, moving the ephemeral port range to
20000-32767; the memory note `wsl-ephemeral-port-range` records it, what to check if 470's shape returns, and the undo
(remove the file, `sysctl -w net.ipv4.ip_local_port_range="32768 60999"`). Lab: unchanged; no lab VM use was reported
for this session.

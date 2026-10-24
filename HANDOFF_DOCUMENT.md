# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**2026-10-24. Branch `feat/virt-cml`, off `main` at `0caadb17` (PR #89's merge). Phase 127d: `virt.cml` plus pr-check
gate work, to the plan the owner approved. Nothing built yet; no commits. Phase 127d has no section in the
implementation tracker yet. Tracker: 127c is 5 of 11 done, 127b 15 of 24.**

### What works

- Everything PR #89 merged (`HANDOFF_ARCHIVE.md`, the entry just above): Actions run 40117974279 green at `b897136b`,
  and local `make pr-check` green there on Linux, macOS, FreeBSD and Windows.

### Blocker

- None.

### Open

- `TestCheck_RealRoutersAgainstRealBatfish` failed once in CI shard 5 (vtysh "% command failed" during router setup,
  likely before FRR's daemons were up); annotated in the Actions ledger as the 575 family. Its fixture needs a
  readiness wait.
- `TestCLI_ImportGNS3ReachesANodesConsole` needed retries three times locally (FAILURE_PATTERNS 629).
- Still open from earlier: 618's hard link residual, 574, 610, 611, 612, 602, 598, 562, 560, 551, 470, 471.

### Next, in order

1. Gate work first:
   - `tools/prscope` adds the direct importers of changed packages, test imports included (transitive would be 209
     of 336 packages for `pkg/netcli`).
   - Every pr-check record is stamped with its commit, a clean flag and a time; `tools/prreport`'s merge flags a
     stale record.
   - `tools/gatereceipt` requires `report.json`'s pass for HEAD with nothing missing, and bounds the 24 hour age by
     the oldest record.
   - `make pr-check LEGS=...` reruns the named legs with their dependents.
   - `go run ./tools/failures watch-pr N` polls a pull request's checks and records them.
   - The report flags an Actions-only failure outside pr-check's scope.
   - Dropped: the `[ci]` title marker (no title uses it).
2. `virt.cml`:
   - `tls_fingerprint` in `pkg/devicetls` (moved from `pkg/cml`); `pkg/cml` writes, with fuzzing.
   - A `cml_controller` device type (`CMLAPICapable`, https, its own credential).
   - `pkg/iossecret` type 8 and 9, proven against real IOS XE hashes; the IOS login seed (secret 9; an enable secret
     only when the credential has an enable password).
   - Ten methods through `internal/forge/catalogdata`.
   - Owner decisions (2026-10-24): `lab.start` records the nodes it started, and its undo stops only those
     (`lab.stop` gains a `nodes` param). Console access is `transport: console`, with `console_server` and
     `console_line` on the node device; the engine resolves the controller's credential for the hop as `route`
     does, logs the line out on close, and never pools it.
3. Proof: the DevNet CML sandbox, then the blind CVD lab in `~/pleiades-cvd`.
4. Also on this branch: correct `examples/routing_lab/GAPS.md` line 213 (owner approved); move the docker-clean
   skill and docker-cleaner agent into the repository's `.claude/`.
5. After this branch: the release tooling PR, then Phase 117b part 3, then Phase 126.

### Files changed

None beyond the records (`HANDOFF_DOCUMENT.md`, `HANDOFF_ARCHIVE.md`).

### Lab state

Unchanged since PR #89: `bsd-check`, `win-check`, `lab-dc1` and `lab-lin1` are saved. `~/pleiades-lab/bin/pleiades`
is built from `main` at `0caadb17` (the previous one kept as `pleiades.old`).

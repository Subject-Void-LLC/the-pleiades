# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**2026-10-19. Branch `feat/pr-report`, off `main` at `34dff1f9` (#74 merged green).** One pull request at a
time; `make pr-check` before every push; the owner merges.

### What #74 shipped

- `internal/archtest`'s `TestEveryDocumentedExampleValidates`: every registered method's examples go through the
  builder and `pleiades validate`. Nineteen examples were fixed.
- The box steadiness check, and `need.POSIXShell` on every test that uses the `/bin/sh` server, with a guard.
- The fixes FreeBSD and Windows found (FAILURE_PATTERNS 515 to 521).

### This branch: one record of a pr-check run

The owner asked whether to build an LSP to help read results; the answer planned with them was a JSON report.

- **`testgate -report-out`** writes `flakegate.Run` however the run ends. It holds:
  - the arguments, packages, time and verdict;
  - each failure, with its outcome (failed, contention or not re-run) and its masked output;
  - each skip, with its reason.
- **Each OS check's `-report-out`** writes `oscheck.LegRecord`:
  - the machine, the commit, how the leg ended, and the step it stopped at;
  - the testgate records its machine wrote, read back from it;
  - a check with no machine named records that at the settings step.
- **`tools/prreport`** has three commands:
  - `step` records a step's exit code, or why it did not run;
  - `merge` writes `.coverage/pr/records/report.json` with pr-check's own exit status, and prints the summary;
  - with no command, it reads the report back: `-json`, `-failures`, `-skips`, `-leg NAME`. A report for
    another commit says so.
- **The Makefile's pr-check** writes every record and ends with the merge; the gate's semantics are unchanged.
- **Found by the failure-pattern sweep before push, both fixed:**
  - the run record kept skip reasons unmasked, beside a masked ledger (FAILURE_PATTERNS 522, a repeat of 417);
  - prreport dropped stray arguments and contradictory flags (523, a repeat of 358).
- **Docs:** `docs/design/os-checks.md`'s "The record of a run", `CLAUDE.md`, and
  `changelog/pr-check-report.added.md`.
- **Proven so far:** the unit and live tests (testgate 77.9%, floor 70.3; prreport 92.5%, no floor yet), and the
  recipe's step helper and merge in a real `sh`. The first real end-to-end run is this branch's own pr-check.

### Next

1. `make pr-check` on this branch, then read its report with `go run ./tools/prreport`. Then push, and open the
   pull request with `-R`.
2. **Every method proven on every OS it targets** (the owner, 2026-10-18: "Since we have all targeted OSes
   available, there is no longer an excuse for a module not working"):
   - an audit of which methods have run through `pleiades` on which lab box;
   - then the gaps closed method by method.

   It's tracked in Phase 122b.
3. **Packages, by the owner's direction of 2026-10-18** (the tracker's Phase 122b item holds the edge cases):
   - Chocolatey, Scoop and PSResourceGet for Server Core, the user picking, after a design note;
   - whether `win.package` stays is open;
   - `pkg.winget.*` for Desktop Experience only;
   - FreeBSD's `pkg(8)`.
4. **Phase 122b's other open items:**
   - `validate` is silent on `always:` and `rescue:`, which parse and never run;
   - the converter maps no Windows module;
   - `win.feature.*` parses DISM's state without `/english`;
   - there is no archive method for Windows;
   - run output does not stream;
   - `tools/maccheck` has no coverage floor;
   - `file.copy` from the Controller (the Walk tier) is still refused.

### Lab state

- `bsd-check`, `win-check`, `lab-dc1` and `lab-lin1` are saved. Leave the check boxes saved between checks.
- `win-lab` (not a check box) is often running, and the host keeps 1 GB, so don't run both check boxes at once.
- The domain's runbooks are in `~/pleiades-lab/runbooks/win/` (`dc-00` to `dc-04`); the archive's previous entry
  has what building it found.

### Files changed this session

- `tools/internal/flakegate/run.go`, `tools/testgate/{record.go,mask.go,main.go,options.go}` and their tests;
- `tools/internal/oscheck/{report.go,flow.go,box.go,posix.go}` and their tests;
- `tools/{wincheck,bsdcheck,maccheck}`;
- `tools/prreport`;
- the Makefile, `docs/design/os-checks.md`, `CLAUDE.md`, the changelog fragment;
- `FAILURE_PATTERNS*` and `HANDOFF_*`.

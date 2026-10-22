# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**2026-10-22. Branch `feat/doc-create-skill`, off `main` at `f5a0a22a` (#85).** Not pushed; no pull request yet. One
pull request at a time; `make pr-check` before every push; the owner merges.

### On this branch since the last handoff

- `76de5ce3` sealed password files: `pleiades seal-key`, `pleiades seal`, `add-credential --password-file`
  (`internal/sealed`, `internal/credential/seal_key.go`, `cmd/pleiades/seal.go`).
- `c2bb3105` `file.stat`, read-only; a path starting with a dash reaches `stat` and `readlink` as `./path` (569).
- `8d43d229` `identity.user.create`'s undo removes the home it made (`created_home`, 570); `pkg.apt.install`'s undo
  names what it pulled in, at most 128 (565); a missing group makes a check unchecked (564).
- `1a75916a` the skill teaches sealed files, read-only looks and an agent journal; `run`, `adhoc` and `rollback` usage
  lines come from their flags (568); `docs/02-get-started.md` gains the sealed file section and its limits.
- Proof: every test run locally passes; touched packages are at or above their floors; `internal/sealed` is new, at
  91.7%, with no floor. Six agent dry runs (Copilot CLI, Antigravity CLI, a blind `claude -p`) set up a Debian server
  from `SKILL.md` and undid it from a fresh conversation; timings in `.IGNORE/timings.md`.
- Records: FAILURE_PATTERNS 563 to 571, LESSONS 304 to 308.

### Settled before the PR

- **563, found while writing these records.** Recording the undo first was not enough: the engine journals no undo for
  a failed task (`failedStats`), and each method still returned the later read's error. Fixed by 452's other half:
  once the change completed, a failed read is a warning and the task succeeds with its undo (a follow-up commit on the
  branch). Proven at the method boundary with the success the engine journals; no real-device fault injection.

### Open

- 560, 551 and 561 as before; 562 as written. On `adhoc --help`, two reviews recommended exit 0 with its flags; the
  owner decided no change (2026-10-22).
- 567: `internal/credential/master_key_test.go` has unguarded POSIX mode checks; pr-check's Windows leg will show.
- `add-registry-credential` and `add-database-connection` take no sealed file (a scope note, not a defect).
- Agents left read-only `adhoc` run ids out of `AGENT_JOURNAL.md` in 4 of 6 rounds; harmless for undo; skill unchanged.
- The dry-run report is a Claude Doc, "Agent CLI Dry Runs", with a Markdown copy outside the repository.
- Carried, not rechecked: no command lists a device type's properties; `pleiades init`'s README text.
- **Security finding, the owner's call:** `pleiades init` writes no `.gitignore`, and `docs/02-get-started.md` still
  says `.pleiades/` is "gitignored by default". Proposed as its own PR after this one.

### Next

1. `make pr-check`, push, open the PR, CI, the owner merges.
2. Then the owner picks the order of 117b part 2, the segmenter (`feat/segmenter-117b`, which triggers the census,
   then part 3), and Phase 126, backups an agent can take, move off the host and prove (added 2026-10-22; its first
   item is the owner's decision on where an off-host copy lands, against Phase 125's rule that object content never
   lands on the machine running pleiades).

### Lab state

Not reported for this session. The last record said `bsd-check`, `win-check`, `lab-dc1` and `lab-lin1` were saved.

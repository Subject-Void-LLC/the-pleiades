# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**2026-10-23. Branch `feat/lab-importers`, off `main` at `62be86d4` (PR #88's merge). Phase 127c's importers and
Telnet back end, the IOS checkpoint undo, Genie's IOS parsers in Go, `http.wait`, nine routing lab gaps, and the fixes
from a whole-branch failure-pattern review. Tip `01cdbb70`, plus the uncommitted records for it. Not pushed; no pull
request yet. Tracker: 127c is 5 of 11 done, 127b 15 of 24.**

### Built and proven

- Everything in the previous entry (`HANDOFF_ARCHIVE.md`).
- `01cdbb70`: the fifteen findings of the fp-reviewer agent's whole-branch review, FAILURE_PATTERNS 613 to 627, each
  with its test named in its archive entry. Among them: `net.ios.replace` deletes only checkpoints it made (613,
  `TestReplace_NeverDeletesAFileItDidNotMake`); `Descriptor.UndoCall` (614); a Telnet password is sent once (617);
  `project.write`'s path reads only `vars` and `.pleiades` is matched by identity (618); Telnet reads bounded by time
  and raw bytes (620).
- Not yet run: the Windows-only tests from 615, 616 and 618 (`TestOthersMayWrite_IgnoresAModeThisSystemDoesNotKeep`,
  `TestModeHolds_ComparesOnlyTheReadOnlyBit`, `TestProjectFiles_RefuseThePleiadesShortName`). They compile for
  Windows (`GOOS=windows` vet).

### Open

- 618's residual: a hard link at the project root to a file inside `.pleiades` can still be read (needs write access
  to both directories).
- 574 stays open: `TemplateRule` now reaches `rollback:` steps, but no test writes a misspelled register in one.
- 610, 611, 612, 602, 598 (other commands), 562, 560, 551, 470 and 471 as before.
- Not built, as before: Walk-tier enable secret, onboarding a `--via telnet` node, `until`/`retries`, 127c's fuzz,
  adversarial, security and hardening gates; the routing lab items listed in the previous entry.
- `tests/cml/.env` is valid until about 2026-10-25 (LESSONS 315).

### Next

1. Commit the records.
2. `make pr-check` with `PR_CHECK_PARALLELISM=1`, under the memory cap. Its Windows leg is the first real run of the
   Windows tests from 615, 616 and 618.
3. Push and open the pull request; stay on it until it is green.
4. Then the `virt.cml` pull request, then release tooling (which must ship `LICENSE.genieparser`).

### Files changed (since the last records)

`01cdbb70`: `internal/catalog/net/ios/` (`checkpoint.go`, `replace.go`, `config.go`), `internal/catalog/net/trace.go`,
`internal/catalog/pleiades/builtin/project/write.go`, `internal/engine/` (project guard, project mode, render),
`internal/journal/` (`active.go`, `reader_unix.go`, `reader_other.go`), `internal/validate/` (`param_tasks.go`,
`template_rule.go`, `vars_rule.go`, `rollback_rule.go`), `pkg/collection` (`UndoCall`), `pkg/telnetexec`, `pkg/cml`,
`pkg/gns3`, `internal/testsupport/gns3.go`, `internal/inventory/file_repository.go`, `cmd/pleiades` (add-credential,
importers, run pipeline, validate), docs, the generated reference, changelog fragments. Records: FAILURE_PATTERNS 613
to 627 (and an update to 574), LESSONS 321, changelog `project-files-windows-short-name.security.md`.

### Lab state

Unchanged: no lab VM was touched; `bsd-check`, `win-check`, `lab-dc1` and `lab-lin1` are saved as last recorded.

# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**2026-10-30. Branch `fix/release-notes-links-and-prune-flake`, off `origin/main` at `7e40c5e1`. Uncommitted, not
pushed, no pull request; `make pr-check` has not run on it.**

v0.3.0 (PR #93) was merged and tagged on 2026-10-29: the tag is on `7e40c5e1`, with six archives and `SHA256SUMS`. The
previous status (the pipeline cut, the headroom policy, the lab data disks, the resources manifest) shipped inside it;
its detail is in `HANDOFF_ARCHIVE.md`. Only what is still open is carried below.

### What works (this branch)

- **Release notes links (FAILURE_PATTERNS 737).** The v0.3.0 release page's links all 404ed, because the workflow
  published `docs/releases/<tag>.md` verbatim and a release page resolves a relative link against the repository
  root. `tools/release/notes.go` (`rewriteLinks`) pins each relative link to the tag; `release -notes ... -repo ...
  -out dist` writes `dist/RELEASE_NOTES.md`; `make release-notes VERSION= REPO=`; `release.yml`'s build job runs it
  as its first tag-only step and publish uses its output. Proof: the `tools/release` tests, including
  `TestRewriteLinks_TheRealV030Notes` and `TestRunNotes`; `make release-notes VERSION=v0.3.0
  REPO=Subject-Void-LLC/the-pleiades` reproduces the live, hand-patched body but for two trailing blank lines;
  actionlint is clean. **Not proven:** the workflow's own tag path, which only the next tag exercises.
- **FAILURE_PATTERNS 560 seen a second time, instrumented, not fixed.** Nightly run 41058796153 (schedule, `main` at
  `7e40c5e1`, job 124072978969, strict) failed on only `internal/catalog/container`
  `TestPrune_RemovesWhatItsCheckListed` (`read_test.go:222`), the same words as PR #84's sighting on 2026-10-22.
  300 consecutive local runs (`-race -tags integration -count=300`, Docker 29.8.0) all passed, so the cause is still
  unproven and no fix was made. `harness.listed` (`harness_test.go`) and the failing assertion now print the
  engine's list as prune asks it, the list by label alone, inspect's state, and both again a second later. The
  next sighting shows whether the status filter hid the container and whether a list a second later holds it (not what the check itself saw). LESSONS 357.
  Nothing else in that nightly failed (skips only).
- No changelog fragment: this branch is CI plumbing and a test.

### Open

- FAILURE_PATTERNS 560 (above), open until a sighting prints the engine's list.
- FAILURE_PATTERNS 734: the FreeBSD lab VM reset itself mid-run and the next start emptied the console log, so the
  cause is lost (most likely 735's damage; not proven).
- `make resources-record` after the next gate, and after CI's artifacts are downloaded; then set `complete: true`
  in `tools/testgate/resources.json` once every package is in it.
- Compaction (`VBoxManage modifymedium --compact`) is not a method yet.
- The older items are unchanged: Phase 131 R4 waits on Phases 87, 88, 89 and 127d; the Phase 87 to 89 gates; the run 7
  items.
- `.SPECIFICATION/IMPLEMENTATION.md` holds a new proposed Phase 132 (exposure and hardening guidance, a design note
  from CISA's Installed Base Program; six unticked items; not approved). That file is gitignored, so it is not in
  the branch.

### Next, in order

1. Commit this branch, run `make pr-check` through the monitor, push, one pull request. Name the failure-pattern
   classes in the PR body.
2. After it, the queue the notes give: 117b part 3; Phase 126 (the owner chose option A, device to object storage);
   the patch compatibility contract follow-up (define "breaking" per area, enforce 0.3.x against the v0.3.0 tag's
   generated schemas); release tooling's remaining gaps.
3. A 0.3.x release may add, never break.

### Files changed this session

`tools/release/notes.go` and `tools/release/notes_test.go` (new), `tools/release/main.go`, `Makefile`,
`.github/workflows/release.yml`, `internal/catalog/container/harness_test.go`, `internal/catalog/container/read_test.go`;
records: `FAILURE_PATTERNS.md`, `FAILURE_PATTERNS_ARCHIVE.md` (560 updated, 737 added), `LESSONS_LEARNED.md`,
`LESSONS_LEARNED_ARCHIVE.md` (357), `HANDOFF_DOCUMENT.md`, `HANDOFF_ARCHIVE.md`.

Lab: not touched this session and not rechecked. The last session left bsd-check and win-check powered off (shut down through ACPI, not saved).

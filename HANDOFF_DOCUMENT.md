# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**Branch `feature/Phase-40-Run-Journal`. Build steps 1 through 19 of
`.SPECIFICATION/PHASE40_MASKING_DECISION.md` Section 8 are DONE, committed, and green. Only step
20 (the human dogfood pass) and a full clean `make ci` run remain before this phase is finished.**
Full detail on everything that closed this session -- steps 17 and 19, the "Corrections"
paragraph, and an unplanned but real UI side quest -- is in `HANDOFF_ARCHIVE.md`'s most recent
entry; read that before starting, not just this summary.

### The two things actually left

1. **Step 20, the human dogfood pass.** Not started. The spec's own words: "Do the human
   dogfood pass before checking anything off." `register_mask` once shipped with every one of
   its own tests green while masking nothing, because every test shared the wrong path
   assumption the bug had. This means actually running `pleiades init`, writing a real runbook,
   running it, and reading the resulting journal (the Crawl-tier `.jsonl` file, and the Walk-tier
   `journal_entries` rows via a real Controller/Runner pair) with fresh eyes -- not re-running
   the existing automated suite and calling that the dogfood pass.
2. **A full, clean `make ci` run.** Never completed on this branch. There is a real, previously
   diagnosed blocker, not an environmental one: `internal/catalog/pleiades/builtin/wait` and
   `pkg/remotefile` fail under full parallel `-race` load from what looks like real TCP-port
   contention (neither imports testcontainers; both pass in isolation). Reproduce once under a
   real full `make ci`, capture the actual failure text per package, then decide -- fix the
   contention, or a `flaky-packages.json` entry with a real written reason. Do step 20 first:
   it is more likely to surface something worth fixing than a second `make ci` run is.

Nothing on this branch is pushed. Working tree is clean as of the last commit below.

### Commits since the branch started (newest first)

```
554da39 fix(ui): let a theme button's own label break, not just the row
62dc6c8 feat(ui): make WCAG AA a guarantee of accessibility mode, not of a skin
89fe5f3 fix(ui): stop the fourth theme button overflowing its own row
50b4f15 feat(ui): add Ventanas Once, a fourth skin for Windows 11 parity
c71dc33 fix(ui): make Las Ventanas Windows 95 and Honeycrisp macOS on sight
1234f40 test(runner): add the Walk-tier journal's redelivery gate (Phase 40 step 17)
409a799 fix(ui): give Honeycrisp real shape, not just Apple colors
abffdfd docs(journal): add the field reference for one journal record
c5ddb20 feat(ui): add a third skin, macOS-like and built from Apple system colors
86bf22e docs(journal): document the sink struct fields, and use American spelling
611f8ee fix(pleiades): open the run journal before the command prints anything
3d8c746 fix(meshid): grant the run journal's dead letter subject to the Controller
63ca33a fix(journal): give the write probe a unique name, so two runs cannot abort each other
9aad536 docs(engine): correct nine doc comments citing things that no longer exist
6b078fa docs(handoff): record Phase 40 steps 1 through 16, and correct the blocker
ab08e70 fix(journal): acknowledge a batch the store can never accept (FP #208)
631f23e docs(journal): document the run journal, and correct a claim false since Phase 16
f19602f feat(journal): store the Walk tier's run journal and consume it
b0826f5 feat(journal): publish the Walk tier's run journal onto the job's subject
8fe51b3 feat(journal): write the Crawl tier's run journal to disk
1060ad0 docs(lessons): record what a "no input survives" fuzz assertion needs
533d0b9 feat(engine): record every node execution in a run journal
525fa22 feat(commitgate): refuse a commit that breaks a rule a machine can check
ef7c96a test(archtest): forbid internal/engine from importing internal/ent
```

**`c5ddb20` through `554da39` (seven commits) are an unplanned side quest**, not Section 8 work:
the web UI's appearance system, requested mid-session. It is real, tested (the full WCAG contrast
matrix, now a11y-mode-gated by explicit product decision -- see the archive entry for why), and
committed on this branch, but it is unrelated to the run journal. Flag it separately if this
branch is ever split before merging to `main`.

### Everything else

Steps 1-16 were done, committed and green as of the previous handoff. Steps 17-19 and the
"Corrections" paragraph closed this session; the archive entry above has the specifics (which
files, which tests, which real defect step 18 found and how it was fixed). Don't re-verify these
from scratch -- they were checked against the actual code this session, not just read from a
stale doc.

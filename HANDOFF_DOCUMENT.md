# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**2026-10-22. Branch `chore/prscope-census`, off `main` at `14b64c16` (#84, 117b part 1, merged green).** One pull
request at a time; `make pr-check` before every push; the owner merges.

### On this branch

- **`tools/prscope` census (559, a repeat of 550; LESSONS 303):** a non-test change under `internal/validate`,
  `internal/engine`, `pkg/collection`, `pkg/capability`, `internal/catalog`, `internal/inventory/devices` or
  `examples/` adds `cmd/pleiades`, and says why on standard error. A trigger with no `cmd/pleiades` is refused.
  `TestCensus_*` fail on the old prscope; `TestCensus_TheRuleNamesThisModulesRealPaths` holds the list to this tree.
- **Records:** part 1's handoff entry; FAILURE_PATTERNS 560 (open), the prune check that listed nothing while
  inspect said "exited".

### Open

- 560 and 551, causes unproven.
- A triggered pr-check runs about 15 minutes longer (`cmd/pleiades` in the containers pass).

### Next

1. This PR's CI, then the owner merges.
2. 117b part 2, the segmenter (`feat/segmenter-117b`); its `internal/engine` change will put `cmd/pleiades` in
   scope through this rule.
3. 117b part 3, segmented dispatch.

### Lab state

Unchanged: `bsd-check`, `win-check`, `lab-dc1` and `lab-lin1` are saved.

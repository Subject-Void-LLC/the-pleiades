# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**2026-10-22. Branch `feat/doc-create-skill`, off `main` at `f5a0a22a` (#85, the prscope census, merged green).** One
pull request at a time; `make pr-check` before every push; the owner merges.

### On this branch

- **`pleiades doc --create-skill`** (the owner asked 2026-10-22): writes `SKILL.md` in the current directory, a skill an
  AI agent reads to learn The Pleiades: the commands to use, the runbook rules, a runbook that validates as written,
  and every implemented built-in method with its check and undo support (`cmd/pleiades/doc_skill.go`).
  - External Collections are left out: a skill is text an agent follows, and their descriptions are a third party's.
  - It replaces a `SKILL.md` carrying its marker and refuses any other; it writes through a temporary file and a
    rename.
  - `TestCLI_DocCreateSkillTeachesARunbookThatValidates` validates the skill's runbook through the real binary
    against a tagged `linux_server`, warnings counted; five mutations each fail their own test.
- Docs: `docs/02-get-started.md` (Teaching an AI agent), the regenerated `docs/reference/cli.md`, CLAUDE.md, a
  changelog fragment.

### Open

- 560 and 551, causes unproven.
- 561 (open): `validate` does not check the register a `when:` condition reads; planned with 117b part 2.
- 562 (open): `--help` exits 1, and `add-host`/`add-credential`/`trust-host`/`set-host` need a name before `--help`.
- From the cold-start dry run (2026-10-22): no command lists a device type's properties (the agent guessed
  `--set port=`), and `pleiades init`'s README still says add-credential prompts and mentions `ssh_exec` tasks.
- **Security finding, the owner's call (2026-10-22):** `pleiades init` writes no `.gitignore`, so committing a project
  commits `.pleiades/master.key` beside the `credentials.yaml` it decrypts; `docs/02-get-started.md` says both are
  "gitignored by default". Proposed: its own PR after this one (`internal/inventory/project.go` writes `.gitignore`,
  the README template fixed, the guide corrected). The skill already tells an agent never to commit `.pleiades/`.

### Next

1. This PR: fp-reviewer, `make pr-check`, push, CI, the owner merges.
2. 117b part 2, the segmenter (`feat/segmenter-117b`), which triggers the census.
3. 117b part 3, segmented dispatch.

### Lab state

Unchanged: `bsd-check`, `win-check`, `lab-dc1` and `lab-lin1` are saved.

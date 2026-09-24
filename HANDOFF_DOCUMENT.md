# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**Phase 35 (Ansible Playbook Migration, v0.3.0) is COMPLETE at 21 of 21 work items, UNCOMMITTED, on
`feature/playbook-migration`**, cut from `1c9bb8b` (main after PR #40 merged Phase 77 and the tftpxfer
fix). The one open item is "Provide Commit Message": the messages, one per logical commit, are in the
session's closing report, with a tree for each. The user commits. **Next is Phase 46** (the user's
instruction), whose open classifier item now has Phase 35's classes to consume.

### What shipped, as six commits

- **(a) strict runbook keys, malformed targets, undeclared params.** Every key a runbook, task or
  `metadata:` does not define is refused (a reflect-driven walker over the parsed tree, since
  `(*yaml.Node).Decode` has no `KnownFields`); JSON refuses near-miss case and repeated keys; a present
  `params.target` must be a non-empty string; `ParamsRule` refuses a parameter a method does not
  declare; `import_tasks` reads through `os.OpenRoot`. The seven examples' `metadata.mcp*` blocks are
  gone (nothing read them; Phase 71 carries the note).
- **(a2) the Runner validates what it is dispatched** before any task runs (FAILURE_PATTERNS 322).
- **(b) native `tags:`** with `--tags`/`--skip-tags`, Ansible's own selection rule, on every tier's
  default selection.
- **(c) the translator core** in `internal/forge/playbook`: `playbook.Translate` is a pure library call
  returning runbooks and a report of findings with stable codes and both positions.
- **(d) the module tables**, checked against the registry and against `ansible-doc -j`.
- **(e) `pleiades forge migrate-playbook`**, exiting 3 when anything needs a person.
- **(f) gates and docs**: the release gate, a behavior gate against real Ansible, generated
  `docs/reference/ansible-modules.md` and `docs/reference/schemas/migration-report.json`, docs/03 and
  docs/01, changelogs, the corpus measurement, and the roadmap writes below.

### Deviations, recorded in IMPLEMENTATION.md under Phase 35

A key walker, not `KnownFields`; the report is `model.go` plus `report_text.go`; four classes, with
`observe` added for reads; `netconf_config` tasks setting `target` are blocked until Phase 108; no Walk
tier tag filter until Phase 109; and `file.directory` changed outside the phase's package (below).

### Findings this session (FAILURE_PATTERNS 327 to 338; LESSONS 233 to 236)

Security-relevant, each fixed and proven: raw playbook file names reached the terminal (329, C1);
refusal messages printed playbook values (330, C7); a negated condition over a registered result ran a
task on every device when one matched (331, C9); two YAML merge keys resolved the opposite way from
Ansible's loader (332); and **`file.directory` left the parents it creates at the umask's mode**, so a
private tree under a new parent was world-readable after conversion (335). That last one is a change to
a shipped Collection method, made because the behavior gate proved it; its changelog is
`changelog/file-directory-parents.security.md`. The rest are correctness fixes (327, 328, 333, 334, 336,
337, 338). Corpus: C1, C7 and C9 got "Seen in" lines, and 332 joined the parser-differential note.

### Verification run

Package tests under `-race` for everything touched, all passing: `internal/forge/playbook`,
`internal/engine`, `internal/validate`, `internal/adapters/native`, `internal/catalog/file` (with
`block` and `line`), `tools/gendocs`, `tools/docs-lint`, `internal/archtest`, `internal/clispec`,
`internal/redact` and `internal/catalog/fragment`. `cmd/pleiades` and `cmd/runner` were run by their
named gates, not as whole packages under `-race`; `make ci` covers that. Release gates, each against real dependencies: `TestMigratePlaybookReleaseGate`
(real Ansible syntax check, real binary), `TestMigratePlaybook_BehaviorMatchesAnsible` (real Ansible
and a real sshd, trees compared), `TestEntries_ArgsMatchAnsibleCore` and `TestMergeKeys_MatchAnsible`
(ansible-core 2.19.11 in the pinned image), `TestCLI_TagsSelectWhatRuns` and the Runner's
`TestValidateDispatchReleaseGate_NoTaskRunsBeforeARefusal`. Fuzz, count-bounded with
`-fuzzminimizetime 2s`: `FuzzTranslate` 300,000, `FuzzWhenToCEL` 300,001, `FuzzKVArgs` 500,000,
`FuzzSelect` 500,000, `FuzzRunForgeMigratePlaybook` 30,000, `FuzzBuildFromYAML` 3,000,000,
`FuzzDAGBuilder` 1,000,025. Every new test was mutation-checked; two that survived their first mutation
were strengthened (LESSONS 235). Coverage: `internal/forge/playbook` 92.1% (floor added), and every
touched package at or above its floor. `vet` under both tag sets, `gofmt`, `docs-lint` clean.

**NOT yet run: `make ci` in full**, which on this machine has to run alone and is the user's call.

### Decisions for the user

1. **Phases 108 and 109 are written with a proposed version, v0.5.0.** 108 moves the device selector
   out of `params` (the S1 decision); 109 gives a runbook launch `job_tags`/`skip_tags`. Place them.
2. **The corpus says `include_tasks` is the largest blocker in public roles (40 constructs) and no phase
   owns it.** The table is in IMPLEMENTATION.md just before Phase 87.
3. **FAILURE_PATTERNS 335 (CWE-276, incorrect default permissions) fits none of the corpus's classes.**
   Whether `~/vuln-corpus` gains a class for it is yours.
4. The migration command rebuilds each runbook twice and allocates about 650 MB for a 5,000-task
   playbook (peak RSS about 230 MB at the 10,000-task cap). Bounded, so left; building the output tree
   directly would cut it, if it ever matters.

### Next

Commit the six commits (messages in the closing report), run `make ci` alone, then Phase 46.

### Files changed this session

New: `internal/forge/playbook/` (whole package, with `testdata/`), `internal/engine/schema_keys*.go`,
`json_strict.go`, `ansible_keywords.go`, `task_syntax_json.go`, `task_target.go`, `tags.go`,
`select.go`, `chain.go`, `task_validate.go` and their tests, `internal/validate/params_rule.go`,
`internal/adapters/native/validate.go`, `internal/catalog/fragment/`, `internal/redact/names.go`,
`cmd/pleiades/forge_migrate_playbook.go`, `tag_flags.go` and their tests and gates,
`cmd/runner/validate_dispatch_release_gate_test.go`, `tools/gendocs/ansible_modules.go`,
`reportschema.go`, `tools/docs-lint/golits.go`, the generated `docs/reference/ansible-modules.md` and
`docs/reference/schemas/migration-report.json`, and eight changelog fragments. Changed: the engine
builder, the validate rules, the native adapter, `internal/catalog/file/directory.go`, catalogdata's
file docs, the examples, docs/01, docs/03, docs/11, generated references, `coverage-floor.json`, and
the FAILURE_PATTERNS and LESSONS files. Gitignored: `.SPECIFICATION/IMPLEMENTATION.md`,
`.SPECIFICATION/SECURITY_ATTESTATION.md`.

# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**2026-10-20. Branch `feat/rollback-on-failure`, off `main` before #80's merge; no pull request yet.** One pull
request at a time; `make pr-check` before every push; the owner merges.

### On this branch, committed

- `rollback_on_failure: true` on a block, refused by validate over a task it cannot undo (Phase 88).
- `examples/ai_repair_lab`: a local model picks a fix in a rescue; `container.run` undos replay.

### On this branch, not committed yet (the owner asked: "rewrite the IOS upgrade example to use the AI module")

The owner's decisions: the runbook author decides whether a question is answered from the task or by a model;
`net.ios.command` plus prompts on `net.cli.command`; proof by a scripted switch now, the DevNet probe when its
credentials are set, and a C8000V lab VM later; this branch.

- `pkg/netcli.Session.Dialog`: one read to the exec prompt, a listed question, or a question-shaped line; each
  listed answer once; `refuse` closes the session; no text to a password question; `ExpectDisconnect`; a timeout
  names the question (`pkg/remoteexec.ReadError` keeps what was read). `netcli.IOS.ErrorPattern` also reads
  `%Error` (549).
- `pkg/aitask`: `Prepare`/`Call.Run`, the one-provider call `ai.chat` now uses; `Chooser`, a model picking one of
  the author's choices by id (schema of the ids plus refuse and wait), through the project, Crawl tier only.
- `pkg/clidialog`: reads `prompts`, `ask`, `expect_disconnect` for both CLI methods.
- `net.ios.command` (new, 177th method); `net.cli.command` gets the same parameters; `wait.connection` tries an
  IOS or `cli_prompt` device by waiting for its prompt.
- `examples/upgrade_ios`: `upgrade_ios_xe.yaml` rewritten (548), `upgrade_ios_xe_ai.yaml` new, a triage prompt,
  `workstation` in the inventory, README rewritten; the Ansible twin gains `no boot system`.
- Proof: `TestCLI_UpgradeIOSExamplesAnswerRefuseAndRecover` (real binary, scripted switch over SSH, Ollama
  stand-in); `pkg/netcli/dialog_test.go`; `TestLiveIOSDialogs` (DevNet, gated, not yet run).
- Roadmap: Phase 88 gains `retries`/`until`/`delay` (owner request).
- Records: FAILURE_PATTERNS 548 to 550.
- Validate (owner: "Do the validate work"): `RequiredParamsRule` (`param-required`) refuses a call leaving out a
  required parameter, `rollback:` steps included; `collection.Param.AliasOf` (file.symlink's `dest` gives `path`);
  `Register` refuses required-and-defaulted and bad aliases. Its first run fixed `exec.winrm.shell`'s doc, the
  `net.catalyst.reachability` example, and the lab's `runbooks/win/10-install.yaml` (no `installer`; outside this repo).

### Open

- The converter still blocks `cli_command`'s `prompt`/`answer` (it needs a mapping that zips two arguments into
  `prompts`); its reasons now say how to write them.
- `TestLiveIOSDialogs` needs DevNet credentials (`PLEIADES_E2E_IOS*`); no reload is ever run there.
- Nothing has run against a real Catalyst switch.

- 551: `TestCLI_RollbackReleaseGate` G2 let a rollback run beneath a later run, 2 in 14 runs (also on HEAD), then 0 in
  30; the gate now names a failed later run. Cause unproven.
- `TestGenericReleaseGate_GRPC` failed once in the full suite at its Java container's start (environment), passed alone.

### Next

1. Commit the IOS work in logical commits, then `make pr-check` and open the branch's pull request.
2. Run `TestLiveIOSDialogs` once credentials are given.

### Lab state

Unchanged: `bsd-check`, `win-check`, `lab-dc1` and `lab-lin1` are saved.

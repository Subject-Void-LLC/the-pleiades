# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**Branch `feature/Phase-46-Simulation-Modes`, off `origin/main` at `27d4bac` (the merge of PR #33).
COMMITTED 2026-09-19 at the user's request as the 19 commits listed below, on top of `cb509f7` (an
earlier compose fix). Not pushed, and not yet gated: `make ci` has not run on these commits.** The
branch has no upstream on purpose (`git branch --unset-upstream`): it was created from `origin/main`
and tracked it, so a stray `git push` would have targeted main. Push with `-u` to its own name. The work
answers the JetPorch comparison's two "ideas worth borrowing": check mode (Phase 46, first slice) and
external Collections (Phase 42's payload decision, Phase 45's loader, Phase 33's forge). The user also
changed `.gitignore` (a `.venv` line) and asked for it kept; it is unrelated and gets its own commit.

### What was built

- **Check mode.** `collection.Mode` and `ParseMode`; `Descriptor.Check` with `Manifest.SupportsCheck`
  (Register enforces agreement); engine `CheckExecutor`, `UncheckedError`, `WithMode`; a check never
  journals, refuses an `inverse` stat, names unchecked tasks and keeps walking, and admits
  simulate-locked devices through `engine.LifecycleAdmitsIn` (also used by `validate.LifecycleRule`,
  whose `WorldView` gained `Mode`). `pleiades run --mode check` ends non-zero if anything went
  unchecked. Nine methods check: six `svc.systemd.*`, `file.directory`, `file.permissions`,
  `file.remove`. `pkg/remotefile` gained `Differs`, `PredictApply`, `PredictCreate`, `DirectoryEmpty`.
- **External Collections.** `pkg/external` (SDK: `Main`, `Serve`, `Description`, `ProtocolVersion`,
  and the device, context and child loop MOVED from `internal/adapters/native`, which now uses them);
  `internal/loader` (two-pass load, ownership and permission checks, SHA-256 pinned and re-checked
  before every run, scrubbed environment, bounded runs, masked output); wiring in `cmd/pleiades`
  (`PLEIADES_COLLECTIONS_DIR`, for run, validate and doc, which now shows each method's origin and
  check support) and `cmd/runner` (`native.WithExternalCollections`); `examples/external_collection`.
- **Forge.** `new-collection` states `SupportsCheck: false` with guidance; new `forge new-external`.
- **Decisions settled, then built (second half of the session).** Every decision got numbered edge
  cases and the secure answer with the most functionality (the user's rule). The three highest-risk
  build items are DONE: confinement of external programs (Landlock plus a non-dumpable parent,
  `internal/loader/confine*.go`, grants through `PLEIADES_COLLECTIONS_READ_PATHS`); the runbook
  `check_mode` key at runbook, block and task level, with unknown top-level keys refused
  (`internal/engine/check_mode.go`, `condition_refs.go`, `internal/validate/check_mode_rule.go`);
  and third-party checks kept off simulate-locked devices (`collection.Descriptor.Provider`, set only
  by the loader, guarded in `engine.checkAction`). New archtest `TestOneYAMLModule`.
- **Then the next three (after the user's "Go").** The approval list (`.pleiades-approvals.json`,
  `pleiades collection approve|revoke|list`, approval checked before any program runs and re-read
  before every call, every run executing the verified file through `/proc/self/fd`, closing the
  swap window); reserved namespaces (`collection.BuiltinNamespaces` plus `pleiades` and `ansible`)
  with `NodeResult.Provider`; and the Runner path, now routed on `Descriptor.Provider` alone, loaded
  before any connection, and proven by a unit test and a real-NATS, real-sshd gate.
- **Then the next three (after the second "Go").** Phase 45's hardening as tests (`internal/termsafe`
  refusing or escaping terminal-controlling text from programs, including at the approve prompt;
  argv, name, stat-name, masking and fuzz tests; an archtest keeping Go source free of invisible
  characters); the provider in the run journal (two ent columns, `sqlite/0028`, `postgres/0025`, the
  CSV, docs/10); and Phase 46's exit status 3 with `--allow-unchecked`, and the engine-owned
  `predicted: true` marker on every check result.
- **Then Walk-tier check mode (the overnight run, under the user's /goal).** The runbook launch kind's
  `mode` field (`launch.TypeChoice`), resolved by narrowing (`launch.resolveMode`, `ErrMode` 422,
  `Template.CheckSavedMode` at save), a form select in the UI; `POST /templates/{id}/check` (rel
  `check`, `Dispatcher.CheckFromTemplate`); fan-out admission by `LifecycleAdmitsIn` and a check
  published on `topology.CheckSubject`, read by the Runner's second loop (durable `runner-check`,
  `routing.CheckOnly`), so a Runner from before check mode never receives one; the legacy adapter
  refuses checks; `dispatch.Job.Mode`, shown by the job API, its OpenAPI schema and the Jobs page;
  `internal/meshid` grants for the check subject and consumer. Proven by a real-NATS, real-sshd gate
  and a real operator-mode broker gate.
- **Then the rest of the plan's buildable items (still the overnight run).** CEL partial evaluation of
  a check's conditions (`Program.EvalPartial`, `ConditionProgram.EvalPartial`, `check_conditions.go`:
  answered where the unknowns cannot change it, unchecked where they can, a misspelled register fails
  as the real run would); the "cannot check this call" answer (`collection.CannotCheck`, the optional
  `wire.ChildResponse.CannotCheck` flag, honored only for a check by the engine and both parents);
  engine version stamping (`internal/buildinfo`, `runner version`, the Runner's loader gets the version,
  one warning per program, the Dockerfiles stamp from `VERSION`/`VCS_REF` with a character guard, the
  scaffold writes the generating release); the scaffold's minimal `go.mod` and `--no-go-mod`, its
  release gate now built out of tree by the README's own offline steps; the loader's registration
  parity fuzz and injected-rule test; the Windows refusal naming WSL 2; the mixed-DAG fuzz and the
  check-versus-run benchmark with `ansible-playbook --check` as reference; the BusyBox chmod test on a
  real Alpine sshd; Phase 46's hardening audit and documentation gate. Also: a doc comment added to
  seventy new Go files the commit gate would have refused (LESSONS 203), and `internal/catalog/file`
  added to the Makefile's `DOCKER_DEPENDENT_PACKAGES` (the repeat-gate test caught it).
- **Then the two Walk-tier check items.** `check_complete` on the job: `wire.Outcome` is the second
  return of every execution adapter, the Runner reports a check's unchecked count and a reason with its
  result, the Controller stores it (`job_task.unchecked`, `sqlite/0029`, `postgres/0026`) and
  `dispatch.Job.CheckCoverage` answers complete or not. And `runbook:check`: the scope (implied by
  execute), the check route requiring it, and external programs' checks run only for a launcher who may
  run the job for real (`api.MayRunForReal` to `dispatch.Job.ExternalChecks`, `jobs.external_checks`,
  `sqlite/0030`, `postgres/0027`, to `wire.DispatchPayload.ExternalChecks` to
  `engine.WithExternalChecks`, off by default).
- **Then check support for every other method (2026-09-19, still the overnight run).** 69 of the 78
  implemented methods now check; the other nine carry `Manifest.NoCheckReason`, which the unchecked
  line, validation and the reference page print, and `internal/archtest` requires of every built-in
  without check support. Read-only methods set Check to Invoke (`facts.gather`, `net.ssh.ping`,
  `net.ios.facts`, `net.ios.ping`, four `net.catalyst.*`); the rest share one body with Invoke that
  branches after the same reads and refusals (`file.*`, `pkg.*`, `identity.*`, `fw.firewalld.*`,
  `svc.*`, `win.feature.*`, `archive.*`, `fs.*` with a new `fstabDecide`, `container.docker.run/stop/
  remove`, the AWS four with a new `awscloud.BucketHoldsAnything`/`AccessDenied`, `net.ios.save`).
  `exec.command`/`exec.shell` check guarded calls and `http.request` safe methods, with
  `Descriptor.CheckCall` letting validation refuse `check_mode` on any other call of theirs
  (`engine.Checkable(fqcn, params)`). A check whose inputs are missing when it runs reports that task
  unchecked, naming the input. `internal/catalog/container/docker` joined the Makefile's Docker list.
- **Docs.** 01 (status, limitations, FAQ), 02 (step 8, real captured output), 11 (check support,
  external Collections, forge), 13 (the protocol's compatibility promise), CLAUDE.md, the regenerated
  reference (a "Check mode" row on every module page, `--mode`, `new-external`, `check_mode` in the
  task-key page and runbook schema), four changelog fragments (one `.breaking`: unknown top-level
  runbook keys), FAILURE_PATTERNS 247 to 255, LESSONS 199 to 201, and progress notes under Phases 33, 42,
  45 and 46 in IMPLEMENTATION.md recording what is and is not built, and every deviation.

### Findings: report each to the user as its own item

1. **Correctness, fixed:** `remotefile.Apply` left a regular file's setgid cleared after changing its
   group, and reported success (FAILURE_PATTERNS 247).
2. **Correctness, fixed:** GNU chmod keeps a directory's setuid/setgid for a four-digit mode, so a task
   asking 0755 of a 2755 directory reported changed forever (FAILURE_PATTERNS 248). Apply now sends
   five digits (`00755`) for every chmod.
3. **Correctness, fixed:** validation refused check mode against simulate-locked devices
   (FAILURE_PATTERNS 249), caught by the real-device gate after the engine unit test passed.
4. **Security, by the user's rule:** an external Collection gets credentials exactly as designed,
   through `InjectSecrets` (the template's bound machine credential resolved at fan-out, or the
   device's own); the loader adds no credential path. The user corrected my earlier wording ("the one
   device's credential") and my over-reading of their rule as a demand for OS sandboxing.
5. **Security, proven by probe, FIXED (FAILURE_PATTERNS 251):** the environment allowlist was
   documented as keeping the master key and broker credentials from a program, but a same-user child
   reads its parent's starting environment from `/proc/<ppid>/environ`, even after the parent unsets
   the variable. The false claims are corrected (loader doc and capture.go, `pkg/external`, docs/11,
   which now lists "No confinement"). `PR_SET_DUMPABLE 0` in the parent blocked the read in the same
   probe. Now fixed by confinement (Landlock) plus the non-dumpable parent; a real program is denied
   the master key, credentials, SSH keys, `/proc/<parent>/environ` and `mem`, and the unconfined
   control reaches them.
6. **Security, FIXED (FAILURE_PATTERNS 253):** check mode admitted simulate-locked devices for a
   third party's unproven Check. Now reported unchecked; proven on a real sshd with a program whose
   Check writes (nothing on the locked device, the write lands on the active control).
7. **Correctness, FIXED (FAILURE_PATTERNS 252):** a runbook-level `check_mode: true` was silently
   ignored and the runbook ran for real. Now honored, and any unknown top-level key is refused.
8. **Stated plainly:** no signature verification yet; the directory's permissions and the SHA-256
   re-check are the trust decision. Between the re-check and the exec there is still a swap window,
   usable only by the directory's owner or root; the approval-lockfile build item closes it by
   executing the bytes it hashed (not built; confinement did not need a re-exec shim after all).
9. **Security, found while building and fixed (FAILURE_PATTERNS 254):** Landlock applied from a
   goroutine could land on the main thread, putting Pleiades inside the program's own domain (the
   program could signal it). Intermittent; now pinned by a deterministic main-thread probe test.
10. **Correctness, found while building and fixed (FAILURE_PATTERNS 255):** an unmarshal hook written
    against `gopkg.in/yaml.v3` is never called by the engine's `go.yaml.in/yaml/v3` decoder, so
    `check_mode: false` decoded silently until a test caught it; now an archtest forbids the old module.
11. **Security, found by the hardening audit and fixed (FAILURE_PATTERNS 257):** a program's text
    reached the terminal raw: the approve prompt, its error message as the task's FAILED line, and
    `--verbose` stat values. A program could draw a fake "approved" line. Now refused at load where
    possible and escaped wherever printed.
12. **Process, found and fixed (FAILURE_PATTERNS 256, LESSONS 202):** the agent's file-writing tool
    decodes `\u` escapes, and real bidirectional overrides landed in source; replaced, scanned
    (2,414 files clean), and guarded by `TestNoInvisibleControlCharactersInGoSource`.
13. **Security and availability, found while building and fixed (FAILURE_PATTERNS 258):** the check
    subject and the `runner-check` consumer were missing from `internal/meshid`'s grants, so under a
    minted identity every check would fail at publish and an upgraded Runner would exit at startup.
    Nothing in production mints identities yet, so nothing was broken today; the grants and the real
    operator-mode broker gate now cover the check traffic.

### Verified, and how

- Check support batch (2026-09-19): every new check tested from converged and unconverged starts
  against its real run and mutation-checked (Check wired to Invoke, or the guard removed), with the
  controls on real things wherever this machine has them: a real sshd for the guarded `exec.command`
  under `check_mode` (the key gate), real tar, this machine's real Docker daemon through the real SSH
  path, real LocalStack for EC2 and S3, real mounts and a real account database (useradd and kin)
  inside an unprivileged user and mount namespace (`testsupport.InPrivateRoot`), the Catalyst replay
  with every request recorded; fakes for Windows, firewalld and the package managers (stated, and a
  new build item).
  `FuzzRegistrationParity` gained `NoCheckReason` and caught the loader mirror's removal. `-race` on
  collection, engine, validate, loader, exec, http, win/feature, archive, gendocs. The key gate's
  docs/02 output was recaptured from the real CLI against a real sshd. The full suites of all 77
  changed packages, serially: green (67 with tests, 10 without), and the seven changed after the run
  passed them green on a rerun. Two infrastructure failures, both in listed packages and both green on
  rerun: `cmd/runner`'s `TestInjectionReleaseGate_NoSecretLeaves` once, under the long serial load
  (alone and in a full rerun it passes), and LocalStack not starting within 60 seconds once under
  `-race` in `internal/catalog/cloud/aws/s3`. `-race` on every package that gained a check, vet under
  both tag sets, vet and build for Windows and macOS, gosec (21 findings, all waived: two scope waivers
  renumbered and re-reviewed, and the helper's G702 fixed by `os.Executable` rather than waived),
  gofmt, docs-lint, gendocs idempotent, `go mod tidy` clean, every new Go file carries its doc comment,
  no em dash.

- Walk-tier check items: `TestAgent_ReportResult_AnIncompleteCheckSaysSo`, `TestCheckCoverage_FromTheRunnersResults`,
  `TestResultConsumer_DropsANegativeUncheckedCount`, `TestJobHandler_SaysWhetherACheckWasComplete`, the
  Walk-tier gate's new incomplete check over real NATS and sshd, `TestCheckRoute_ACheckOnlyCallerMayCheckAndNotRun`
  (real tokens), `TestCheckMode_ExternalChecksAreOffByDefault`, `TestWorker_CarriesTheExternalChecksDecision`,
  `TestAdapter_Execute_ExternalChecksFollowThePayload`; five mutations caught; migration parity; full
  suites of auth, api, dispatch, adapters, engine, ui, ent, archtest, launch, runner and the three
  commands green; `-race` on runner, dispatch, adapters, api, wire.

- Plan-items batch: `TestCheckConditions_*`, `FuzzCELEvalPartial` (183k runs), `TestCannotCheck_*`,
  `TestInvokeRequest_CannotCheckCrossesAsItsOwnFlag`, `TestIPCCollectionExecutor_CannotCheckCrossesTheProcessBoundary`,
  `TestHardening_CannotCheckIsNeverASuccess`, `TestRelease`, `TestResolve`, `TestCheckEngineVersion`,
  `TestLoad_OneVersionWarningPerProgram`, `TestVersionReleaseGate_OneVersionAcrossBothBinaries` (both real
  binaries, stamped both ways), `TestGenerate_EngineConstraintFollowsTheGeneratingBuild`, `TestGoMod_*`,
  the scaffold release gate built offline from its README, `TestForgeNewExternal_GoMod`,
  `FuzzRegistrationParity` (7.9M runs), `TestRegister_ARuleOnlyRegisterHasNamesWhatStaysRegistered`,
  `TestUnsupported`, `TestLoadExternalCollections_UnsetIsSilent`, the Windows-only test (compiled and
  vetted for Windows, not run: no Windows host), `FuzzCheckMixedDAG` (422k runs) and its generator
  coverage test, the file benchmarks, `TestBusyBoxChmod_FiveDigitModesMeanWhatTheySay`,
  `TestCheckSubject_AHostileDeviceIDIsOneToken`, and the CEL cost limit on partial evaluation. Every new
  guard mutation-checked. Full short suite of `./internal/... ./pkg/... ./cmd/... ./tools/...` green
  after the Makefile fix (one Vault container start-up refusal passed on rerun); `-race` on engine,
  loader, buildinfo, externalscaffold, adapters, pkg/external, collection, topology, api, dispatch;
  vet under both tag sets and for Windows and macOS; gosec 21 (all waived); gofmt; docs-lint;
  gendocs idempotent; no em dash in any added line; every new Go file has its doc comment.

- Walk-tier batch: `TestResolveMode`, `TestModeIsRefusedWhenSaved`, `TestChoiceField`,
  `TestCheckRoute_*`, `TestJobHandler_SaysWhetherAJobWasACheck`, `TestModeBadge_MarksACheckApart`, the
  dispatch mode table, `CheckOnly`, the legacy refusal, the grant tables,
  `TestReleaseGate_TheRealControlPlaneRunsUnderAMintedIdentity` (now with a check act), and
  `TestCheckModeReleaseGate_TheWalkTierChecksAndChangesNothing` (now asserting a check journals nothing
  against a real run that does). Handler, grant and broker-gate mutations each caught. Short-mode
  suites of every touched package, `-race` on launch, api, dispatch, adapters, ui jobs and templates,
  meshid; vet, gosec (21, all waived), gofmt, docs-lint, gendocs clean.

- Unit and integration tests for every touched package, one at a time, and `-race` on each core
  package; `internal/loader` 90.8% coverage, under goleak, fuzzed (FuzzParseDescription, ~426k execs
  clean); `pkg/external` 98.2%. Mutation checks: the engine's seven check-mode guards, the three file
  checks, five loader guards; each failed its test when disabled.
- Real devices: `TestCLI_CheckModeChangesNothing` and `TestCLI_ExternalCollectionRunsAgainstARealDevice`
  against a real sshd, inspecting the device over an independent connection.
- `internal/archtest` (all), `make gosec` (21 findings, all previously waived), `make vet` (both tag
  sets), `make fmt`, `docs-lint`, gendocs regenerated, `GOOS=windows`/`darwin` vet of the loader.
- Benchmark: an external call costs 0.83 ms (re-verify, spawn, one exchange) against ~1 ns in-process.
- Second half: `TestConfinement_*`, `TestCheckModeKey_*`, `TestRunbookKeys_*`, `TestConditionReads`,
  `TestCheckModeRule_*`, `TestCheckMode_AnExternalCheckSkipsASimulateLockedDevice`,
  `TestLoad_TheLoaderSetsTheProvider`, `TestOneYAMLModule`; eleven guards mutation-checked;
  FuzzCheckModeKey ~568k runs clean; `-race` on loader, engine, validate, collection, remoteexec,
  archtest; real-sshd gates `TestCLI_CheckModeKeyAgainstARealDevice` and
  `TestCLI_AnExternalCheckNeverReachesASimulateLockedDevice` new, and every existing gate still
  passes; the full `cmd/pleiades` suite passes; `cmd/runner`'s full run had one NATS provisioning
  timeout (listed package), and that test passed alone. `make vet`, `make gosec`, gofmt, docs-lint,
  gendocs idempotent, `go.mod` unchanged.
- Third batch: `TestApproval_*` (including the swap race and its by-path control),
  `TestLoad_ReservedNamespacesAreRefused`, `TestCLI_CollectionApproveAsksAndRecords`,
  `TestCLI_AProgramCannotClaimACatalogNamespace`, `TestIPCCollectionExecutor_ExternalMethodsRunInThisProcess`,
  `TestExternalCollectionReleaseGate_*` (real NATS, real sshd, and the real Runner binary refusing a
  bad directory with no broker); five more guards mutation-checked; full `cmd/pleiades` and
  `cmd/runner` suites pass; race, vet, gosec, gofmt, docs-lint clean; `go.mod` unchanged.
- Fourth batch: `TestHardening_*`, `FuzzDecodeResponse`, `FuzzParseApprovals`, `FuzzEscape` (30s each,
  clean), `TestCLI_CollectionApproveEscapesTheProgramsText`, `TestNoInvisibleControlCharactersInGoSource`
  (proven by planting a character), `TestJournal_AnExternalMethodsEntryNamesItsProgram`, the ent
  round-trip and migration parity, `TestCLI_CheckExitStatus`, `TestPrediction_*`; nine more guards
  mutation-checked; full `cmd/pleiades` and `cmd/runner` suites pass; race, vet (both tag sets), gosec,
  gofmt, docs-lint, gendocs clean; cross-vet on Windows, macOS, FreeBSD; `go.mod` unchanged.
- **Not run:** `make ci` and `make push-gate` (they saturate this machine for ~20 minutes; ask first).

14. **Correctness, found by the key gate and fixed:** making `exec.command` checkable for guarded
    calls made `pleiades validate` accept `check_mode: true` on an unguarded one, which can only ever
    be reported unchecked. Fixed with `Descriptor.CheckCall` (LESSONS 204).
15. **Security, process, report it:** a shell test of whether `LOCALSTACK_AUTH_TOKEN` was set printed
    its first 22 characters into this session's output (`${VAR:+set}${VAR:-unset}`). Nothing left the
    session, but the user should rotate that LocalStack token (LESSONS 205).
16. **Tests, found and fixed (FAILURE_PATTERNS 259):** hoisting `svc.windows`'s operations into
    package-level values froze their test seams at init, and the tests dialed a real WinRM address.

### Known and not done

Every decision is SETTLED (2026-09-18) under the user's rule: list each decision's edge cases, then
take the secure answer that gives the most functionality. Each is a ticked item in IMPLEMENTATION.md
(options with pros and cons, numbered edge cases, the decision), followed by an unticked "Build:" item
carrying its test plan; 15 decisions across Phases 33, 42, 45 and 46, plus one build item in Phase 48.
Four answers changed from the earlier recommendations: confinement with Landlock and a non-dumpable
parent instead of an environment-variable key (Phase 45), a minimal `go.mod` (Phase 33), stamped
versions enforced on releases only (Phase 42), and CEL partial evaluation for conditions (Phase 46).
Walk-tier mode resolves by narrowing, the one exception to the most-specific-wins settings rule. The
headline gaps, all now build items:

- Phase 45: the phase's publishing and signing items (Phases 43 and 44). (Confinement, the approval
  list, namespaces, the Runner path, the hardening tests, the journal provider, the registration parity
  fuzz and the Windows refusal DONE.)
- Phase 46: real-device proof for the checks proven only on fakes (Windows, firewalld, package
  managers), toybox and BSD chmod, and the Phase 35 cross-check. (Check support for
  every implemented method DONE: 69 check, nine say why.) (Walk-tier mode, `check_complete`, `runbook:check`, the `check_mode` key, simulate-lock admission, exit status 3, the
  prediction marker, partial evaluation, the "cannot check this call" answer, the mixed-DAG fuzz and
  benchmark, BusyBox chmod, the hardening audit and the documentation gate DONE.)
- Phase 42 and Phase 33: DONE (engine version stamping; the minimal `go.mod`). Phase 48: the stored,
  digest-bound check result.
- Phases 42 to 44: OCI media types and artifacts, reproducible digests, a registry, signing.
- External Collections on Windows.

### Commits (made 2026-09-19)

Split by file rather than by hunk, since the engine, the native adapter and a few other files carry
several features each; the 32-commit plan this replaces assumed hunk splits. Every commit was checked
to build and vet from a clean worktree at that commit, cumulatively, before any was made. The commit
gate refused nothing and warned three times: a deliberate capitalized test error in
`pkg/awscloud/bucket_contents_test.go`, and `cb9388a` and `a62b891` editing a spec without the
regenerated reference, which is in `37ead22`.

1. `3040d2b` chore: ignore .venv
2. `e3903d9` fix(remotefile): keep setuid and setgid right across chown and chmod
3. `49ee6af` feat(build): report one build version from every binary
4. `031a97b` feat(collection): add check mode to the Collection contract
5. `eda7a21` feat(external): load Collections built outside this repository
6. `73e58e1` feat(engine): run checks through the engine, both parents and the Runner
7. `ec5d571` feat(runner): read checks from their own subject, load Collections first
8. `0b47a5d` feat(store): record task providers, check coverage and external checks
9. `ddeabe5` feat(catalog): check the read-only, network and HTTP methods
10. `38dcd95` feat(catalog): check every file method
11. `eeb2ba5` feat(catalog): check the package methods
12. `1c8d258` feat(catalog): check the identity and firewalld methods
13. `00a9477` feat(catalog): check the Windows and generic service methods
14. `ae53ab6` feat(catalog): check guarded commands, features, archives and mounts
15. `9c02b30` feat(catalog): check Docker and AWS, and say why the rest cannot be
16. `cb9388a` feat(controller): run a template as a check
17. `a62b891` feat(cli): add pleiades run --mode check, and load external Collections
18. `37ead22` docs: document check mode and external Collections
19. `0a2d28c` docs: record what the check mode and external Collection work found

### Next step

Run `make ci` (or `make push-gate`) on the committed tree with nothing else running; it writes the
receipt the pre-push hook checks. Then push with `-u` to this branch's own name, since it has no
upstream on purpose. The remaining build items:
real-device proof for the checks proven only on fakes, toybox and BSD chmod (no device in the lab),
and the Phase 35 cross-check (waits on Phase 35). Rotate the LocalStack token (finding 15).

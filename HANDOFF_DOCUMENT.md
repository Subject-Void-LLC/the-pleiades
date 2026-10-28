# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**2026-10-27. Branch `feat/authoring-parity-v0-3-0`, off `main` at `122b8c92` (PR #91's merge), 16 commits (oldest
`e0b243fd`, newest `578390c2`), plus these records. Not pushed, no pull request, and `make pr-check` has not run. No tag
was cut and nothing was published.**

The branch is the path to the first binary release, v0.3.0 "Paranoid Porpoise", redefined as "Crawl and Walk plus
authoring parity": release tooling, runbook variables, loops, retries and failure keywords, `file.template`, and the
converter, docs, skill text and Walk-tier proof for them.

### What works

- Release tooling: `make release VERSION=x.y.z` and `make release-dry-run` (`tools/release`, `e0b243fd`). The dry run
  built 18 binaries in 5m18s and `sha256sum -c` passed on all six archives. `.github/workflows/release.yml` builds on a
  tag, refuses a tag not on `main`, reads `VERSION` through the shell, and publishes only archives and `SHA256SUMS`.
  Only `actionlint` has run on the workflow. Codenames: `internal/buildinfo.Codenames`, shown on a second line of the
  three version commands and in the release title (`5a429d3d`).
- Variables (`vars.go`): `vars:` at runbook, block and task level; `set_fact` with `global` and `no_override`; the
  narrower scope wins (runbook, global fact, device inventory vars, device fact, block, task, then extra vars). That
  order is not Ansible's. `group_vars/` and `host_vars/` beside the inventory fold into each device's `vars` property
  (links refused, 64 files at most, nested secret keys refused); `vars` rides the Walk payload through one shared filter,
  `redact.CleanVars`. `import_tasks` with `vars:` is a function call (`TestImportTasks_VarsAreTheCallsArguments`).
- Loops and retries (`loop.go`, `retry.go`, `assert.go`): `loop:`/`with_items:` with `loop_control`, `until`/`retries`/
  `delay`, `changed_when`, `failed_when`, `ignore_errors`, `assert`, `fail`. Each item and each try is its own result and
  journal entry (`<node>.item[k]`, `<node>.attempt[k]`; `DAG.NodeFor` maps back). An ignored or retried failure keeps
  its error and is recorded as a failure (`Ignored`, `Retried`). Only the method's own failure is retried; a stopped
  wait fails the task; runs are bounded (1000 items, 5000 runs, 16 MiB of results).
- `file.template`: the engine renders `src` from the project and hands the method bytes; `file.copy`'s write, check and
  undo. Proven over SSH on Linux only.
- Validate rules for vars, loops and until; the converter maps the new keywords (`internal/forge/playbook`); docs page
  `docs/04-writing-runbooks.md` with checked examples; skill text; fuzz (`FuzzLoopsAndVars`).
- Proof: `cmd/runner/loop_release_gate_test.go` (real NATS, Runner, sshd and database: a looped job with vars and a
  dynamic list, journaled per item, rolled back newest first). Engine 95.3%, validate 100%, converter 92.8% by plain
  `-cover`.
- Review (fp-reviewer, 10 findings) fixed before this handoff: `d1318b37`, `578390c2`, `7d54619e`. FAILURE_PATTERNS
  691 to 698, LESSONS 348 to 351, twelve changelog fragments.
- Blind CVD lab, runs 5 and 6 (turn A, prompt unchanged but for per-instance addresses and a Batfish naming line).
  Run 5 at `b1999791`: Opus 44/0, Haiku 34/7. Run 6 at `d5994cdf`, three instances at once: Opus 44/0 (rollback of loop
  items held on the lab), Haiku 37/1 (a grader false positive; 13 shell-rule breaks), Sonnet 44/6 (it stopped CORE2
  and the controller refused the restarts, HTTP 400). Run 4's 69 repeated tasks are 0 in both Opus runs and Sonnet's,
  22 in Haiku's run 6. Reviews `~/pleiades-cvd/runs/AAR-run5-*.md` and `AAR-run6-*.md`, metrics `runs/metrics-*.json`,
  comparison in a private artifact. Fixes from run 5: `48716acc`, `578390c2`; from run 6: `7783c730`, `0d111b4f`
  (not yet run on a lab). Records: FAILURE_PATTERNS 699 to 703.

### Blocker

- None. The gate has not run.

### Open

- Gate: `.venv/bin/python tools/monitor/monitor.py run --cap 4500M`, waited on with one background `curl` on
  `/wait?for=fail,end`. Not yet exercised: full `cmd/pleiades`, `cmd/runner`, the Mac, BSD, Windows and Windows ARM legs
  (the Windows tools runbook now uses a loop), the Docker migrate-playbook gate (its expected `loop.unrolled` and
  `keyword.ignore_errors` codes should still hold), and the real coverage floors. `tools/release` has no floor.
- `file.template` is unproven on macOS and FreeBSD, has no Crawl-tier real-sshd test, and `validate` does not compile
  the template body.
- Unproven Release Gate clauses in Phases 87 to 89 (see the dated notes in the roadmap): extra vars beating `vars:`, a
  register read in params and `set_fact` read by `when_cel` on a real sshd, the Walk-tier `until`, `changed_when`
  against a real device. Schema/Injection Hardening for 88 and 89 is not written. No warning when a register changes
  from plain to looped.
- Phases 41 and 81 are not started and still sit in v0.3.0, which keeps it open. Moved out on 2026-10-27: Phases 43,
  44, 49, 119e to 119h, 119l, 119m to v0.5.0, and 125a to 125p to v0.7.0.
- Method gaps from the labs (OPEN, see the AARs): `net.batfish.check` takes no network name; `until` over a read-only
  `show` warns every time and `wait.routes` waits only for presence; `net.ios.config` misses the IKEv2 fvrf duplicate
  notice; `net.ios.config` cannot be checked; stdin splitting for two credentials, the System Bridge value and
  `route.protocol` values are undocumented. The grader cannot import a lab whose connectors have spaces in their names.
- Groundconfig: three knowledge entries were added in `~/Groundconfig/knowledge/` (IOL interface names, the IKEv2 fvrf
  notice, Batfish protocol names), uncommitted by rule. Its own test fails on two older entries (Podman, Fusion).
- Not built on purpose: handlers, `serial:`, `async`/`poll`, rescue across Walk segments, other `with_*` forms.
- `internal/buildinfo.CurrentRelease` still reads `0.2.0`; moving it is the owner's change when v0.3.0 closes.
- Carried from before: FAILURE_PATTERNS 668, 677 and the earlier lists in the archive.

### Next, in order

1. Commit these records (`HANDOFF_*`, `README.md` if touched). `.SPECIFICATION/IMPLEMENTATION.md` is git-ignored.
2. Run the gate through the monitor. On a failure stop, prove the failing packages alone, fix, rerun.
3. Push once and open one pull request. In its body: the failure-pattern classes checked, the run 4 and run 5 table with
   its caveats, and a Tools used section that says Groundconfig is not a released tool. No AI attribution anywhere.
4. After it merges: the owner decides the tag. Then Phase 117b part 3, Phase 126, and the open method gaps.

### Lab state

The CML sandbox is reserved (VPN interface `plcml0` up); `default-lab` (10 nodes) is the only lab, and both run-5 labs
were torn down by their turn B. The harness takes `INSTANCE=opus|haiku` (`run5-instance.sh`) and shares one Batfish.
`win-check` and `bsd-check` are saved on VENGEANCE; the Mac and its Fusion Windows ARM VM are as in the archive.

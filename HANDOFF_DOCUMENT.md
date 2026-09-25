# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**Phase 35 is committed and pushed** (seven commits ending `0b5fe4e` on `feature/playbook-migration`,
push-gate passed). **Phase 110 (Connection Persistence), written this session at the user's request, is
built and verified except `make ci`, and UNCOMMITTED on the same branch.** The user commits; the
messages are in the session's closing report. Phase 46 is next after it, per the user.

### What Phase 110 is

Every Collection task used to log in to its device afresh. The user asked for Ansible's
`ControlPersist`: on by default, switchable off for extra security, set at stepped levels where the most
specific direct setting wins, and off meaning the old behavior. Decided with the user: one child per
dispatch on the Walk tier; a connection lives for one run or dispatch (closed at its end or after 60 s
idle); off wins between the job's ladder and the device's.

- **`pkg/remoteexec.Pool`**, reached through `sdk.Connect` when the `RunbookContext` implements
  `sdk.ConnectionPooler`, so no method changed. Keyed by device, address, a per-process HMAC of the
  credential (`Auth.identity`), host key mode and known_hosts path; reuse also needs the known_hosts
  content unchanged and a keepalive answered in 3 s. A shell, subsystem, streamed process or cut-off
  command taints the connection, which then closes at `Close`.
- **Crawl:** `pleiades run --persist-connections` (default true), the `persist_connections` device
  property over the hierarchy (`engine.PersistFor`; a non-boolean is off).
- **Walk:** runbook launch field `persist_connections` (`on`/`off`); fan-out ANDs it with the device
  ladder into `wire.DispatchPayload.PersistConnections` (absent means off); the native adapter runs such
  a dispatch through one session child (`--internal-collection-session`,
  `internal/adapters/native/ipc_session.go`). `external.ServeChild` is unchanged; the session loop uses
  the new `external.InvokeRequestWithPool`. Every TestMain routes children through `native.RunChildFor`.
- **Login changes:** `Manifest.EndsLoginSession` on the six identity methods; new
  `pleiades.builtin.connection.reset` (catalog now 82 registered, 79 implemented, 70 checkable), which
  `migrate-playbook` maps `meta: reset_connection` to.
- **Measured** (same containers as Phase 35's benchmark): ten reads 0.092 s with persistence, 0.232 s
  without, 2.783 s for Ansible's default; the target's sshd logged 5 logins for 5 runs, against 50.

### Verification run

`-race` on every touched package, passing: `pkg/remoteexec` (pool tests also `-count=5`), `pkg/sdk`,
`pkg/external`, `pkg/wire`, `pkg/collection`, `internal/engine`, `internal/adapters/native` (session
tests `-count=3`), `internal/dispatch`, `internal/launch/...`, `internal/catalog/pleiades/...`,
`internal/forge/...`, `internal/archtest`, `internal/clispec`, `tools/gendocs`; plus every one of the 35
packages that use the in-process SSH server (its `Close` changed), without `-race`. Every pool safety
check was mutation-tested. `FuzzSessionChild` ran 303,193 execs clean. Release gates against real
sshd: `TestCLI_RunPersistsConnections` and, over real NATS, `TestSSHMeshReleaseGate_PersistentConnectionLogsInOnce`.
`cmd/runner` in full: one failure in four runs whose test name was lost (output not captured; the
package is on flaky-packages.json), then three clean runs. `tests/e2e`: see the closing report; two
compose gates fail before starting because something else (the `dvwa` container) holds 127.0.0.1:8080.

**NOT yet run: `make ci` in full**, which has to run alone and is the user's call. Coverage floors were
not re-checked by `tools/coverage-check`.

### Added later in the session: `--forks` and the Ansible scaling benchmark

- `pleiades run --forks N` (1 to 1000, default 5, Ansible's), `TestCLI_RunForks`.
- `tools/ansiblebench` (Python, manual, like `tools/genrrulefixtures`) and its report,
  `docs/15-performance.md`, with raw results in `tools/ansiblebench/results/2026-09-24.json`. 27
  measurements (1 to 200 hosts, 5 and 25 wide), all passing: Pleiades 24 to 41 times faster, about 57
  times less control-node CPU per task at 200 hosts, 9 to 14 times less control memory, 62 times less
  network. docs/10's persistence numbers now come from it (the first comparison's target ran `sshd`
  unprivileged and understated logins, FAILURE_PATTERNS 341).
- Getting there: the harness first died when zombies from Ansible's orphans (reaped by nobody under
  `sleep` as PID 1) exhausted the machine's task table (FAILURE_PATTERNS 343), and it lost its results
  on that failure (342). Both fixed in the harness.

### Decisions for the user

0. **FAILURE_PATTERNS 343 applies to production.** `internal/adapters/legacy/docker_orchestrator.go`
   runs `ansible-playbook` as PID 1 with no init; a 20-host run through that shape left 191 zombies
   under it, one per task execution, until the container exited. A playbook large enough (hosts times
   tasks in the tens of thousands) could exhaust a Runner host's task table in one run. The fix is to
   start the container with an init (`HostConfig.Init`); not made, pending your decision.
1. Phase 110's version is proposed as v0.3.0, beside Phase 35.
2. `persist_connections` needs Organization and Project levels and a System setting; written as items
   in Phases 103c and 104.
3. `net.ssh.ping` deliberately always logs in afresh; the transport actions (`ssh_exec` and kin) and
   external Collection programs never pool.

### Files changed this session

Also, later: `cmd/pleiades/run_forks_test.go`, `internal/engine/executor.go` (`DefaultMaxConcurrency`
exported), `tools/ansiblebench/` (`bench.py`, `Dockerfile.target`, `results/2026-09-24.json`),
`docs/15-performance.md`, the README, docs/03 and docs/10 links, `changelog/run-forks.added.md`, and
FAILURE_PATTERNS 341 to 343.

New: `pkg/remoteexec/pool.go`, `pool_health.go` and tests; `internal/engine/persist_connections.go` and
test; `internal/launch/persist.go`; `internal/adapters/native/ipc_session.go`, `ipc_session_child.go`
and tests; `internal/catalog/pleiades/builtin/connection/`; `internal/forge/catalogdata/collections_session.go`;
`internal/dispatch/worker_persist_test.go`; `cmd/pleiades/persist_connections_release_gate_test.go`;
`cmd/runner/persist_mesh_release_gate_test.go`; three changelog fragments; the generated module page.
Changed: `pkg/remoteexec` (auth identity, taint and use-after-close guards, test server), `pkg/sdk`,
`pkg/external`, `pkg/wire`, `pkg/collection` (the manifest field), the identity methods, the engine's
collection executor and context, the runbook launch kind, the dispatch worker, the native adapter,
`cmd/runner` routing, `cmd/pleiades/run.go`, `internal/clispec`, the translator's `meta` handling,
generated references, docs/01, docs/03, docs/10, README, CLAUDE.md, the FAILURE_PATTERNS (339, 340) and
LESSONS (237) files. Gitignored: `.SPECIFICATION/IMPLEMENTATION.md` (Phase 110; items in 103c and 104),
`.SPECIFICATION/SECURITY_ATTESTATION.md` (PW.9).

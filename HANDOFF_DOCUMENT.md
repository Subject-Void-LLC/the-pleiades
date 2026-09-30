# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**Branch `feature/Phase-75-WinRM-Closeout`, cut from `main` at `e8129b2f` (PR #46, Phase 117a merged),
2026-09-29. The user's order: "plan Phase 75: WinRM, the Three Execution Modes", then "Cut the branch and
start"; mid-session, "You can build windows hosts in virtualbox" and "you have bsd too".** Plan approved:
`/home/noot/.claude/plans/sharded-beaming-rabbit.md`. Nothing is committed; the commit messages are in the
final message of the session. Rules unchanged (method-as-key runbooks, two agents at most, heavy commands
under `~/.local/bin/capped`, the lab provisioned by The Pleiades only).

### What was done

- **Phase 75 is 25/25.** Three open items and the commit-message item closed; follow-ups moved to Phase 112
  (a breaker for WinRM Collections) and Phase 117b (plan-time `RequiredCapabilities`, Controller admission).
- **One circuit breaker, `pkg/breaker`.** Moved out of `pkg/remoteexec` (Phase 72's "not in `pkg/`" is
  superseded, recorded in both places and in PATTERNS.md); every platform instance an unexported field,
  held by `TestNoPlatformCircuitIsReachable`. The WinRM transport now has one, keyed by `winrmexec.Addr`,
  counting only network failures before a shell opens.
- **Three defects found and fixed, each with a test that failed first:** FAILURE_PATTERNS 398 (a canceled
  call kept the half-open probe; the probe is now also leased), 399 (SSH sent a refused password three times
  and counted it against the shared circuit; measured 3 then 1 "Failed password" against real OpenSSH 10.3,
  which also blocks the source by `PerSourcePenalties` at the old rate; the user approved the fix as step 2b),
  400 (a WinRM host that never answered was reported as "may still be running" and seen by no retry or
  breaker).
- **Transports checked at plan time and run time.** `pkg/capability/transports.go` (vocabulary and the
  capability reaching each), `collection.CheckTransports` shared by `validate`'s `TransportRule`, the engine
  and the `pkg.*`/`svc.*` dispatchers (which now declare their concrete methods' union). `WindowsShellCapable`
  is a child of `CommandExecCapable`. Proven through the binary and on the real lab inventory (`bsd-lab`,
  `hosts: lab`).
- **Industry comparison, on the real host `vengeance`, certificate auth, keys handed over in `memfd` only:**
  per command at parity with pywinrm 0.5.0 (3 to 7 ms faster at p50); per run `pleiades adhoc` p50 85 ms
  against `ansible-playbook` `win_command` p50 1.233 s. pywinrm installed with `pip --user` (user's choice).
- **Verified:** race runs of every touched package (`-count=2` on the breaker packages), the whole
  `cmd/pleiades` suite, every real-host WinRM gate (package and binary), `make fmt`, `make vet`, `make arch`,
  `make docs-lint`, `make gosec` (no new findings), doc regeneration stable. Coverage at or above every
  floor touched; `pkg/breaker` added at 100.0.

### Open

1. **The strict gate.** `make ci` / `make push-gate` were not run (the user's go-ahead is needed on this
   box); `docs-gen-check` passes only once the regenerated pages are committed.
2. **The WinRM VM route was not needed:** the comparison ran on `vengeance`, the host the stress numbers
   came from. `win-lab` authenticates by password from the vault, which no test may extract for pywinrm.
3. Carried from 117a: its env-gated ServiceNow gate against a real instance; Phase 110's strict `make ci`;
   Phase 113's missing Pattern Entry Gate; Phases 12 and 70 have no Implements line.

### Decisions for the user

1. **Behavior change to accept or push back on:** a mixed-fleet runbook (for example `hosts: lab` with an
   SSH method) is now refused whole by `pleiades validate` when some devices cannot be reached over the
   method's transport, instead of running and failing on those devices, as the capability and lifecycle
   rules already do.
2. Commit (messages provided), run the gate, push; then 117b per the standing order.
3. No SECURITY_ATTESTATION control maps to outbound login attempts against managed devices (AC-7 is about
   the platform's own logons); Phase 75 links none. Whether 399 deserves a control is the user's call.

### Files changed

See `git status`. New: `pkg/breaker`, `pkg/capability/transports.go`, `pkg/collection/transports.go`,
`internal/validate/transport_rule.go`, `internal/archtest/breaker_test.go`, the WinRM adapter's breaker and
real-host tests, `pkg/winrmexec` comparison (`compare_release_gate_test.go`, `latency_test.go`,
`silent_host_test.go`, `testdata/pywinrm_bench.py`), `cmd/pleiades` transport and Ansible comparison gates,
the SSH refused-credential tests, six changelog fragments. Changed: `pkg/remoteexec` (breaker, both
fixes), `pkg/winrmexec` (`Addr`, the pre-shell deadline), `internal/transport/winrm`, the engine gate, both
dispatchers, catalogdata, the scaffold, `WindowsShellCapable`, gendocs and regenerated references, docs 01
and 11, CLAUDE.md, FAILURE_PATTERNS 398 to 400, LESSONS 261. Local only: the roadmap and PATTERNS.md.

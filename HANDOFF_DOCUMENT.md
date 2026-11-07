# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**2026-11-07. `main` at `b8a5e065`, clean. No open pull requests and no branch besides `main` (this records branch,
`docs/handoff-2026-11-07`, aside). `stash@{0}` (GNS3 link suspend and capture, back burner) is kept.**

### What works

Merged since the last handoff (#125 to #128 tonight, each with every check green the first time):

- **#124** `c55f91e2`, `fix/open-patterns-batch2` (FAILURE_PATTERNS 448, 560, 551, 954 to 958). It merged before the
  night's queue; the previous entry calls it unpushed.
- **#125** `c95baf51`, Phase 135a, contracts a release keeps: `docs/13-releases-and-stability.md` (the 0.4.0 line and
  the point-release rule), the journal's `format` field (`FormatVersion` 1), `wire.DispatchPayload.PayloadVersion` (1),
  `tools/schemacompat` (direction-aware schema compatibility over the OpenAPI envelope, the module catalog and the
  protocol window; `changelog/*.breaking.md` tokens; no break accepted on a patch line), and golden tests
  (`internal/golden`). FAILURE_PATTERNS 959 to 962.
- **#126** `1d579ebf`, Phase 135b: the external Collection protocol window (`external.Protocols`, `Negotiate`,
  `Description.Range`, `ChildRequest.Protocol`, `ErrNoCommonProtocol`), advertised through `pleiades version` and
  `runner version`, and reserved fact families (`collection.CheckExternalFact`). FAILURE_PATTERNS 963 (`list
  --describe` ran unapproved bytes; submitted to vuln-corpus as C10/CWE-696), 964, 965.
- **#127** `ccf890c4`, Phase 41h PR 1: native fact names (`os.*`, `host.*`, `hardware.*`, `network.*`, `catalyst.*`),
  the `pkg/ansiblefacts` mapping and the register rules. Proven in the lab on the Mac, bsd-check and win-check; the
  live distribution test runs pinned images in CI. FAILURE_PATTERNS 966, 967; LESSONS 409.
- **#128** `b8a5e065`, Phase 41h PR 2: the playbook converter turns a condition on a known Ansible fact into a native
  one, over an inserted, always-tagged `facts.gather` read as `nodes.<reg>[device.id]`. Narrow on purpose: only `==`,
  `!=`, `in` and `not in` against strings convert, with an absent fact guarded as unequal. Orderings, filters (`| int`
  included), pattern tests, truth reads, values with no single native word, localhost plays, tuned gathers and
  shadowed facts stay `when.fact`; setup's `filter` is refused. Gate:
  `TestMigratePlaybook_AFactReadDecidesPerHostAsAnsibleDoes` in `cmd/pleiades` (Debian and Alpine sshds; red without
  the absence guard). FAILURE_PATTERNS 968, LESSONS 410.

Tonight's gates and their times are in the timings log.

### Current blocker

None.

### Open

- (a) Owner questions on #128: should setup's `filter` ever be translated, or stay refused? Should an ordering over
  the major version convert by reading the native integer, which differs from Ansible's string comparison ("10" <
  "8")?
- (b) OpenBSD: Ansible's `ansible_distribution_release` there is said to be "release", "current", "stable" or "beta"
  (research, not checked on a host). The converter would convert `== "current"` cleanly, and the run would then fail
  loudly at the inserted gather, since an OpenBSD host gets no fact-gathering capability. Not a silent wrong answer.
  Owner call: refuse those words in the converter, or document OpenBSD as unsupported.
- (c) `examples/virtualbox_lab/README.md` still shows old fact names in a pasted transcript; it needs a fresh lab run.
- (d) The first-party collection split (cloud and network Collections released apart from the core), raised by the
  owner on 2026-11-06. Design calls open: an external program or a linked module, Landlock being Linux-only, the trust
  tier. Not started.
- (e) Leftover research (2026-11-07, not verified here):
  - 483 (the CI reaper's 60 s readiness wait): image pull time falls outside the wait, so pre-pulling would not have
    prevented it, and `TESTCONTAINERS_RYUK_CONNECTION_TIMEOUT` does not change the library's readiness wait. On the
    next sighting, capture `docker ps -a`, `docker inspect` and `docker logs` of the ryuk container, `docker events`,
    and `journalctl` for docker and containerd, and run with `TESTCONTAINERS_RYUK_VERBOSE=true`. Several packages
    failing in the same second with one reaper ID would point at a shared-creation race under a slow daemon.
  - 690 (a silent `[build failed]` on win-check): `cmd/go` normally prints a tool's exit status even when stderr is
    empty, so the suspect is capture: since Go 1.24, build output arrives as build-output events keyed by the
    `pkg [pkg.test]` action. Cheapest experiment: put a syntax error in a `_test.go`, run the win leg's exact
    invocation, and check that testgate shows it. If it does not, it is a tooling gap in `flakegate.FailureOutput`.
    If it does, capture Windows Application events 1000 and 1001, Resource-Exhaustion-Detector 2004, Defender
    Operational 1116 and 1117, and the commit charge before rerunning.
  - FreeBSD and macOS release: Ansible gives the kernel release (digit-first), which the converter already refuses.
    No hole there.
- (f) API credit: spent tonight (claude-fable-5-1, effort high, batch) rcompat $0.31, rv135a $0.70, rv135b $0.36,
  rv41h $1.04, rv41h2 $0.32, leftover research $0.35; $3.08 in all. The ledger stands at $48.85 of the $92.58 credit,
  so about $43.70 is left, and it expires 2026-11-08.
- (g) `stash@{0}` is the GNS3 work set aside on purpose (`lab-captures-129b`, 2026-10-25): never drop it.

### Next, in order

1. Phase 41h's last item, "Seen versus changed" (its own PR): insert-only fact history, one observation per gather,
   last seen read from the latest observation, in both stores (section 3.1 of the fact-name design note). Not
   started.
2. Phase 135c (written in the roadmap, not built).
3. Then 41d and 41e (both now depend on 41h), 117b, 117c and 105, toward 0.4.0. No tag until the owner says.

### Files changed on this branch

`HANDOFF_DOCUMENT.md` and `HANDOFF_ARCHIVE.md` only (uncommitted).

Machine: unchanged. Lab: unchanged. The Mac, bsd-check and win-check were each ACPI-stopped after the #127 lab gates
and confirmed powered off then. At the end of the night `VBoxManage list runningvms` returned nothing with exit 0, so
no lab VM is running.

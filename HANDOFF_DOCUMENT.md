# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**Branch `feature/Phase-96b-Stream-Ownership`, cut from `feature/Phase-96a-Resilience-Core`'s HEAD
(`725d463`), which is itself unmerged. 96a is committed in three commits on its own branch; 96b is
committed on this one. The user gave live go-ahead to commit between sub-phases.**

Working through Phase 96 end to end. 96a is done and committed. 96b is done and committed. 96c and
96d remain.

### 96b changed design before any code was written, and that is the headline

The phase was specified as **"the Controller owns stream shape; Runners ATTACH and fail loudly if the
stream is absent."** That design is rejected. Six findings, each verified against the tree, are
written into `.SPECIFICATION/IMPLEMENTATION.md`'s 96b section:

1. **Its load-bearing precedent does not exist.** `FAILURE_PATTERNS.md` #178 and the phase text both
   cite `internal/transport/ssh/known_hosts.go` as the fail-closed instinct to copy. That file is not
   in the tree. The real site is `pkg/remoteexec/knownhosts.go`, and it fails ONE CONNECTION rather
   than a process, its alternative is a man-in-the-middle, and it ships a documented bypass.
2. **Attach-only is strictly worse on infrastructure loss.** Today a destroyed stream is recreated by
   whichever process starts next. A running Controller never re-asserts (it provisions once at
   startup), so attach-only would turn a lost stream into a permanent outage.
3. **"The Controller" is already five writers** (HPA `maxReplicas: 5`).
4. **Leader-gating cannot fix that**, and `internal/schedule/scanner.go` already says so about this
   codebase's own election: leadership bounds how many replicas act, it does not make an action unique.
5. **It would falsify a shipped promise.** `docs/10-running-in-production.md` says services need no
   ordering, and nothing orders Controller before Runner in either compose or Helm.
6. **It cannot be observed yet.** No configuration surface for stream shape exists until 96c.

**What was built instead: change the VERB, not the ACTOR.** `topology.ProvisionStream` reconciles the
shape and belongs to `cmd/controller` alone. `topology.AttachStream` binds, creates only when absent,
and never reshapes. `topology.BindStream`/`BindLockBucket` select on a required
`topology.StreamRole` (iota+1, so the zero value is rejectable, per `LESSONS_LEARNED.md` #154 on
required decisions belonging in the signature). The defect closes completely, and self-healing and
start-order independence both survive untouched. The precedent actually followed is
`internal/tlscert`'s lock-free convergence.

**Drift detection is the real deliverable.** Ownership cannot help during a rolling upgrade, when an
old build is no longer reverting anything but is also not applying the new shape.
`topology.StreamConfigDrift` compares only the seven fields this project declares (the struct has far
more and the server fills the rest), order-insensitively on Subjects, and every bind logs a warning
naming each field that differs.

**The lock KV bucket got the same treatment**, and its case was more urgent: `internal/archtest`'s own
comment records that a lowered TTL there "lets two runners execute against one device".
`FAILURE_PATTERNS.md` #196 records why it went uncatalogued for a phase.

### Guard, gate, and evidence

- `internal/archtest.TestOnlyTheControllerProvisionsSharedInfrastructure` matches the symbol NAMES
  (`StreamProvisioner`, `ProvisionStream`) against a one-entry allowlist, because an import-graph rule
  provably cannot work (both roots link `internal/event`) and a rule on the `CreateOrUpdate` call
  would be vacuous (one call, inside the owning package). Live-controlled: red when `cmd/runner` is
  given provisioning authority, green on restore. Carries the non-vacuity assertion.
- Six container-backed release-gate assertions in `internal/topology/stream_ownership_gate_test.go`,
  all passing, including the two that matter most: a reader creates an absent stream (the self-healing
  property the rejected design would have destroyed), and a reader does not revert an operator's
  `MaxAge` while warning that it differs.
- `FuzzStreamConfigDrift` clean at 629k execs. `BenchmarkBindStream` measures reader 786us against
  provisioner 2.3ms, so the fleet-wide effect is that the cheaper path became the common one.

### Recorded, not fixed

Four more instances of the same multi-writer provisioning shape, in
`.SPECIFICATION/IMPLEMENTATION.md`'s 96b section: the shared dispatch consumer, `Bus.Subscribe`'s
durable consumer, the SSE log viewer's per-request consumer, and most seriously **database migrations,
which `internal/ent`'s `Apply` runs from every Controller replica with no advisory lock**, so two
replicas racing a fresh Postgres install can both run the first migration.

### Environment note that costs a CI run if missed

`make ci` needs `LOCALSTACK_AUTH_TOKEN` or `internal/catalog/cloud/aws/{ec2,s3}` skip their tests and
the coverage ratchet fails with what looks exactly like a real regression. The token IS in
`~/.bashrc`, but at line 111, BELOW the stock `[ -z "$PS1" ] && return` at line 15, so plain
`source ~/.bashrc` and `bash -lc` both leave it unset. Only `PS1=x; source ~/.bashrc` works. The Go
tool PATH block is fine: it sits at line 9, above the early return.

### Next step

96c (one outage budget, three derived constants, and the D3 inversion), then 96d (`wss://` traversal,
server-side TLS, `NATS_URL` scheme allowlist). Note 96c is where stream shape finally becomes
configurable, which is what makes 96b observable at all.

**Separately, and unrelated to this work:** `main`'s GitHub Actions is red at `test-repeat`.
`pkg/remotesvc`'s `TestOperations_NonZeroExitIsAnError` fails at `-count=3` with `EOF` where it wants
"masked", the same class as `FAILURE_PATTERNS.md` #188. That package is not in `flaky-packages.json`,
so it fails hard.

# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**Branch `feature/Production-Packaging`. Directive: plan and build Phase 20, Production Packaging.
Stages 20a, 20b and 20c are BUILT. Phase 20 is NOT closed: 10 of 19 items are ticked and the nine
that remain are named below. Nothing is committed. Phase 20a's handoff moved to `HANDOFF_ARCHIVE.md`.**

### What is done and proven

- **Images.** Both distroless (`gcr.io/distroless/base-debian12:nonroot`), digest-pinned, non-root at
  a NUMERIC uid, stripped, with OCI provenance from build args. Build context 410 MB to 13 MB.
- **Compose.** Named volumes, real healthchecks on every service, warm start about 4 s.
- **TLS terminates in the controller**, and the insecure cookie path is DELETED rather than disabled.
  `make gosec` is 9 findings against 12, with **zero `G124`**, which is the promise Phase 79's
  Security Analysis was amended on.
- **Certificates self-provision** when the admin configures none, and the provisioning is LOCK-FREE.
- **Helm chart** is real: two Deployments, two StatefulSets, four liveness and four readiness probes,
  zero `:latest`, non-root throughout, per-kind name budgets.
- **`FAILURE_PATTERNS` #119 is CLOSED**, proven by severing a real broker under a real runner.

### The nine open items, honestly

`/readyz` bounding is **not implemented** and is the one open item that is code rather than writing.
The endpoint is unauthenticated, unrate-limited, runs a real query per request, and nothing sets
`MaxOpenConns`, so a caller can flip a healthy controller out of rotation today. Single-flight
collapse is the fix and the write-probe alternative was tested and rejected; the item records why.

The other eight are the gate items: Pattern Entry Gate, Fuzz/Stress, Security Analysis, Adversarial
Pattern Justification, Schema/Injection Hardening, Documentation Gate, Release Gate, and Provide
Commit Message. Much of the underlying work exists (the release-gate tests are written and pass, the
docs are updated, `namesFrom` is fuzzed); what is missing is the written justification each gate
requires, which is the deliverable and not a formality.

### The lesson this phase kept teaching

Three separate designs for certificate provisioning were built and two were torn out, and each time
the adversarial pass found the same shape: **a mechanism that made one participant's bad state
everyone else's problem.** First a fail-closed refusal, then a claim lock whose dead holder froze
every sibling, then an ownership rule so broad that unparseable bytes bricked a directory forever.
The design that survived removes the shared decision entirely: one atomic file, load-generate-load,
losers re-read. When a fix keeps growing new faces, the primitive is wrong.

### Governance corrected, and it took three attempts

`gosec-waivers.json`'s header demanded "zero remaining waivers" before Phase 20 and attributed that
to AGENTS.md. **AGENTS.md never said it.** The bar came from Phase 0's policy and was copied with a
false attribution; both are struck. My first two corrections of it were themselves wrong, in exactly
the way `LESSONS_LEARNED` #112 records, and were caught by adversarial passes that recomputed every
number rather than by review. **Phase 82** now owns the seven waivers that pointed at closed Phase 39.

### New phases recorded this session

- **Phase 82**, retiring the inherited `gosec` waivers.
- **Phase 83**, the setup command, including the data-loss discipline: guards that scale with blast
  radius, detection rather than warnings, typed confirmation, and a recovery matrix printed at the
  moment a key is created.
- **Phase 84**, upgrade, rollback and restore, which found that concurrent `migrate.Apply` is a race
  (`schema_migrations` has `version TEXT PRIMARY KEY` and no lock) and that rollback across a schema
  change does not work today.

### Next

Implement `/readyz` single-flight, then write the eight gate justifications, then close.

### Commit message, provided per the standing instruction (not committed)

```
feat(packaging): a product that installs, over TLS, on a clean machine (Phase 20a-c)

Phase 20 opened by correcting its own map. Four of its items described a
repository that no longer existed: both binaries compiled, both Dockerfiles
already built package paths, and Phase 19 had deleted the UI service. The
NATS healthcheck was broken twice over, and both halves were verified
against the real image before either was touched.

Images are distroless, digest-pinned, stripped and non-root at a NUMERIC
uid. Numeric matters: USER nonroot:nonroot makes every runAsNonRoot pod
fail with CreateContainerConfigError, and Compose cannot express
runAsNonRoot, so no check here could see it. cgo stays on, because
CGO_ENABLED=0 compiles clean and then dies in the first migration on the
controller's own default DSN. The shipped Alpine image was already broken
that way.

The controller terminates TLS and self-provisions a certificate when the
admin has configured none, so nothing serves plain HTTP unasked and nothing
refuses to boot for want of a certificate. Provisioning is lock-free: one
atomic bundle, load-generate-load, losers re-read. Two earlier designs were
built and torn out because each made one participant's bad state everyone
else's problem.

The insecure cookie path is deleted rather than disabled. gosec goes from
12 findings to 9 with zero G124, which is what Phase 79's Security Analysis
was amended on the strength of. The premise those waivers rested on was
false: browsers accept Secure cookies on localhost, and the real defect was
that every non-loopback origin failed as a misleading wrong-password
message while the password was never checked.

docker-compose.yml gains named volumes, and that is the sharpest fix here:
it declared none, so every docker compose down destroyed the control plane
database, the JetStream store and the scheduler leases.

The Helm chart replaces nginx scaffolding: two Deployments, two
StatefulSets, four liveness and four readiness probes on separate paths,
per-kind name budgets, non-root throughout. It refuses to render without an
explicit master encryption key, because a generated one would differ on the
next helm upgrade and everything stored would become permanently
undecryptable with no error.

The runner gets a liveness surface driven by its consumer answering, not by
a ticker, closing FAILURE_PATTERNS 119 with a test that severs a real
broker under a real runner.

Also corrects a governance rule that was never real: gosec-waivers.json
demanded zero remaining waivers before this phase and attributed that to
AGENTS.md, which never said it. Struck at its origin in the Phase 0 policy
and in the header that copied it.

FAILURE_PATTERNS 119, 122-126. LESSONS_LEARNED 112-114.
```

### Resuming after a context compaction

Everything needed is on disk; nothing is held only in conversation.

1. **`make ci` is RED**, and this is the result of the re-run the previous version of this
   sentence asked for, so trust it over any earlier claim. Exactly one test fails:
   `TestPackagingReleaseGate_KubernetesInstall` in `tests/e2e`. Everything else, including the
   whole non-integration half and `tests/e2e`'s other cases, passes.

   The failure is at that test's last-but-one assertion,
   `assertALongReleaseNameStillProducesFourWorkingWorkloads`. Every assertion before it passed
   against a real cluster: the chart installed, `/readyz` reported its database and broker,
   `bootstrap-admin` ran through `kubectl exec`, and the runner Deployment reached Available with
   its in-pod `runner healthcheck` reporting an 8-second-old heartbeat. Then the second install,
   at a 53-character release name in its own namespace, sat at `Available: 0/1` for its full
   8-minute budget, after which every `kubectl` and `helm` call returned
   `connection refused` against the kind API server. The control plane went away mid-test.

   That last detail is what makes the result ambiguous rather than a verdict on the chart. Two
   candidates, and the log cannot separate them:

   - The cluster was deleted out from under the running test. The gate names its cluster
     `pleiades-release-gate`, and a cleanup ran `kind delete cluster --name pleiades-release-gate`
     while this run was still in its integration stage.
   - The single-node cluster fell over carrying two full releases at once. The long-name case
     installs a second postgres, nats, controller and runner beside the first, which is still
     installed at that point. There are no OOM kills in the kernel log, so if this is the cause it
     is not a host memory ceiling.

   The `connection refused` is evidence for the first: a node under load produces timeouts and
   `NotReady`, not a refused TCP connect on the API port. Settle it by running the test alone,
   which is safe: it writes its kubeconfig into its own `t.TempDir()` and passes `KUBECONFIG`
   explicitly to every command, so it cannot touch `~/.kube/config` or the `desktop` cluster.

   ```
   go test -tags integration -race -count=1 -timeout 45m ./tests/e2e/ \
     -run TestPackagingReleaseGate_KubernetesInstall -v
   ```

   **Resolved.** It passed alone: 259 seconds, all six assertions, and the install that had
   consumed its full 8-minute budget finished in 67 seconds. The gate is sound and the `make ci`
   failure was environmental. `FAILURE_PATTERNS` #141 and `LESSONS_LEARNED` #129 record it.

### The break-glass

`make break-glass` (`tools/breakglass`, `//go:build devtools`) returns the machine to the state
every test assumes it starts from: no throwaway kind cluster, no compose project holding a
database from a previous run, no containers left by a test binary killed before its cleanup ran.

Reach for it the moment a gate fails in a way that does not match the code you changed. That is
the failure shape above, and it is not rare: leftover infrastructure never announces itself, it
surfaces as a test failing at whichever assertion touched the stale state.

- `make break-glass BREAK_GLASS_FLAGS=-n` says what would go and removes nothing.
- `BREAK_GLASS_FLAGS=-images` also drops the built images, so the next run builds from nothing.
- `BREAK_GLASS_FLAGS=-force` cleans through the live-run guard, breaking that run.

Two properties it is worth knowing are deliberate. It is **not** `docker system prune`: prune is
defined by what is unused, which is a fact about the daemon rather than about this repository, so
it would take the long-lived `desktop` cluster with the same confidence it takes ours. Every
removal is positively attributed to this repository first and everything else is listed and left.
And it **refuses while a run is live**, asking whether a testcontainers reaper is running and
whether a `go test` process has its working directory inside this repository, because cleaning up
underneath a run is how the tool came to exist. Verified against a genuinely live `make ci`: it
refused, exited 1, and the gate's cluster survived.

The gate's own delete-first is unchanged, so **two concurrent runs still destroy each other**.
The fix is a per-run cluster name with prefix-matched reclamation, or a liveness check before the
delete. Neither is written and nobody owns it; `FAILURE_PATTERNS` #141 states both options.

### The coverage regression the flakes were hiding

`make push-gate` reached the ratchet for the first time and failed it: `internal/runner` at
85.4% against a floor of 86.8. The regression is in this phase's own committed heartbeat work,
and it had been invisible for three runs because `make ci` stops at its first failure and every
one of those runs died earlier, at `test-integration`, on container flakes. That is
`LESSONS_LEARNED` #110 exactly, and it is the reason a red gate must be cleared rather than
explained: everything behind it is unobserved, not passing.

Three functions were at 0%: `WithHeartbeat`, the option that wires the whole feature into the
Agent; `detachedValueContext`'s accessors, which are what let a non-interruptible execution
outlive `Agent.Run`'s shutdown; and `StaleHeartbeatError.Error()`, the message an operator reads
off a failed probe. `internal/runner/heartbeat_wiring_test.go` covers all three and takes the
package to 87.4%. The floor was not moved, and `coverage-check` reports 160 packages with none
below their recorded floor.

Both new tests are negative-controlled by mutating the source and watching them fail. The first
version of the `WithHeartbeat` test could not fail at all: `liveness` is a concrete `*Heartbeat`,
so asserting it is nil after `WithHeartbeat(nil)` passes whether or not the guard exists. The
guard's real contract is about option ORDER, and `LESSONS_LEARNED` #130 records the shape.

**Three separate tests written this session could not fail on first writing**, and source
mutation caught every one where reading caught none. Treat that as the expected rate, not as a
run of bad luck.
2. The one open item that is CODE is `/readyz` single-flight bounding. Phase 20's own item states
   the design, the measured numbers, and why the write-probe alternative was rejected.
3. The eight remaining gate items need their written justifications. The evidence for most of them
   already exists in the tree; what is missing is the prose each gate asks for.
4. Verify before trusting any claim in this document. Three separate corrections this session were
   wrong on first writing and were caught by recomputing from source rather than by review.

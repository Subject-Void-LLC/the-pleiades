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

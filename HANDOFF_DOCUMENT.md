# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**Branch `feature/launch-fields-and-push-gate`. Directive: plan and build Phase 22, Credential Types
and the Injector Engine, aiming for AWX parity. Everything below is uncommitted, held per standing
instruction. A commit message for 22a was prepared and given; 22b's is not written yet.**

**STAGES 22a AND 22b ARE COMPLETE.** Stage 22a's own status is in `HANDOFF_ARCHIVE.md`. This
section covers 22b: the injector engine, both adapters, and the release gates. Stage 22c
(managed-type data, the two UI views, the AWX import CLI, the Book 10 docs) is not started.

### What 22b built

**The injector engine (`internal/credtype`).** Five Targets behind a `pkg/registry` Registry rather
than a five-armed switch, in two phases: `file` and `vault` run first, then the reserved filename
namespace is resolved, then `env`, `extra_vars` and `machine`. The ordering is forced rather than
chosen, because `{{ tower.filename.cert }}` does not exist until the file target has decided where
the cert file goes.

`secretTracking` wraps every Target, including one added later by somebody who never reads that
file, and it is the control rather than a convenience: it diffs the artifact instead of asking the
Target what it did, so it needs no knowledge of any Target's internals and works identically for one
that does not exist yet.

`Combine` refuses five kinds of collision (env, extra-var leaf, file path, two machine identities,
two vault credentials sharing an identifier) and names both credentials in every message, because
the operator seeing it has to choose which one to drop. Nested extra-variable maps deep-merge;
only a leaf written twice is a real disagreement.

**Machine and vault are real Go code rather than data**, and for one reason: their output is not one
of the three data targets. A machine credential resolves to the flattened identity the transport
authenticates with, and a vault credential to a password file plus the `--vault-id` argument naming
it, which no injector document can produce. Every other input a machine type declares, `become_*`
included, is an ordinary input its own injector document can reference, which is why there is no
special case for it.

**External secret sources**: the port, one real implementation (`internal/credtype/lookup/file`), and
eight declared-not-implemented under AWX's own namespaces so an import maps onto them and reports
what is missing rather than "no such source". The `file` source refuses anything that is not a
single plain filename, which is stricter than cleaning a path and checking the result, and
deliberately so: there is then no traversal to check for.

**Injection happens at fan-out, not at launch**, and `internal/dispatch/inject.go`'s package comment
carries the four reasons. The consequential one: a job record carries credential ids and nothing
else, so a database backup or a badly-scoped read of the job history contains no secret at all.

**The precedence rule**: a machine credential bound to the TEMPLATE authenticates every device in the
fan-out (AWX's semantics), and the per-device file store is the fallback when the template binds
none. That is what keeps every dispatch that worked before this phase working unchanged, including
the whole Walk tier.

**The argv leak is fixed.** `buildArgv` now takes a `bool` rather than the extra variables, so no
value is in scope for it to emit; the variables reach `ansible-playbook` as `-e @file`,
unconditionally. `ContainerSpec` gained `SecretEnv`, kept apart from `Env` so anything printing a
spec prints the safe half, merged by the orchestrator immediately before the container starts.

**The native path refuses honestly.** `env` and `file` injectors are refused at bind time
(`PUT /templates/{id}/credentials`, 409 naming the offending targets) and again at run time in
`internal/adapters/native`. The rule itself lives in `internal/adapters/routing` with one
implementation and two callers; putting it in the native adapter would have dragged
`internal/transport/ssh` into the Controller binary so an HTTP handler could compare a string.

**Prompted credential inputs are never persisted, structurally.** `LaunchTemplate` takes them as
their own parameter and `recordConfig` takes only `launch.Config`, so the function that writes to
the database is not handed the value that must not be written. They travel on the `job.requested`
event, which is the only place they can: the fan-out worker runs on every controller replica, so the
replica that served the launch and the one that fans it out are routinely different processes.

### Three findings, all from running things for real

1. **FAILURE_PATTERNS #120**, found by the release gate on its first real run: the legacy adapter
   registered EVERY injected value with the masking set, so the gate reported an environment of
   nothing but `********`. Secrecy is not recoverable downstream (a token and a region are the same
   shape by then), so `wire.Injected.Mask` now carries it explicitly. Over-masking is not the safe
   direction: it corrupts the operator's own debugging output permanently and protects nothing.

2. **`CheckValues` refused a credential whose required input lives in an external source**, which
   made an externally-sourced credential impossible to create. Found by writing the resolver's own
   external test. Fixed by giving `CheckValues` the external map, which also let
   `credstore.checkExternalRefs` be deleted: it was the same rule written twice.

3. **The OpenAPI generator emitted no `requestBody` for any endpoint**, so more than twenty
   declared `RequestSchema` values were computed and never read: FAILURE_PATTERNS #116's shape in
   the docs generator. The published document said how to call every endpoint and not what to send
   to any of them, so a generated client could read a credential and not create one. Fixed, with a
   test asserting every endpoint declaring both fields publishes a body. **Sixteen unrelated
   endpoints declare a `RequestSchema` with no `RequestContentType` and are still skipped**; that is
   pre-existing, unrelated to credentials, and belongs in its own commit rather than bundled here.

### Verification status

Green: `build`, `vet`, `fmt`, `gosec` (10 findings, all waived), `docs-lint`, `make arch`, the
tolerant coverage ratchet (155 packages, none below floor), and `go test ./...`.

Both release gates pass against real containers AND were each proven to fail on the defect they
exist to catch, by reintroducing it:

- `cmd/runner/ansible_injection_release_gate_test.go`: two runs, one proving arrival byte for byte
  against `testdata/awx_reference_env.json`, one proving absence in the container spec's argv, in
  `/proc/1/cmdline` read from INSIDE the container, in every job event, and in every byte the masked
  logger wrote. Negative control: restoring the `-e <json>` form fails both the argv assertion and
  the file-reference assertion.
- `tests/e2e/credential_injection_test.go`: the whole chain through the real binaries, with the
  playbook printing a SHA-256 so arrival is proven without printing the value. Negative control:
  dropping `applyInjection` fails it.

Fuzzers, each run clean: `FuzzInjectDeclaresEverySecretItRenders` (1.0M execs, both directions of the
secrecy property), `FuzzCombineNeverSilentlyPicksAWinner` (1.1M), `FuzzBuildArgvNeverCarriesAValue`
(487k). The argv fuzzer immediately found a flaw in its own first assertion, which is worth knowing:
a launch whose `limit` field is literally `-e` produces `--limit -e`, so scanning argv for `-e`
reads a VALUE as a flag. The check is at the tail now, where the flag can actually be.

**`make docs-gen-check` fails until the generated files are committed**, which is expected and not a
defect: the generator's output is verified stable across runs, and `docs/reference/schemas/openapi.json`
plus `internal/api/wellknown/openapi.json` are modified in the working tree and must be committed
together with the `apispec` and `tools/gendocs` changes.

### Next step

Stage 22c: the managed-type data (`internal/credtype/managed/*.json`, one file per AWX type under its
exact name and namespace, plus the five declared-not-implemented), idempotent reconcile at controller
startup keyed on namespace rather than a migration, both UI views (including revising
`internal/ui/resources/credentials`' own package doc, which currently promises no enumeration and
must record in writing that the promise was revised and why), the launch form's prompted-credential
controls (`credential_<id>_<inputid>`, mirroring the `answer_` prefix), the
`pleiades import awx-credential-types` CLI, and the Book 10 security section.

Two things are already known and should not be rediscovered. The UI launch form passes `nil` for
prompted inputs today, with a comment naming the follow-up: a template bound to a prompting
credential fails at fan-out with a reason naming the input, which is loud rather than silent, but it
is a real gap. And `internal/adapters/native`'s named follow-up for file injection is
`sdk.RunbookContext.InjectFiles()` over the existing stdin plus fd-3 child channel, where content
would live in the per-task subprocess's memory for one task and never touch a filesystem; do not
invent a tmpfs on the Runner to close it, which would be building a new secret-at-rest surface to
satisfy a checklist.

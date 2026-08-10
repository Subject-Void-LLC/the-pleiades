# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**This session built Phase 17 (Legacy Ansible Adapter) in full**, the next unbuilt phase after Phase 16
(Native Go Execution Adapter, landed on `main` at the start of this session). Branch is still `main`.
**Nothing is committed** (the user asked for a plan, approved it, and the session proceeded to
implement; committing was never requested). Real Go code changed, real tests pass, real containers ran.

**What's real.** `internal/adapters/legacy` (renamed and rebuilt from the old `internal/ansible`, whose
`ReceptorAdapter.StreamMockJob` fabricated events from three hardcoded arrays): a real
`ContainerOrchestrator` port with one real `testcontainers-go`-backed Docker implementation; a real
STDOUT parser targeting Ansible's actual `ansible.builtin.default` text callback at `-v` (not the `json`
callback PLAN.md's own prose might suggest, which does not exist in any maintained Ansible -- verified
empirically, `FAILURE_PATTERNS.md` #90); a real `inventory.json` generator targeting Ansible's actual
"yaml" inventory plugin schema (also verified empirically, not the dynamic-inventory-script shape that
turned out not to parse); a real playbook resolver; and `legacy.Adapter`, which ties all of it together
and genuinely implements `runner.ExecutionAdapter`, making PATTERNS.md's Strangler Fig claim true for the
first time (it was false from Phase 2 through Phase 16). The Release Gate
(`cmd/runner/ansible_release_gate_test.go`) dispatches a real job through a real NATS broker to a real
`runner.Agent` holding a real `legacy.Adapter`, which provisions a real container (built from the new,
committed `Dockerfile.legacy-ansible-runner`) on a shared Docker network with a second ephemeral `sshd`
container, runs a real playbook over a real SSH connection, and asserts the real parsed `wire.JobEvent`s
-- including a genuine wrong-password negative control. `internal/adapters/legacy` measures 90.3%
coverage. `make build vet fmt`, `go test -race` (including the release gate), `make gosec govulncheck
docs-lint docs-gen-check` all pass clean, no new findings.

**What's explicitly not real, so the next session does not assume otherwise.** `cmd/runner/main.go`
still only composes `native.Adapter` -- nothing routes a real dispatch to `legacy.Adapter` yet, since no
Phase 21 Launchable Kind registry exists to choose per job. One device per dispatch, never a whole play's
host list (Phase 24's own open problem). Events are a post-hoc batch parse, not Phase 25's future live
stream. No GitOps auto-discovery, no Galaxy/pip dependency caching, no Kubernetes container groups (Phase
26). Host key verification is disabled inside the container. A passphrase-protected SSH key is rejected,
not handled. The full list, with reasoning, is Phase 17's own closing note in
`.SPECIFICATION/IMPLEMENTATION.md`.

**Real wire-format change:** `pkg/wire.DispatchPayload` gained a `Tags []string` field (populated at
`internal/dispatch/worker_devices.go`'s one real construction site), and `wire.JobEvent`'s doc comment
was updated now that the `AnsibleEvent` struct it names is actually gone -- folded in this session,
closing an item Phase 16 deliberately deferred.

**Documentation updated for real:** `docs/03-migrating-from-ansible.md` (new "Running an unconverted
playbook" section, honestly noting no CLI/API path selects it yet), `docs/01-start-here.md`
(Implementation status narrative and tier table, correcting the prior implication that all Ansible
interop belongs to the unbuilt Run tier), a changelog fragment
(`changelog/legacy-ansible-adapter.added.md`). `go generate ./... && git diff --exit-code` is clean:
this phase adds no Collection method, CLI flag, API route, capability, device type, or sync plugin.

**Gitignored spec docs updated too** (real work, not committable): `.SPECIFICATION/IMPLEMENTATION.md`
(Phase 17's own checklist checked off with full closing notes, and a stale Phase 39 cross-reference that
had misattributed the `ansible-playbook` command-injection standing requirement to "Phase 25 alone"
corrected to name Phase 17), `.SPECIFICATION/PATTERNS.md` (Adapter, Strangler Fig, Anti-Corruption
Layer, Bulkhead, and Feature Flag entries all corrected to match the real shape built), two new
`FAILURE_PATTERNS.md`/`LESSONS_LEARNED.md` entries (#90 and #93) recording the empirical discovery that
PLAN.md's own Ansible-integration prose described tool behavior that no longer matches a real, current
`ansible-core` install.

**Next step.** Nothing wires `legacy.Adapter` into a real composition root yet. The two most natural next
phases are Phase 21 (Launchable Kind registry, needed before any real per-job adapter routing can exist)
or Phase 25 (The Ansible Callback Bridge, which replaces this phase's batch parse with real live
streaming). Neither is started.

**Files changed this session:** `internal/adapters/legacy/*` (new package, ~15 files, replacing
`internal/ansible`, deleted), `pkg/wire/dispatch.go`, `pkg/wire/job_event.go`,
`internal/dispatch/worker_devices.go` (+test), `internal/dispatch/worker_test.go`, `cmd/demo/main.go`,
`cmd/runner/main.go` (comment only), `cmd/runner/ansible_release_gate_test.go` (new),
`internal/archtest/layering_test.go`, `Dockerfile.legacy-ansible-runner` (new),
`docs/03-migrating-from-ansible.md`, `docs/01-start-here.md`, `changelog/legacy-ansible-adapter.added.md`
(new), plus the gitignored spec files listed above.


---

Full session-by-session history (every `## Previous session: ...` and `## Files changed in the ... session` entry) lives in [`HANDOFF_ARCHIVE.md`](HANDOFF_ARCHIVE.md), kept out of this file so it stays cheap to read every session. Read the archive only when you need a specific past session's detail.

When Current Status above is superseded, move the outgoing text into `HANDOFF_ARCHIVE.md` as a new `## Previous session: ...` entry at the top of that file (before its current first entry), then overwrite Current Status here. Never delete a past entry.

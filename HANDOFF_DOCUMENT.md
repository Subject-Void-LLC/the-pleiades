# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**Branch `feature/Phase-40-Run-Journal`. Build steps 1 through 16 of
`.SPECIFICATION/PHASE40_MASKING_DECISION.md` Section 8 are DONE, committed, and green. Step 18 is
done and found a real defect. Step 19 is half done. Steps 17 and 20 remain.** The previous
session's entry (steps 1-10 written but uncommitted) is now in `HANDOFF_ARCHIVE.md`.

### The eight commits

```
ef7c96a test(archtest): forbid internal/engine from importing internal/ent
525fa22 feat(commitgate): refuse a commit that breaks a rule a machine can check
533d0b9 feat(engine): record every node execution in a run journal
1060ad0 docs(lessons): record what a "no input survives" fuzz assertion needs
8fe51b3 feat(journal): write the Crawl tier's run journal to disk
b0826f5 feat(journal): publish the Walk tier's run journal onto the job's subject
f19602f feat(journal): store the Walk tier's run journal and consume it
631f23e docs(journal): document the run journal, and correct a claim false since Phase 16
ab08e70 fix(journal): acknowledge a batch the store can never accept (FP #208)
```

Nothing is pushed. The mechanism now runs end to end on both tiers: a Crawl run writes
`<dir>/.pleiades/journal/<run-id>.jsonl`, and a Walk dispatch publishes a batch per topological
level onto `pleiades.jobs.journal.<job>` which the Controller stores in `journal_entries`.

### What the gates actually said

`go build` 0, `go vet ./...` 0, `make fmt` 0, `make arch` 0, `make gosec` 0 (9 findings, all
pre-existing and waived), `make docs-lint` 0, `make docs-gen-check` 0. `-race` green on
`internal/journal`, `internal/engine`, `internal/archtest`, `internal/adapters/native`,
`cmd/pleiades`. Coverage: `internal/journal` 94.7 (new floor), `internal/engine` 95.9 against
95.2, `internal/adapters/native` 93.6 against 92.9, `internal/topology` 94.8 against 94.6,
`tools/commitgate` 91.7 (new floor).

**`make ci` in full was NOT run.** See the blocker below.

### The one defect found, and how

Step 18's Schema and Injection Hardening audit was written as tests rather than prose, and it
found a poison-message bug on its first pass. A deliberately hostile payload (a 2,000-level
nested object) DECODED cleanly into a `Batch` holding one entry with an empty `Outcome`; the
store refused it correctly; the consumer classified every store failure as transient and asked
for redelivery forever. The split was drawn on "which layer said no" rather than on "could the
same bytes ever succeed". Fixed with `journal.ErrUnstorable`, tested in both directions, recorded
as `FAILURE_PATTERNS.md` #208.

### Six departures from the design document, all recorded in its new STATUS header

`JournalEntry` carries snake_case json tags (the alternative was a 28-field mapping function that
drifts); the sink does not re-detach the context because `recordLevel` already does; `Batch` lives
in `internal/journal` rather than `internal/adapters/native` or `pkg/wire`; one domain package
holds the file sink, the ent store, the consumer and the attempt key; the idempotency key is
`journal:<job>:<device>:<attempt>:<first>-<last>`; and `EntStore.Save` returns a written count.

### Side quest, unrelated to the journal: `tools/commitgate`

`.githooks/pre-commit` and `.githooks/commit-msg` now run a gate over the STAGED content and the
commit message, in well under a second. It refuses an em dash in any added line, a staged Go file
gofmt would rewrite, a Go file the commit adds with no docstring, an ent schema edit with no
regenerated code, a new entity missing either dialect's migration, a non-conventional subject, and
a trailer crediting a model as an author. Soft rules warn. It caught three real things while being
built: a missing docstring in the previous session's `journal_internal_test.go`, its own missing
docstring on the new ent schema file (which every other schema file is also missing, a separate
cleanup), and its own first commit, because `text.go` has to name the character it forbids. Every
dash in that package is now written as a code point.

### THE BLOCKER, unchanged in kind but now measured properly

`make ci` still cannot go green in this environment, and the previous session's diagnosis of it
was WRONG in a way worth correcting. It named `internal/catalog/pleiades/builtin/wait` and
`pkg/remotefile` as failing for "a testcontainers reaper or port-mapping error". Neither package
imports testcontainers at all, and neither is in the Makefile's `DOCKER_DEPENDENT_PACKAGES`.
`wait` opens real `net.Listener`s on ephemeral ports and takes 13.8s alone; `pkg/remotefile` runs
`remoteexectest.Start`, an in-process real-TCP real-SSH server, and takes 0.13s alone. Both pass
in isolation. So whatever they are, they are a real-TCP-port failure under saturated parallel
load, not a Docker one, and copying the container boilerplate into `flaky-packages.json` would
produce exactly the unexamined entry that file's own header warns about.

**This still needs a decision and it is the first thing to settle.** Reproduce once under a full
`make ci`, capture the actual failure text per package, then choose. `pkg/remotefile` at 0.13s is
the suspicious one: a package that fast failing under load looks more like a real bind race in the
harness than contention.

### Next step

**Step 17, the Walk-tier redelivery gate**, is the only build-order item left with real risk. In a
real-NATS environment, force a task failure so the dispatch is Nak'd to exhaustion, then read the
journal back raw and assert `MaxDeliver` distinct `Attempt` values under one `JobID`, ordered,
with no duplicate `(JobID, DeviceID, Attempt, NodeID)` tuple. Then SIGTERM a Runner mid-run and
assert the last completed level's rows are present. `cmd/runner/ssh_mesh_release_gate_test.go` is
the shape to extend. The Crawl half of step 17 is already done and mutation tested
(`cmd/pleiades/journal_release_gate_test.go`).

Then step 19's remainder (the Book 12 pointer in `IMPLEMENTATION.md` needs correcting rather than
guessing at, see Section 10) and step 20 (the human dogfood pass, which must not be skipped:
`register_mask` once shipped with every one of its own tests green while masking nothing).

### Two smaller things worth doing while in these files

`internal/ent/schema/*.go` all lack a file docstring except the one added this session. And
Section 8's own "Corrections" paragraph is still undischarged: the dead `credential.Mask` citations
(now at `executor.go:248` and `dag.go:237`, not `:130`), `RunResult`'s "order each one finished"
claim, and the three `pkg/collection.Inverse.Captures` references to a type that no longer exists.

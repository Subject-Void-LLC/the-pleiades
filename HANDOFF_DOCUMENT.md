# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**Branch `feature/Catalog-First-Tier`, off `main`. Directive: Phase 38's first tier, build out the
Collection catalog. `exec.command` is BUILT, wired, proven against a real device and flipped to
`implemented`; the catalog reads 6 of 76. The other five first-tier modules (`exec.shell`,
`file.copy`, `file.directory`, `svc.*`, `pkg.*`) are open. Nothing is committed, per the standing
instruction; the commit message is at the bottom of this section.**

### Why this phase, out of order

Phase 38 sits in Part VIII, well below Phase 23, and taking it first was deliberate: a scheduler
multiplies whatever the platform does, and the platform did very little. Every engine under the
catalog is real and proven while the catalog on top of it could not copy a file, install a package
or start a service. The reasoning is written into `IMPLEMENTATION.md`'s Phase 38 section so the
next reader does not read the ordering as an accident.

### The three blockers that had to close before any module could be written

Each was a map correction, made before code, and each is recorded in `IMPLEMENTATION.md`.

1. **The recorded "71 stubs carry the OLD signature" trap is stale.** All 76 already carry the
   current one. Settled with the compiler, not grep: a throwaway test assigned all 76 exported
   methods to a `[]collection.Method` literal and the package compiled. So the
   full-regeneration-versus-per-module-migration decision the roadmap asked a future session to
   make once has no subject.
2. **The Walk tier handed every Collection method an empty secret set.** `pleiades run` could not
   run `net.ssh.ping` or any `net.catalyst.*` method at all, failing with an authentication error
   against a device whose credential was on disk. `engine.RunbookContextFunc` now takes a context
   and returns an error, and `engine.NewCredentialRunbookContext` resolves the stored credential.
   `FAILURE_PATTERNS` #144.
3. **No device type implemented `CommandExecCapable`**, so `exec.command` was unreachable by
   admission before it was unimplemented in body. `linux.Server` gained `WorkingDirectory()` and
   `ShellPath()` and now declares `ShellExecCapable`, which resolves upward to satisfy both.

### The shared primitive, which is the load-bearing part

`pkg/remoteexec` is new and owns the SSH mechanism once: dial with retry and backoff, the
per-target circuit breaker, fail-closed known_hosts verification, turning secrets into exactly one
authentication method, POSIX quoting, and POSIX word splitting.

The decision it settles, recorded rather than left implicit: the mechanism **moved** rather than
being duplicated. `internal/transport/ssh` keeps its `transport.Transport` identity, its
`credential.Credential` translation and its `Options` surface (now a type alias) and is about 60
lines of adapter. Its container tests against a real, independent sshd pass **unchanged**, which
is the proof the move preserved behavior. `net.ssh.ping` was refactored onto the same primitive
and its existing tests pass **unchanged**, which is the proof the primitive is usable from a
Collection. `FAILURE_PATTERNS` #143, `LESSONS_LEARNED` #132.

Two design points worth knowing before the next module:

- `remoteexec.Auth` keeps its secret in unexported fields with no accessor, so there is nothing to
  redact rather than four redaction methods to keep in step with `internal/credential.Credential`.
- `remoteexec.Shared(opts)` memoizes one Runner per Options for the process. A Collection method
  is invoked once per task with nowhere to keep a Runner, so `New` every time would carry a
  breaker that never opens. It buys nothing under the Crawl tier's per-task subprocess, and says
  so.

### What `exec.command` establishes for the rest of the tier

`Changed` is true whenever the command ran and false only when it did not. A command cannot be
inspected, so anything else would be a guess dressed as a fact, and that makes `creates`/`removes`
load-bearing rather than convenient: they are the only way a task built on this method becomes
idempotent. Run twice with `creates`, it reports changed then not changed, and the second run
opens no session for the command at all. A non-zero exit status is an error, matching Ansible's
own `command` module.

Parameter names are Ansible's throughout (`cmd`, `argv`, `chdir`, `creates`, `removes`, `stdin`),
per the superset rule. `internal/catalog/exec/exec.go` holds what the namespace shares, so
`exec.shell` should be cheap.

### Verification, and the one thing that surprised me

The release gate (`cmd/pleiades/exec_command_release_gate_test.go`) drives the real built binary
through `init`/`add-host`/`add-credential`/`run` against a real openssh-server container, with
real fail-closed host key verification, and checks every claim by asking the container over a
second connection it opens itself. It was negative-controlled: disabling `creates` makes it fail
on both the reported status and the file's mtime read off the device.

**Every test written this pass was mutation-tested.** Seventeen mutations, three of which did not
fail a test. Two were weak mutations (a field added but never populated; a no-op statement) and
re-testing with sharper ones showed the tests were fine. The third looked like a coverage gap and
was actually a defect; see the section below, which is the more important half of this story.

### Four defects a green gate did not catch, and one my own test rationalized

After `make push-gate` passed and after a seventeen-mutation negative-control pass, an
adversarial review of the finished diff found four real defects, each reproduced by running
code. All four are fixed with regression tests proven to fail against them
(`FAILURE_PATTERNS` #146-149):

1. **The circuit breaker latched half-open forever.** `Allow` is a transaction, not a query: past
   the cooldown it hands out the single probe and mutates state to say so. `Connect` called it and
   then `dialWithRetry` called it again, so the first took the probe, the second refused, nothing
   dialed, and nothing ever recorded an outcome to leave half-open. A device that was briefly down
   was unreachable for the life of the process. Split into `Permitted` (looks) and `Allow`
   (claims). **This one predates this work in `internal/transport/ssh`**; the refactor carried it
   into a `pkg/` primitive with three callers, which is what made it worth finding. Worse, the
   mutation pass had already seen the two guards were indistinguishable and I wrote
   `TestConnect_OpenCircuitFailsBeforeAnyOtherWork` to justify the pair rather than asking why
   there were two. `LESSONS_LEARNED` #135 now carries that correction.
2. **`creates`/`removes` resolved relative paths in the wrong directory.** The command ran under
   `chdir` and the guard did not, so a relative `creates` never fired and a relative `removes`
   skipped a task whose file was still sitting in `chdir`, reporting success. An unenterable
   directory now has its own exit status so it is an error, not an absence.
3. **`chdir: "-P"` was consumed as a `cd` option** and `cd` succeeded into the home directory.
   Quoting stops word splitting, not option parsing. Now `cd -- '<dir>'`, tested both ways.
4. **A large stdin a command never read** turned a successful command into `EOF` with rc, stdout
   and stderr discarded. `x/crypto/ssh`'s `Wait` returns the stdin copy's error when the exit
   status was clean, so the copy is ours now. The regression test written beside the code passed
   against the broken version; it had to move up to the real-shell harness in
   `internal/catalog/exec` before it could fail. `LESSONS_LEARNED` #136.

### A finding that was not this phase's work

`TestCatalogDataDocsMatchTheRegistry` (new, in `internal/archtest`) compares every catalogdata
entry's `Doc` against the registered manifest's. It found pre-existing drift on its first run:
`net.catalyst.site_facts` and `net.catalyst.tag_facts` each carried an Example the catalog data
did not. A from-scratch regeneration would have dropped them, and `tools/gendocs`'s own
completeness gate requires an Example on an implemented method, so the regenerated tree would have
failed its own gate for a reason nothing in the diff explained. Both synced.
`FAILURE_PATTERNS` #145, `LESSONS_LEARNED` #134.

### Known limitation, deliberately not fixed here

`internal/engine`'s `collectionActionExecutor` discards a method's stats when it returns an error,
so a failed `exec.command`'s `rc`, `stdout` and `stderr` never reach the run result even though
the module records them before returning. That is pre-existing engine behavior affecting every
module equally, and the error message carries the exit status and the relevant stream so an
operator is not blind. Fixing it means deciding whether `ActionResult` survives an error at the
executor level, which is an engine change with its own blast radius.

### Next

`exec.shell` is the cheapest next module: same package, same helpers, and the only real difference
is that it deliberately does send the command to a shell, so its Doc has to be honest about the
tradeoff and its capability is `ShellExecCapable`. After that `file.copy` and `file.directory`,
which will need `remoteexec.Conn.RunWithStdin` (already built and tested for exactly this) and a
checksum comparison for idempotence. `svc.systemd.*` and `pkg.apt.*` need a different container:
the openssh-server image is Alpine with no systemd and no apt, so their release gates need a
Debian-based sshd image, and `internal/testsupport` is where that pin belongs.

When the second module needs the real-shell-over-real-SSH harness in
`internal/catalog/exec/sshd_test.go`, move it to `internal/testsupport` rather than copying it.
It will need a `gosec-waivers.json` entry for its `G204` at that point, since testsupport is
non-test code that gosec scans; today it lives in a `_test.go` file and is not scanned.

### Commit message, provided per the standing instruction (not committed)

See the end of this session's report; it is not duplicated here to keep one copy authoritative.

### The break-glass

`make break-glass` (`tools/breakglass`, `//go:build devtools`) returns the machine to the state
every test assumes it starts from: no throwaway kind cluster, no compose project holding a database
from a previous run, no containers left by a test binary killed before its cleanup ran.

Reach for it the moment a gate fails in a way that does not match the code you changed. Leftover
infrastructure never announces itself; it surfaces as a test failing at whichever assertion touched
the stale state (`LESSONS_LEARNED` #129).

- `make break-glass BREAK_GLASS_FLAGS=-n` says what would go and removes nothing.
- `BREAK_GLASS_FLAGS=-images` also drops the built images, so the next run builds from nothing.
- `BREAK_GLASS_FLAGS=-force` cleans through the live-run guard, breaking that run.

It refuses while a run is live, and it is not `docker system prune`: every removal is positively
attributed to this repository first and everything else is listed and left.

### Resuming after a context compaction

Everything needed is on disk; nothing is held only in conversation.

1. Read `IMPLEMENTATION.md`'s Phase 38 section. Its 2026-08-16 session note carries the scope
   re-derivation, all three map corrections, and the primitive decision with its rejected
   alternatives. The checklist items under it record what each gate was held to.
2. `pkg/remoteexec`'s package doc explains why it exists and what is deliberately never retried.
   Read it before writing the next module; it is the shortest path into this design.
3. `internal/catalog/exec/command.go`'s `Command` doc comment is where the `Changed` contract is
   written down. Every later module in this tier copies it.
4. Verify before trusting any claim in this document. The recorded trap about 71 stale signatures
   was wrong, and it was only settled by making the compiler answer.

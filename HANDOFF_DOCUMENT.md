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

**`IMPLEMENTATION.md`'s Phase 38 section now carries a full, measured plan for the remaining
twelve methods.** Read that rather than re-deriving it; what follows is the short version.

**Fix three things before writing another module**, because each is paid twelve more times
otherwise:

1. **Host key policy, and the shipped container that cannot satisfy it.** `FAILURE_PATTERNS` #150:
   `Dockerfile.runner` sets no `HOME` and ships no known_hosts, so `os.UserHomeDir` fails and
   every SSH Collection method refuses unless the task sets `insecure_skip_host_key_verify`. The
   escape hatch is currently the only working Crawl-tier path. The gates cannot see it because
   they set `HOME` and write a known_hosts themselves. Fixing the image is necessary but the real
   question is where a stateless runner's known_hosts comes from.
2. **`Manifest.RequiredCapabilities` is enforced by nothing at run time** (`FAILURE_PATTERNS`
   #151). I found this because a comment I had written claimed the opposite; the comment is
   corrected in `internal/catalog/exec/exec.go` and the gap is not.
3. **`wireDevice` cannot express a per-device capability set.** Adding accessors makes every
   dispatched device satisfy the interface, so the type assertion stops gating. `file.copy`,
   `svc.*` and `pkg.*` all want accessors no device type implements yet.

**Then, cheapest first:** `exec.shell` (nearly free: same package, same helpers, and the only
things it must not reuse are `SplitWords` and `QuoteCommand`), then `file.copy` and
`file.directory` (`RunWithStdin` is already built for the write; the open decision is whether
`src` can work at all under the Crawl tier, where the runner cannot see the runbook's files), then
`svc.systemd.*` and `pkg.apt.*`.

**The container question is settled, and the answer is better than feared.** Measured this
session: the current sshd image is Alpine with no `apt-get`, `dpkg` or `systemctl`, so neither
namespace can be gated against it. But a Debian image carrying `systemd`, `systemd-sysv` and
`openssh-server`, run `--privileged --cgroupns=host` with `/sys/fs/cgroup` mounted read-write,
reaches `systemctl is-system-running` = `running` here, and stop/start/is-active on a real unit
all behave. **`svc.*` does not need a VM.** `pkg.apt.*` needs the image to retain its package
index or pre-seed a `.deb` at build time, since installing at test time otherwise wants the
network.

When the second module needs the real-shell-over-real-SSH harness in
`internal/catalog/exec/sshd_test.go`, move it rather than copying it, and prefer `pkg/` (beside
the existing `pkg/inventory/inventorytest` precedent it already uses) over
`internal/testsupport`: a third-party Collection's tests cannot import `internal/` either, so
`pkg/` is where the constraint the catalog lives under actually points. Measured while planning:
`gosec` is invoked without `-tests`, so the move makes its `exec.Command("/bin/sh", ...)` newly
scannable, and the `#nosec` annotations already on those lines travel with the code and make it a
no-op. A `gosec-waivers.json` entry is the alternative and the worse one, since a line-numbered
waiver on a file that keeps growing goes stale and `gosec-check` fails on stale waivers.

Several catalog packages still carry a 100.0 coverage floor set while they were stubs, so each
implementation lands at 100 percent or moves its floor with a written justification.

### Gate status

`make push-gate` passes. 161 packages measured by the coverage ratchet, none below their recorded
floor, including the new `pkg/remoteexec` at 98.3. Four packages failed under full parallel `-race`
load and were downgraded as known-flaky: `cmd/runner`, `tests/e2e`, `internal/election` and
`internal/runner`. **All four were confirmed passing in isolation rather than assumed**, which
matters most for `cmd/runner`, since its `TestSSHMeshReleaseGate_*` pair drives `net.ssh.ping`
through the whole Crawl-tier chain and is therefore also evidence the `pkg/remoteexec` refactor
holds on that path. `tests/e2e` failed a different test on the isolation run with the documented
`port "4222/tcp" not found` signature, and that one passed alone too.

One thing about the gate worth knowing before running it: `docs-gen-check` diffs the working tree
against the git INDEX, so regenerated-but-unstaged documentation fails it every time. Stage the
tree (`git add -A`) before running `make push-gate` on uncommitted work. That is not a defect, it
is what the check is for, but it reads as a failure in your own generated output.

### Commit message, provided per the standing instruction (not committed)

```
feat(catalog): a shared SSH primitive, and the first module that changes something (Phase 38)

Phase 38 was taken ahead of the scheduler and the IDE plugin, and the
reasoning is written into the roadmap rather than left implicit: a
scheduler multiplies whatever the platform does, and the platform did
very little. Every engine under the catalog was real and proven while
the catalog on top of it could not copy a file, install a package or
start a service.

It opened by correcting its own map three times, before any module was
written. The recorded trap about seventy one stubs carrying an old
method signature is stale; all seventy six already carry the current
one, settled by assigning every exported catalog method to a
[]collection.Method literal and building, because grep cannot see a
signature. The Walk tier handed every Collection method an empty secret
set, so pleiades run could not run net.ssh.ping or any net.catalyst.*
method at all, failing with an authentication error against a device
whose credential was in .pleiades/credentials.yaml the whole time. And
no device type implemented CommandExecCapable, so exec.command was
unreachable by admission before it was unimplemented in body.

pkg/remoteexec is the load-bearing part. A Collection may import only
pkg/, which is enforced and is the same constraint a third-party
Collection will have to satisfy, so no module can reach
internal/transport/ssh no matter how much of the same work it needs.
The one SSH module hand-rolled its own dial, its own authentication and
its own host key check as a result, and said in its own doc comment
that this would need revisiting if the package grew a second,
write-capable method. This tier is twenty of them.

The mechanism moved rather than being copied. internal/transport/ssh
keeps its transport.Transport identity, its credential.Credential
translation and its Options surface, and is now about sixty lines of
adapter. Its container tests against a real, independent sshd pass
unchanged, which is what proves the move preserved behavior, and
net.ssh.ping's existing tests pass unchanged, which is what proves the
primitive is usable from a Collection. Two implementations of host key
verification is one implementation and one liability.

exec.command establishes what Changed means for the rest of the tier. A
command cannot be inspected, so it reports changed whenever it ran and
false only when it did not, which makes creates and removes
load-bearing rather than convenient: they are the only way a task built
on it becomes idempotent. Parameter names are Ansible's throughout, per
the superset rule. A non-zero exit status fails the task.

Its Release Gate drives the real built binary through init, add-host,
add-credential and run against a real openssh-server container, with
real fail-closed host key verification, and checks every claim by
asking the container over a second connection it opens itself: the
marker file's contents, the absence of the file a metacharacter
argument would have created had a shell interpreted it, and the
marker's mtime unchanged across a second run, which is what separates a
real creates short-circuit from a rewrite with identical content.

An adversarial review of the finished diff, run after the gate passed
and after a seventeen-mutation negative-control pass, found four real
defects and all four are fixed here. A circuit breaker latched
half-open forever, so a device that was briefly down was never dialed
again: Allow is a transaction that consumes the single probe, and two
calls sat on one dial path. The idempotence guard resolved relative
paths in a different directory from the command it guarded, which made
creates a silent no-op and made removes skip work it had never done. A
chdir value beginning with a dash was consumed as a cd option, so the
command ran in the home directory and reported success. And a large
standard input a remote command never read turned a successful command
into an opaque failure with its exit status, stdout and stderr thrown
away.

Two of those are worth naming for what they say about the process. The
breaker defect predated this work and was carried into a pkg/ primitive
with three callers, and the mutation pass had already noticed the two
guards were indistinguishable and produced a test rationalizing the
pair instead of asking why there were two. The stdin regression test
written beside its own code passed against the broken version and had
to move a package up, to a real shell, before it could fail.

A new archtest comparing the catalog data against the registered
manifests found drift that predates this work: two net.catalyst.*
methods each carried an Example the data did not. A from-scratch
regeneration would have dropped them, and the documentation generator's
own completeness gate requires an Example on an implemented method, so
the regenerated tree would have failed its own gate for a reason
nothing in the diff explained.

FAILURE_PATTERNS 143-149. LESSONS_LEARNED 132-138.
```

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

1. Read `IMPLEMENTATION.md`'s Phase 38 section. It carries TWO 2026-08-16 session notes. The
   first has the scope re-derivation, all three map corrections and the primitive decision with
   its rejected alternatives; the checklist items under it record what each gate was held to. The
   second, at the end of the section, is the measured plan for the remaining twelve methods and
   is what to read before writing any of them.
2. `pkg/remoteexec`'s package doc explains why it exists and what is deliberately never retried.
   Read it before writing the next module; it is the shortest path into this design.
3. `internal/catalog/exec/command.go`'s `Command` doc comment is where the `Changed` contract is
   written down. Every later module in this tier copies it. `internal/catalog/exec/exec.go`'s
   `workingDirectory` comment is worth reading too, for the opposite reason: it records a claim
   that was false and what is true instead.
4. `FAILURE_PATTERNS.md` #143-151 are this session's, and #146-151 are the ones a future reader
   is most likely to need. #146-149 are defects found and fixed after the gate was already green.
   #150 and #151 are found, recorded and deliberately NOT fixed, and both are named in the plan
   note as work that should come before another module.
5. Verify before trusting any claim in this document. Three things this session that looked
   settled were not: the recorded trap about 71 stale signatures was wrong and only the compiler
   settled it; a green `make push-gate` plus a seventeen-mutation pass still left four real
   defects; and a comment I wrote asserting that admission checks a method's declared capability
   was false, which is how #151 was found.

### What is deliberately not on disk

Nothing. The commit message is above rather than in the session transcript, the flaky-package
isolation results are in the gate section, and the container measurements behind the plan note
(the sshd image is Alpine with no `apt-get`, `dpkg` or `systemctl`; a Debian image with `systemd`
plus `systemd-sysv` reaches `is-system-running` = `running` under `--privileged --cgroupns=host`)
are written into the plan rather than left as something to re-measure.

# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**Branch `feature/Catalog-First-Tier`, off `main`. Standing goal: every declared-but-unimplemented
Collection method made real, each recording what would undo it. The catalog reads 22 of 76, up from
7. Nothing this session is committed; the commit message is at the bottom.**

Two commits from earlier sessions are in: `591441e` (`pkg/remoteexec` and `exec.command`) and
`aa383e9` (its follow-up docs).

### Read this first: the reversibility contract changed, and the old shape is wrong

`Manifest.Inverse` used to name the method that undoes each Collection method plus the prior-state
keys a rollback would feed it. **It could not be right**, and the reason generalizes:

**A method's inverse is a property of the RUN, not of the method.** `svc.start` against a service
that was already running must undo to nothing. `file.directory` that found a directory and only
fixed its mode must undo to the old mode, and the static declaration named `file.remove`, so a
rollback acting on it would have deleted a directory the run never created, with everything in it.
`http.request` is read-only or destructive depending on a parameter.

The contract now:

- **`Manifest.Reversibility{Reversible bool, Notes string}`** answers WHETHER, once, at
  registration. `Notes` is required when `Reversible` is false, and registration refuses without it,
  because "this cannot be undone" is the answer an operator most needs a reason for.
- **`sdk.RecordInverse`** emits WHAT, per run: an `inverse` stat holding an FQCN, already-resolved
  params and a one-line description. It is a TASK, so undoing a run is running more tasks through
  the same dispatcher, with the same capability checks and audit trail. A rollback engine needs no
  second execution path and no per-method knowledge.
- **A converged run emits nothing**, and that absence is meaningful: it is how the journal says
  undoing this means doing nothing. The static form could not express that at all.

`FAILURE_PATTERNS.md` #156 and `LESSONS_LEARNED.md` #141 carry the full reasoning. Nothing performs
a rollback yet; the recording exists because only the forward run can capture what an undo needs.

### What is implemented (22)

`exec.command`, `exec.shell`, `net.ssh.ping`, four `net.catalyst.*`, ten `file.*`
(`copy`, `directory`, `touch`, `permissions`, `remove`, `symlink`, `line.set`, `line.remove`,
`block.set`, `block.remove`), `wait.path`, `wait.search`, `pleiades.builtin.wait.port`,
`facts.gather`, `http.request`.

**Honest split on evidence.** Seven are gated against a real device over a real network hop
(`exec.*`, `net.ssh.ping`, the four `net.catalyst.*`). The other fifteen pass against a real
in-process SSH server running a real `/bin/sh`, at 99.8 to 100 percent coverage, each
mutation-tested, but have **no Release Gate against a container**. By this repository's own rule
that is ahead of their evidence, and closing it is cheap: the harness exists in
`cmd/pleiades/exec_shell_release_gate_test.go`.

### Three findings still OPEN, verified open at the end of this session

From an adversarial review of the first `file.*` batch. Fix these before adding more methods, since
two of them are the same class of problem twice:

1. **`file.touch` validates nothing.** It reads mode, owner and group with `sdk.StringParam`, so an
   unquoted `mode: 0600` (the integer 384 after YAML) is silently dropped and the task still reports
   success. `file.directory` and `file.permissions` both refuse that and a symbolic mode by name.
2. **`file.permissions` builds `diff.after` from the REQUEST** (`permApplied(before, want)`) rather
   than re-reading the device. Every sibling re-reads. This is precisely what made the setuid defect
   below invisible in the run report.
3. **The setuid ordering fix is pinned by no assertion.** `FAILURE_PATTERNS.md` #155: ownership must
   be applied before mode, because Linux clears setuid and setgid on a regular file whenever its
   owner or group changes. Verified at a real shell and by convergence; a comment records the
   reasoning and nothing fails if someone reverses the order again. A test needs a secondary group
   (`os.Getgroups`) to make a chgrp succeed unprivileged.

### Next, in order, with the reasoning

**1. Two small things that unblock more than their size.**

- **`file.template`**, the one method group one could not finish. The render engine is
  `internal/render` and a Collection may import only `pkg/`. This is a decision about what the
  template surface IS (move the engine, or expose a `pkg/` subset), not a module-sized task. It also
  closes the `file.*` namespace.
- **The three open findings above.**

**2. The capability decision, and it belongs BEFORE the next group rather than after.**

Eight of the remaining methods are DISPATCHERS: `svc.start`/`stop`/`restart`/`enable`/`disable` and
`pkg.install`/`remove`/`upgrade` resolve to a platform-specific implementation based on what the
device can do. They cannot be honestly written until two things are settled:

- **`Manifest.RequiredCapabilities` is enforced by nothing at run time** (`FAILURE_PATTERNS.md`
  #151). Note the concrete consequence found this session: all seven remaining `file.*` methods
  require `POSIXFileSystemCapable` and `linux.Server` declares only `Linux`, `SSHTransport` and
  `ShellExec`. They work solely because nothing checks.
- **`wireDevice` cannot express a per-device capability set.** Go interface satisfaction is static,
  so the moment it grows `RootPath()`, every dispatched device satisfies `POSIXFileSystemCapable`,
  Cisco switch included, and the type assertion stops gating anything. The alternative worth costing
  is rehydrating the real device type on the Runner from `record.LookupType`, which deletes
  `wireDevice` and its whole class of divergence.

Writing the 8 dispatchers before this is decided means writing them twice.

**3. Group two: one systemd container image, 11 methods.** `svc.systemd.*` (6) plus the `svc.*`
dispatchers (5). The container question is already MEASURED, not guessed: the current sshd image is
Alpine with no `apt-get`, `dpkg` or `systemctl`, and a Debian image with `systemd` plus
`systemd-sysv`, run `--privileged --cgroupns=host` with `/sys/fs/cgroup` mounted read-write, reaches
`systemctl is-system-running` = `running` on this machine, with stop/start/is-active all behaving.
**No VM needed.** `internal/testsupport/ansible_image.go` is the precedent for building an image
from a Dockerfile rather than pulling one. Add the package to `flaky-packages.json` with a written
reason, as every container-backed package here has needed.

Their inverses, worked out: `start`/`stop` and `enable`/`disable` are each other's, and each must
emit NOTHING when the state it found already matched, which is the case the old contract could not
express. `restart` is reversible false with a reason (it converges to the state it started in,
though the process identity changed). `daemon_reload` likewise.

**4. Then, in descending return on work:** `identity.*` (6, needs only root in an ordinary Linux
container), `pkg.apt.*` plus `pkg.*` (6, needs an image retaining its package index or a pre-seeded
`.deb`; installing at test time otherwise wants the network), `pkg.dnf.*` (3, a second image),
then the specialized group (`fs.mount`/`unmount`, `fw.firewalld.*`, `archive.*`,
`container.docker.*`), then the genuinely blocked 17 (`net.*.config` needs real hardware,
`win.*`/`svc.windows.*` need a Windows target and a WinRM transport that does not exist,
`cloud.aws.*` needs an SDK dependency and credentials).

### Using workflows for this, and the two things that went wrong

Both fan-outs worked and both hit the same avoidable problems. Read this before launching another.

- **Worktrees are cut from COMMITTED state, and this work is uncommitted.** Every agent's worktree
  was missing `pkg/remotefile`, `pkg/sdk`'s additions and the `Reversibility` type. Give agents an
  explicit step zero: check for a specific file, and sync from the main checkout if it is absent.
  The prompts in the persisted workflow scripts already do this and are worth reusing.
- **Leftover worktrees break a repo-wide uniqueness test** (`FAILURE_PATTERNS.md` #157):
  `internal/redact`'s `TestRulesetHasExactlyOneCopy` counted 19 copies of one file across 18
  checkouts. Clean up with `git worktree remove --force` then `git worktree prune`. **Reconcile
  before deleting**, which is the mistake made here: diff every produced file against the
  integrated copy and check each branch for commits ahead. Branches survive the removal, so
  committed work stays reachable.
- **What worked**: batching related methods into one agent so shared helpers are written once, and
  making agents copy finished files to a directory OUTSIDE the repository and return only a summary,
  which keeps file contents out of the orchestrator's context entirely.
- **The adversarial review earned its cost.** Four lens-based reviewers over five freshly written
  modules found the setuid defect, which a green suite and a clean mutation pass had both missed.
  Run one after any fan-out, read-only, and require a runnable reproduction per finding.

### Gate status

`go build`, `go vet`, `gofmt`, full `go test ./...`, `make docs-lint` (170 files),
`make docs-gen-check` all pass. Zero em-dashes in added lines. New packages at 100 percent;
`internal/catalog/file` 99.8 against 99.5.

**A full `make push-gate` has NOT been run since the redesign.** Run it before the commit lands, and
`git add -A` first: `docs-gen-check` diffs against the git INDEX, so regenerated-but-unstaged
documentation fails it every time.

### The break-glass

`make break-glass` returns the machine to the state every test assumes it starts from. Reach for it
the moment a gate fails in a way that does not match the code you changed.

- `BREAK_GLASS_FLAGS=-n` says what would go and removes nothing.
- `BREAK_GLASS_FLAGS=-images` also drops the built images.
- `BREAK_GLASS_FLAGS=-force` cleans through the live-run guard.

If it refuses and names processes that are not a real test run, read `FAILURE_PATTERNS.md` #153
first: stale self-matching `pgrep` wait loops from earlier sessions never exit and look like a live
run. Killing them is the actual fix. Note the same trap when writing one: a `pkill` pattern that
appears in its own command line kills its own shell.

### Resuming after a context compaction

1. `IMPLEMENTATION.md`'s Phase 38 section carries FIVE session notes plus the reasoning behind the
   reversibility redesign. Read the last one first.
2. `pkg/collection/manifest.go`'s `Reversibility` doc comment and `pkg/sdk/inverse.go` are the
   contract. Read both before writing any method.
3. `internal/catalog/file/permissions.go` is the worked example for a converging method;
   `internal/catalog/file/directory.go` shows an inverse that BRANCHES on what the run found, which
   is the pattern `svc.*` will need.
4. `FAILURE_PATTERNS.md` #143-157. #151 blocks the 8 dispatchers; #155 is fixed but unpinned;
   #156 is the redesign.
5. Verify before trusting anything here. Across these sessions, six things that looked settled were
   not, and five of the six were found by testing a claim rather than reading it.

### Commit message, provided per the standing instruction (not committed)

```
feat(catalog): emit the inverse instead of declaring it, and ten more methods (Phase 38)

The catalog reads 22 of 76, and the more important change is how a
method says it can be undone.

The manifest used to name the method that undoes each collection method,
plus the prior state keys a rollback would feed it. That could not be
right, and the reason generalizes past this field: a method's inverse is
a property of the RUN, not of the method. Starting a service that was
already running must undo to nothing rather than to a stop. Creating a
directory undoes to a removal, but fixing an existing directory's mode
undoes to the old mode, and the declaration named the removal, so a
rollback acting on it would have deleted a directory the run never
created along with everything in it. An HTTP request is read only or
destructive depending on one of its parameters, so a single declaration
covering every invocation can only describe the worst case.

So whether and what are now separate. The manifest answers whether, once,
at registration, with a reason required when the answer is no. The run
answers what, every time, by emitting a concrete already parameterized
instruction: a method to call, the arguments to call it with, and a
sentence saying what running it would do. That instruction is a task, so
undoing a run is running more tasks through the same dispatcher with the
same capability checks and the same audit trail, and nothing needs a
second execution path or per method knowledge to interpret it.

A converged run emits nothing at all, and that absence carries meaning
the old shape could not express: it is how the record says undoing this
means doing nothing.

Ten methods landed on that contract. file.copy, the four file.line and
file.block editors, the two waits, the port wait, the fact gatherer and
the HTTP request. The five file methods written earlier were migrated to
emit real inverses, and file.directory is the one worth reading: it
branches on what it found, emitting a removal only for a directory it
created and the old attributes for one it merely adjusted.

file.template is deliberately still declared rather than half built. Its
renderer lives under internal/ and a collection may import only pkg/, so
implementing it is a decision about what the template surface is rather
than a module sized task, and its page says so.

http.request declares itself not reversible, which is where this started:
a GET changes nothing and a DELETE may change something on a system this
platform cannot see, and one static answer covering both can only be the
worst one. Saying so is more useful than a declaration that would be
wrong half the time.

Three findings from an adversarial review of the earlier file batch are
recorded as still open rather than quietly carried: the touch method
validates none of its attribute parameters, so an unquoted octal mode is
silently dropped; the permissions method builds the after half of its
diff from the request rather than from the device, which is what made the
setuid ordering defect invisible in the run report; and that ordering fix
is verified at a shell and by convergence but pinned by no assertion.

FAILURE_PATTERNS 156-157. LESSONS_LEARNED 141-142.
```

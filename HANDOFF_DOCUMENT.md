# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**Branch `feature/Catalog-First-Tier`, off `main`. HEAD is `7d3638a`, the six `identity.*` methods
(committed with the user's own live go-ahead, after they ran it themselves). Everything below — the
ten `fs.*`/`archive.*`/`fw.firewalld.*`/`container.docker.*` methods — is implemented, tested, and
verified on top of that commit, but uncommitted: the standing rule holds (no commit without the
user's own live word in the current conversation), and no such word has been given yet this
session.**

This session opened with a request to "plan the next batch" before implementing. A plan was written
to `/root/.claude/plans/polished-purring-puppy.md`, approved by the user, and then implemented in
full in the same session, following `HANDOFF_ARCHIVE.md`'s prior-session note that this batch was
next by the "no new primitive" test `pkg.*` and `identity.*` both matched.

### What landed

**All ten methods across four new namespaces, implemented and tested**, each built entirely on
`pkg/remoteexec` (and, where a package edits a text file, `pkg/remotefile`) via `sdk.Connect`, no
new `pkg/` primitive — the same tier `pkg.*`/`identity.*` shipped at.

- **`fs.mount`/`fs.unmount`** (`internal/catalog/fs`): mount state read via `findmnt`, changed via
  `mount`/`umount`; fstab persistence reuses `pkg/remotefile`'s existing `Read`/`Write`/`Apply`, the
  same "read the whole file, decide in Go, write the whole file back" discipline
  `internal/catalog/file/line` already established, rather than a `sed -i` of a live fstab.
  Mounting and persisting are independent: a task can persist an already-hand-mounted path with no
  `mount` command sent, and mount a path without touching fstab at all. A path already mounted with
  a different `src`/`fstype`, or an explicitly-requested different `opts`, is refused rather than
  silently remounted. **Reversible partially**: an inverse is recorded when the mount itself
  changed (a real `fs.unmount`/`fs.mount` counterpart); a run that only touched the fstab entry on
  an already-live mount records no inverse, documented as a known gap in both manifests' own
  `Reversibility.Notes`.
- **`archive.create`/`archive.extract`** (`internal/catalog/archive`): tar/tar.gz only, no zip (not
  guaranteed present on a target the way tar is). `archive.create` is existence-only idempotent on
  its destination path. `archive.extract`'s forge stub declared `FileTransferCapable`, implying a
  control-node-to-device transfer this codebase cannot do (`file.copy` explicitly refuses `src` for
  the identical reason) — its capability was changed to `POSIXFileSystemCapable` and it is scoped to
  a `src` archive already on the device (Ansible's own `remote_src: true` shape), using `creates`
  (`exec.command`'s own idiom) for opt-in idempotency. `archive.create` is `Reversible: true`
  (inverse: `file.remove`); `archive.extract` is `Reversible: false` — enumerating everything an
  extract created well enough to safely delete it is out of scope, the same call `pkg.upgrade` made.
- **`fw.firewalld.allow`/`deny`/`reload`** (`internal/catalog/fw/firewalld`): mirrors `svc.systemd.*`'s
  shape against `firewall-cmd`. The permanent configuration and the runtime one are read and
  converged independently (both always read, regardless of which the task's `permanent`/`immediate`
  params ask to change), matching real firewalld semantics rather than folding them into one
  toggle. `allow`/`deny` are `Reversible: true`, exact inverses of each other; `reload` is
  `Reversible: false` and always reports changed, mirroring `svc.systemd.daemon_reload`'s identical
  reasoning (no way to ask whether a reload would have made a difference).
- **`container.docker.run`/`stop`/`remove`** (`internal/catalog/container/docker`): state read via
  `docker inspect`, scoped well below `community.docker.docker_container`'s full surface —
  `run` is idempotent on the container **name** existing only, never a config comparison, and never
  recreates. `run` is `Reversible: true` only when it actually created a fresh container (inverse:
  `container.docker.remove` with `force: true`). `stop` and `remove` are both `Reversible: false`:
  this catalog declares no `container.docker.start`, so recording `container.docker.run` as `stop`'s
  inverse would be dishonest (its own idempotency means it would just no-op rather than restart);
  and `remove`'s inverse would need to reconstruct ports/volumes/env/restart-policy from `docker
  inspect` output, which is parsing this pass does not take on — a partial inverse would be worse
  than an honest refusal, the same call `pkg.upgrade` made.

**Capability reachability split for the first time this batch.** `fs.*` and `archive.*` are
**already reachable against a real `linux.Server` device today**: `NameLinux` and
`NamePOSIXFileSystem` are both already in that type's baseline declared-capability set (unlike every
prior batch's gap). `fw.firewalld.*` (`NameFirewalld`) and `container.docker.*` (`NameDocker`) hit
the same documented-gap class `identity.*`/`pkg.*` did — no device type implements the accessor
(`FirewalldZone()`/`DockerSocketPath()`) at all — with a further wrinkle for `fw.firewalld.*`
specifically: unlike `LinuxCapable`, firewalld isn't universally true of every Linux box, so even
implementing the accessor would not earn a baseline declare on `linux.Server`; it would need a
per-instance property the way `service_manager` already works.

**A new `LESSONS_LEARNED` entry, #148**: `fs.mount`/`fs.unmount`'s fstab path is a task parameter
(`fstab`, defaulting to `/etc/fstab`) rather than a hardcoded constant, discovered as a real
necessity rather than a nicety — `remoteexectest.Start` runs every test command through a real
`/bin/sh` on the actual test-running machine, so a hardcoded `/etc/fstab` would have meant either
genuinely rewriting the test runner's own fstab or mocking `remotefile` out from under the method
(the exact RULE 0 violation this codebase's whole testing discipline exists to prevent). Ansible's
own `ansible.builtin.mount` already exposes the identical parameter for the identical reason,
confirming the design rather than inventing one.

**Two small dead-code removals caught by the 100% coverage requirement itself**, not by review:
`internal/catalog/fs/fs.go`'s `syncFstab` had a redundant `!info.Exists()` check duplicating what
`fstabReadLines` (called immediately after) already enforces; `internal/catalog/archive/archive.go`'s
`removePaths` had a `len(paths) == 0` guard neither real caller can ever trigger (both always pass
at least one path). Both were unreachable through the real call paths, and coverage refused to pass
until they were either exercised or removed; removed was correct in both cases.

**Doc entries hand-synced, same discipline as `pkg.*`/`identity.*`.**
`internal/forge/catalogdata/collections_extended.go`'s ten `Doc` entries are hand-expanded to
byte-match the real registered manifests (`TestCatalogDataDocsMatchTheRegistry` passing, and
`go generate ./internal/forge/catalogdata` reporting "wrote 0 new file(s)"); `archive.extract`'s
`Capabilities` entry was also corrected there to `NamePOSIXFileSystem` to match the registered
manifest's own capability change.

**One pre-existing test fixed, unrelated to a regression**: `cmd/pleiades/doc_test.go`'s
`TestRunDoc_EntryDeclared` and `TestRunDoc_SnippetDeclaredFallsBackToSkeleton` hardcoded
`archive.create` as an example of a still-declared-not-implemented method; both now use
`cloud.aws.ec2.create`, which remains genuinely declared (item 2 of the remainder list below).

**Coverage reached 100.0% on all four new packages**, against pre-existing floors already recorded
at 100.0 from their old stubs, via the same discipline as prior sessions: `sdk.Connect` failing with
no SSH accessor, a connection dying at each call site via `remoteexectest.Options.SessionLimit` (an
undocumented-but-load-bearing technique this session: for a multi-step shared primitive like
`pkg/remotefile`'s `Read`/`Write`/`Stat`/`Apply`, the exact session-budget number for each branch was
found by a disposable diagnostic test looping budgets 0..N and printing the resulting error, rather
than hand-counting through several layers of shared code), `SetStat`/diff/inverse failures via a
`ctxStub.failOnKey`, and, for `archive.*` specifically, real `tar`/`gzip` archives built and read
with Go's own `archive/tar`/`compress/gzip` stdlib rather than fake scripts, since creating and
extracting real archives under `t.TempDir()` is genuinely safe to do in a test (unlike mounting a
filesystem, running a real firewall command, or a real Docker daemon, all of which still use fake
shell scripts on `PATH`).

### Read this first

**Module names are `xxx.xxx.xxx`.** FAILURE_PATTERNS #158; still the rule, still not violated here.

**No commit without the user's own live word in the current conversation.** Unchanged. `7d3638a`
landed because the user ran it themselves after seeing the drafted message; the ten methods below
have not been asked for yet.

**Never use the Agent or Workflow tool to delegate without being asked, even with Ultracode on.**
Unchanged from last session (`pleiades_no_unrequested_delegation`). Not tested against this
session, since the batch was implemented directly throughout with no delegation temptation.

**A converge method's inverse comes from the value about to be overwritten, not a requery-diff.**
`LESSONS_LEARNED` #147, unchanged, applied again this session in `fs.unmount`'s own inverse
(captures `src`/`fstype`/`opts` from the pre-unmount query, never a post-unmount one).

**A real system path a method's own RULE-0 tests must touch belongs on a task parameter.**
`LESSONS_LEARNED` #148, new this session, described above.

### The remainder, in order

1. ~~`fs.*`/`archive.*` and `fw.*`/`container.*`~~ — done this session.
2. **`cloud.aws.*` (4)** needs an AWS SDK client, which is a real new dependency decision, not just
   more `remoteexec` commands.
3. **`svc.windows.*`/`win.feature.*` (7)** is transport-unblocked (WinRM exists) but needs two
   Windows capability accessors on `windows.Server` first.
4. **`net.cli`/`ios`/`eos`/`junos`/`netconf` (6)** is blocked on a NETCONF transport that does not
   exist yet.
5. **`file.template`** stays declared: the render engine is `internal/render`, unreachable from a
   Collection, and is a stable test fixture in `internal/validate` precisely because it is expected
   to stay declared for a while.
6. **Make `exec.shell` dispatch on capability**, the way `svc.start` resolves to `svc.systemd.start`.
   Unchanged from prior sessions: a design step, not a port, still not done.
7. **`file.directory` still has its own mode validator**, unreconciled with `attributes.go`. Also
   unchanged from prior sessions.
8. **Supplementary group membership and account passwords**, deliberately out of scope for
   `identity.user.*`. Unchanged from prior sessions.
9. **The four pre-existing private int-param parsers** could migrate to `sdk.IntParam`. Unchanged
   from prior sessions: deliberately not done, mechanical once started.
10. **Wire `FirewalldCapable`/`DockerCapable`** (and, from a prior session, `PosixAccountCapable`)
    onto a real device type. `FirewalldCapable` specifically needs a per-instance property (like
    `service_manager`) rather than a baseline declare, since firewalld isn't universal the way
    `LinuxCapable`/`SystemdCapable` are.

With items 1 done, the module catalog now has **59 of 77** methods at `collection.StatusImplemented`
in the working tree (49 committed at `7d3638a`, plus these ten), confirmed via `internal/archtest`'s
`TestEveryImplementedMethodAnswersReversibility`, which logs the count.

### Verification state

Full `go build ./...`, `go vet ./...`, `make fmt`, `go test -race ./...` (whole repo, not just the
new packages — this is what caught the two `cmd/pleiades/doc_test.go` tests needing an unrelated
fixture update), `make gosec` (9 pre-existing individually-waived findings, no new ones —
`gosec-waivers.json` itself is untouched), `go run ./tools/coverage-check` (169 packages measured,
none below their recorded floor), and `go run ./tools/docs-lint` all pass clean on top of `7d3638a`
plus this session's uncommitted work. `go generate ./internal/forge/catalogdata` and
`go run ./tools/gendocs` are both confirmed idempotent (a second run of each produces no further
diff), and `internal/archtest`'s full suite passes, including `TestCatalogDataDocsMatchTheRegistry`
and `TestCatalogPackagesImportOnlyPkg`.

`make docs-gen-check` "fails" for the same non-defect reason as every prior session: its own `git
diff --exit-code` compares the regenerated tree against `7d3638a`, and this session's work is real,
intentional, uncommitted content in `docs/reference` and `internal/api/wellknown`. Resolves on its
own the moment this is committed.

**`govulncheck` still fails, still not this session's doing.** The same five real, unrelated CVEs in
`github.com/lib/pq@v1.10.9` (GO-2026-6172/6171/6170/6168/6166) that blocked `make ci` every prior
session, confirmed again, none with a fix available upstream ("Fixed in: N/A" on every one).
`go.mod`/`go.sum` are untouched by this session.

Nothing about `fs.*`/`archive.*`/`fw.firewalld.*`/`container.docker.*` was exercised against a real
device either — same honest caveat every implemented-but-not-device-proven batch has carried, and
for `fs.*`/`archive.*` specifically the caveat is now purely "not yet run against a real device,"
not "not yet capability-reachable," which is a genuine step forward from every prior batch.

### Commit message

Drafted, not run; nothing is committed except `7d3638a`.

```
feat(catalog): fs.*, archive.*, fw.firewalld.* and container.docker.*, ten more methods

Ten of the remaining declared-but-unimplemented methods, across four
new namespaces, all matching the same "no new pkg/ primitive" test
pkg.* and identity.* both matched: fs.mount/unmount,
archive.create/extract, fw.firewalld.allow/deny/reload, and
container.docker.run/stop/remove. Every one talks to the target
through pkg/remoteexec (via sdk.Connect); fs.* additionally reuses
pkg/remotefile's existing Read/Write/Apply for fstab persistence, the
same "read the whole file, decide in Go, write it back" discipline
internal/catalog/file/line already established, rather than a live
sed -i.

fs.mount/unmount decide mounting and fstab persistence independently:
a task can persist an already-mounted path with no mount command
sent, or mount without touching fstab at all. A path already mounted
with a different src/fstype, or an explicitly different opts, is
refused rather than silently remounted. Reversible only partially: an
inverse is recorded when the mount itself changed, not when a run
only touched an already-live mount's fstab entry, documented as a
known gap in both manifests.

archive.create/extract are tar/tar.gz only, no zip, since zip/unzip
are not guaranteed present the way tar is. archive.extract's forge
stub declared FileTransferCapable, implying a control-node-to-device
transfer nothing in this codebase can do (file.copy already refuses a
src param for the identical reason); its capability changed to
POSIXFileSystemCapable and it is scoped to a src archive already on
the device. archive.create is Reversible: true (inverse: file.remove);
archive.extract is Reversible: false, the same call pkg.upgrade made,
since enumerating everything an extract created well enough to safely
delete it is out of scope this pass.

fw.firewalld.allow/deny read and converge the permanent configuration
and the runtime one independently, always reading both regardless of
which the task's permanent/immediate params ask to change, matching
real firewalld semantics. They are exact inverses of each other.
reload always reports changed and has no inverse, mirroring
svc.systemd.daemon_reload's identical reasoning.

container.docker.run is idempotent on the container NAME existing
only, never a config comparison, and never recreates -- the same
restraint identity.user.* took against full ansible.builtin.user
parity. Reversible only when it actually created a fresh container.
stop and remove are both Reversible: false: this catalog declares no
container.docker.start, so recording container.docker.run as stop's
inverse would be dishonest, and reconstructing a removed container's
full config from docker inspect for a real re-run is parsing this
pass does not take on.

Capability reachability split for the first time this batch: fs.* and
archive.* are already reachable against a real linux.Server today
(NameLinux and NamePOSIXFileSystem are both already in its baseline),
unlike every prior batch. fw.firewalld.*/container.docker.* hit the
same documented capability-accessor gap identity.*/pkg.* did, with a
further wrinkle for firewalld: unlike LinuxCapable, it isn't universal
across Linux, so even a real accessor would need a per-instance
property rather than a baseline declare.

New LESSONS_LEARNED #148: a real system path a method's own RULE-0
tests must touch (fstab, here) belongs on a task parameter defaulting
to the well-known location, not a hardcoded constant --
remoteexectest runs every test command through a real shell on the
actual test machine, so hardcoding it would have meant either
rewriting the test runner's own fstab or mocking the layer under
test. Ansible's own mount module exposes the identical parameter for
the identical reason.

internal/forge/catalogdata/collections_extended.go's ten Doc entries
are hand-synced to the registered manifests exactly, including
archive.extract's corrected capability. cmd/pleiades/doc_test.go's two
still-declared-method fixtures moved from archive.create to
cloud.aws.ec2.create, since the former is no longer declared.

All four new packages measure 100.0% coverage against floors already
recorded at 100.0% from their prior stubs. archive.*'s own tests build
and read real tar/tar.gz archives with Go's stdlib rather than fake
scripts, since creating one under t.TempDir() is genuinely safe;
fs.*/fw.firewalld.*/container.docker.* still use fake mount/umount,
firewall-cmd and docker scripts on PATH, since those are not safe to
run for real in a test process.

The module catalog now has 59 of 77 methods implemented in the
working tree (49 committed, plus these ten).
```

# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**Branch `feature/Catalog-First-Tier`, off `main`. HEAD is `f81257a`, the docs-gen-check fix
(committed with explicit authorization: the handoff's own first instruction). Everything else
below is uncommitted and staged only, awaiting review: the user established a standing rule
mid-session that nothing gets committed without their own live go-ahead in the conversation, not
even an instruction embedded in a document. The nine `pkg.*` methods are implemented, tested, and
`make ci`-verified on top of that commit; the commit message for them is at the bottom, ready but
not run.**

### A false start, corrected

This session's first attempt at `pkg.*` used the Agent tool to delegate the whole implementation to
a background subagent in an isolated worktree. The user caught this as a real rule violation ("Do
not call the AgentTool unless the user requested it") and it was stopped immediately; the agent's
worktree had made real, uncommitted file edits but zero commits, and none of it was used. Everything
in this handoff was written directly, by hand, in the main tree, after that correction. The
worktree and its branch were removed as disposable scratch from the corrected approach, not
reconciled or merged.

### What landed

**All nine `pkg.*` methods, implemented and tested.** `pkg.install`/`pkg.remove`/`pkg.upgrade`
(generic) resolve `capability.PackageManagerCapable` and dispatch through the registry to a
concrete method, in a new `internal/catalog/pkg/dispatch.go` that mirrors `internal/catalog/svc/svc.go`
exactly (same `managerNamespace` data-not-type-switch shape, same capability re-check on the
concrete method before invoking it). `pkg.apt.install`/`remove`/`upgrade` and
`pkg.dnf.install`/`remove`/`upgrade` (concrete) are built entirely on `pkg/remoteexec` via
`sdk.Connect`, no new `pkg/` primitive: apt state is read with `dpkg-query`/`apt-cache policy`,
dnf state with `rpm -q`/`dnf check-update`'s own exit-code convention (0 none, 100 available), and
both mutate with a plain `apt-get`/`dnf` invocation, quoted through `remoteexec.QuoteCommand`.
Converge is real: a task against an already-satisfied package sends nothing. `install`/`remove`
are `Reversible: true` with a genuine captured-state inverse (remove records the exact version it
found and pins the paired install to it); `upgrade` is `Reversible: false` on both children and
both generics, because downgrading a package is not something apt or dnf reliably support once a
newer build has superseded it in the repo.

**A capability-wiring gap, found and deliberately left alone.** `capability.AptCapable` and
`capability.DnfCapable` already existed (`pkg/capability/capabilities_package.go`), but no device
type in this repository structurally implements either: `internal/inventory/devices/linux/server_test.go`'s
`TestNewServer_UnionsClassificationCapabilities` is a deliberate regression proof that
`linux.Server` does not, even when a record's classification data explicitly declares
`AptCapable` ("neither side is trusted alone"). That means `pkg.apt.*`/`pkg.dnf.*` are implemented
and tested against a real in-process SSH server with a fake `apt-get`/`dnf` on `PATH`, at the same
tier `svc.systemd.*` already ships at (no container release gate; `find cmd/pleiades -iname
'*release_gate*'` confirms none exists for `svc.systemd.*` either), but are not yet reachable
against any real inventory device through the platform end to end. Wiring a device type to this
capability is real, separate, deliberate follow-up work, not a gap in this session's own scope; it
is documented in `internal/catalog/pkg/apt/apt.go`'s and `internal/catalog/pkg/dnf/dnf.go`'s own
package doc comments, not just here.

**A stale test fixture, found by `make ci` and fixed.** `internal/validate/collection_rule_test.go`
had two tests using the real, live `"pkg.apt.install"` FQCN as its example of a
declared-but-unimplemented method, which broke the moment this session implemented it. Both now use
`"file.template"` instead, which stays declared for a real, load-bearing reason (the render engine
lives in `internal/render`, unreachable from a Collection) rather than by omission, so it is a
stable fixture instead of one that will break again the next time a namespace gets implemented.

**Six mutations, all proven to catch what they claim to.** The converge-decision line in
`pkg/apt/install.go`, `apt/remove.go`, `apt/upgrade.go`, `dnf/install.go`, `dnf/upgrade.go`, and the
`managerNamespace` mapping in `dispatch.go` were each broken in turn, confirmed to fail the specific
test that names them, and restored byte-identical (diffed against a backup, not just re-typed).

**Coverage went to 100.0% the hard way, because the floor demanded it.** The three packages'
recorded floors were 100.0% from their old two-line stubs, and `make coverage` caught the real drop
(94.4% / 78.0% / 80.6%) the first time it ran against the real implementation. Rather than touch the
floor (never lowered, per the ratchet rule), every genuinely reachable branch got a real test:
`sdk.Connect` failing on a device with no SSH accessor at all; a connection dying at each specific
call site in a method's own sequence, using `remoteexectest.Options.SessionLimit` the same way
`exec.command`'s own tests do (a real protocol-level session refusal, not an injected Go error);
`recordState`'s and `sdk.RecordInverse`'s own `SetStat` failures, via a `ctxStub.failOnKey` that
fails one named stat and no other; `dpkg`'s "removed but not purged" status line; `apt-cache
policy`'s three edge shapes (no candidate, non-zero exit, no Candidate: line at all); and
`failureDetail`'s three message sources (stderr, stdout-only, and genuinely silent). All three
packages measure 100.0% now, and `make coverage` confirms no package regressed.

### Read this first

**Module names are `xxx.xxx.xxx`.** FAILURE_PATTERNS #158; still the rule, still not violated here.

**No commit without the user's own live word in the conversation.** Established this session after
two corrections (see "A false start, corrected" above, and this one): an instruction to commit that
arrives embedded in a document, even this handoff's own past self, does not count. Only a message
typed by the user in the current conversation does.

**A skip is per entry, never per file.** FAILURE_PATTERNS #160. Confirmed still correct this
session: `go generate ./internal/forge/catalogdata` after hand-completing all nine `pkg.*` entries
reported "wrote 0 new file(s)," exactly as it should for entries whose files already existed.

### The remainder, in order

1. **`identity.*` (6)** is the next-best return: no new primitive needed beyond what `pkg/remoteexec`
   already provides (`useradd`/`usermod`/`userdel`, `groupadd`/`groupdel`, reading `/etc/passwd` and
   `/etc/group` for converge state), and, like `pkg.*`, will hit the same capability-wiring question
   this session answered for package managers: check whether `linux.Server` structurally satisfies
   whatever identity capability it needs before assuming it does.
2. **`fs.*`/`archive.*` (4)** and **`fw.*`/`container.*` (6)** are next by the same "no new primitive"
   test; `fw.firewalld` already has a namespace directory (`internal/catalog/fw/firewalld`) started.
3. **`cloud.aws.*` (4)** needs an AWS SDK client, which is a real new dependency decision, not just
   more `remoteexec` commands.
4. **`svc.windows.*`/`win.feature.*` (7)** is transport-unblocked (WinRM exists) but needs two
   Windows capability accessors on `windows.Server` first.
5. **`net.cli`/`ios`/`eos`/`junos`/`netconf` (6)** is blocked on a NETCONF transport that does not
   exist yet.
6. **`file.template`** stays declared: the render engine is `internal/render`, unreachable from a
   Collection, and is now also a stable test fixture (see above) precisely because it is expected to
   stay declared for a while.
7. **Make `exec.shell` dispatch on capability**, the way `svc.start` resolves to `svc.systemd.start`.
   Unchanged from last session: a design step, not a port, still not done.
8. **`file.directory` still has its own mode validator**, unreconciled with `attributes.go`. Also
   unchanged from last session.

### Verification state

`make ci` on top of `f81257a` plus this session's uncommitted `pkg.*` work: build, vet, fmt,
`test-race` (including `internal/archtest`'s `TestCatalogDataDocsMatchTheRegistry` and
`TestCatalogPackagesImportOnlyPkg`) and gosec all pass clean, run twice for confirmation. `go
generate ./internal/forge/catalogdata` and `go run ./tools/gendocs` are both confirmed idempotent (a
second run produces no further diff). `make coverage` and `make docs-lint` pass when run directly
(the full `make ci` chain never reaches them, see below). `make docs-gen-check`'s own `git diff
--exit-code` reports a diff, correctly: it is comparing against `f81257a`, and this session's
`pkg.*` work is real, intentional, uncommitted content in `docs/reference` and
`internal/api/wellknown`. That resolves on its own the moment this is committed; it is not a defect.

**`govulncheck` fails, and it is not this session's doing.** Five real CVEs
(GO-2026-6172/6171/6170/6168/6166) in `github.com/lib/pq@v1.10.9`, reachable through
`internal/ent`'s Postgres driver, none of which has a fixed version available yet
("Fixed in: N/A" on every one). Confirmed twice, both times identical. `go.mod` and `go.sum` are
completely untouched by this session (`git diff --stat -- go.mod go.sum` is empty), and `lib/pq` has
nothing to do with `internal/catalog/pkg`; this is `govulncheck`'s live advisory database having
been updated sometime during this session (CLAUDE.md's own caveat: "The one thing a local run still
cannot predict is govulncheck's live advisory database"). The very first `make push-gate` run at the
start of this session, before any `pkg.*` work began, passed govulncheck clean. This blocks a real
`make ci` pass right now, through no fault of this branch, and is squarely `internal/ent`'s problem
to pick up, not this namespace's.

The module catalog now has 43 of 77 methods at `collection.StatusImplemented` in the working tree
(34 committed at `f81257a`, plus these nine), confirmed via
`internal/archtest`'s `TestEveryImplementedMethodAnswersReversibility`, which logs the count.

Two things are honestly unproven, same as last session, unchanged by this one: the WinRM gate's own
conversion path, and `exec.winrm.shell` exercised live only on a read-only task. Nothing about
`pkg.*` was exercised against a real device either, for the capability-wiring reason above, which is
new and honestly stated rather than inherited.

### Commit message

Drafted, not run; nothing is committed except `f81257a`.

```
feat(catalog): pkg.install/remove/upgrade, and the six concrete apt/dnf methods

Nine of the 43 remaining declared-but-unimplemented methods, all in the
pkg.* namespace. pkg.install/remove/upgrade resolve the device's package
manager and dispatch through the registry, the same generic-plus-concrete
shape svc.start already proved for service managers. pkg.apt.* and
pkg.dnf.* are built entirely on pkg/remoteexec, no new pkg/ primitive:
apt state comes from dpkg-query and apt-cache policy, dnf state from rpm
-q and dnf check-update's own exit code convention. Converge is real: an
already-satisfied package sends nothing. install and remove are
reversible with a genuine captured-state inverse; upgrade is not, on
every method in the namespace, because downgrading a package is not
something apt or dnf reliably support once a newer build has superseded
it in the repo.

No device type in this repository structurally implements AptCapable or
DnfCapable (internal/inventory/devices/linux/server_test.go proves
linux.Server deliberately does not, even when a record's classification
data declares it), so this namespace is implemented and tested against a
real in-process SSH server with a fake apt-get/dnf on PATH, the same tier
svc.systemd.* already ships at, but is not yet reachable against a real
inventory device end to end. That is separate, deliberate follow-up
work, not a defect in this change; both apt.go and dnf.go say so in
their own package doc comments.

internal/validate/collection_rule_test.go used the real pkg.apt.install
FQCN as its example of a declared-but-unimplemented method, which broke
the moment this method was implemented. Both tests now use file.template,
which stays declared for a real, load-bearing reason rather than by
omission, so this fixture will not break the next time a namespace is
implemented.

internal/forge/catalogdata/collections_packages.go's nine Doc entries
are hand-synced to match the registered manifests exactly:
TestCatalogDataDocsMatchTheRegistry requires byte equality, and
--skip-existing means the forge does not propagate a catalogdata edit
into an already-generated file for you.
```

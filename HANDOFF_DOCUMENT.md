# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**Branch `feature/Catalog-First-Tier`, off `main`. HEAD is `f481fbb`, the nine `pkg.*` methods
(committed with the user's own live go-ahead, after they ran it themselves). Everything below —
the six `identity.*` methods — is implemented, tested, and verified on top of that commit, but
uncommitted: the standing rule from last session holds (no commit without the user's own live word
in the current conversation), and no such word has been given yet this session.**

### What landed

**All six `identity.*` methods, implemented and tested.** `identity.user.create`/`modify`/`remove`
and `identity.group.create`/`modify`/`remove` are built entirely on `pkg/remoteexec` via
`sdk.Connect`, no new `pkg/` primitive: account/group state is read with `getent passwd`/
`getent group`, and changed with `useradd`/`usermod`/`userdel`/`groupadd`/`groupmod`/`groupdel`,
quoted through `remoteexec.QuoteCommand`. Unlike `pkg.*`, there is no generic-plus-concrete
dispatcher here — these six FQCNs were already the concrete layer with nothing generic above them
to resolve to, so no `dispatch.go` equivalent exists in this namespace.

`identity.user.create` converges an existing account's `uid`/`group`/`shell`/`home`/`comment`
toward whichever of those the runbook named, using `usermod`, rather than only ever creating; a
brand-new account is created with `useradd` from the same set of attributes.
`identity.user.modify` is the same converge logic but refuses outright if the account does not
exist, rather than creating one — it is for changing a known-existing account, `create` is for
"make sure it exists". `identity.user.remove` captures the full attribute set before deleting so
its inverse is a real `identity.user.create` pinned to the old values. `identity.group.*` mirrors
this shape with `gid` as the only mutable attribute, since a POSIX group's name is its identity
(no rename, in `groupmod` or in `ansible.builtin.group`) and membership is `identity.user.*`'s own
concern. Supplementary group membership (`ansible.builtin.user`'s `groups`/`append`) and account
passwords are deliberately out of scope this pass — both need their own separate design, and both
packages' doc comments say so plainly rather than silently doing a partial job.

Converge is real: a run against an account/group already exactly as requested sends nothing.
`create`/`modify`/`remove` are all `Reversible: true` in every one of the six manifests — unlike
`pkg.*`, where `upgrade` had no safe inverse, every operation here has a real one, because POSIX
account/group attributes (unlike a package's installed build) are simple values this platform can
always capture and restore exactly.

**A new shared `sdk.IntParam` helper.** `uid`/`gid` needed whole-number param parsing, and this
was the fourth place in the catalog needing exactly the int/int64/float64 handling
`internal/catalog/exec/winrm/shell.go`'s own `secondsParam` comment already flagged as worth
hoisting once a shared package needed it a third time (`wait.port`, `net.catalyst.device_facts`
and `http.request` each already had a private copy). Added to `pkg/sdk/params.go` with its own
test in `pkg/sdk/params_test.go`, used by `identity.user.*` and `identity.group.*` only — the four
existing private copies were deliberately left alone rather than refactored, since a hoist
touching four already-shipped, already-tested files is its own change, not a side effect of a new
namespace (this mirrors `secondsParam`'s own comment: "a change to a shared package with its own
tests rather than something to slip into a transport fix").

**A capability-wiring gap, the same one `pkg.*` found, for a related but distinct reason.**
`capability.PosixAccountCapable` already existed (`pkg/capability/capabilities_posix.go`,
`PasswdPath() string`), but no device type in this repository implements it, same as
`AptCapable`/`DnfCapable`. This time the reason isn't "no honest per-instance default" (POSIX
accounts genuinely are universal across Linux) — it's simpler: nothing has ever wired the
accessor at all. See `LESSONS_LEARNED` #146's update and `internal/catalog/identity/user/user.go`'s
own doc comment. Practical consequence, identical to `pkg.*`: implemented and tested against a
real in-process SSH server with fake `getent`/`useradd`/`groupadd` on `PATH`, not yet reachable
against a real inventory device end to end.

**A real design bug, caught by the test harness before it ever shipped.** The first draft of
`identity.user.create`/`modify` built each converge's inverse by re-querying the account after
`usermod` ran and diffing that "after" against the original "before". Every test against the fake
`getent` (a static script, same technique `pkg.apt.*`'s tests use) failed: the fake's requery
reports the same canned values regardless of what `usermod` was told to do, so the diff always came
back empty. The fix is a better design, not a test workaround: `converge` now returns the old value
of each attribute it decides to touch, captured from the same query that made the decision, and the
inverse is built directly from that — never from a post-mutation query, which real NSS-backed
sources (LDAP, SSSD) aren't guaranteed to reflect promptly anyway. Full writeup, and the general
rule, is `LESSONS_LEARNED` #147 (new this session).

**A real correctness bug, also caught before it shipped.** `useraddArgs` built every `useradd` flag
correctly but never appended the account's own name, its one required positional argument — a bug
that would have made every `identity.user.create` on an absent account fail outright the moment it
reached a real device. Caught by the first `useradd` invocation assertion in `user_test.go`, not by
any later review pass.

**Doc entries hand-synced, same discipline as `pkg.*`.**
`internal/forge/catalogdata/collections_identity.go`'s six `Doc` entries are hand-expanded to
byte-match the real registered manifests (`TestCatalogDataDocsMatchTheRegistry` passing, and
`go generate ./internal/forge/catalogdata` reporting "wrote 0 new file(s)").

**Coverage reached 100.0% on both new packages**, against a pre-existing floor of 100.0% left over
from the old two-line stubs (`coverage-floor.json` already had `internal/catalog/identity/user`
and `internal/catalog/identity/group` at 100.0, same situation `pkg/apt`/`pkg/dnf` were in). Every
genuinely reachable branch got a real test: `sdk.Connect` failing on a device with no SSH accessor;
a connection dying at each call site in a method's own sequence via
`remoteexectest.Options.SessionLimit`; `recordState`'s and `sdk.RecordInverse`'s own `SetStat`
failures via a `ctxStub.failOnKey`; malformed/non-numeric `getent` output on both the passwd and
group paths; and `failureDetail`'s three message sources (stderr, stdout-only, genuinely silent).

### Read this first

**Module names are `xxx.xxx.xxx`.** FAILURE_PATTERNS #158; still the rule, still not violated here.

**No commit without the user's own live word in the current conversation.** Unchanged from last
session. `f481fbb` landed because the user ran it themselves after seeing the drafted message; the
`identity.*` work below has not been asked for yet.

**Never use the Agent or Workflow tool to delegate without being asked, even with Ultracode on.**
New this session, saved as a durable memory (`pleiades_no_unrequested_delegation`): a generic
"Ultracode is on" system reminder is not the user asking for delegation. This work was done
directly, the same way `pkg.*` was after the earlier correction.

**A converge method's inverse comes from the value about to be overwritten, not a requery-diff.**
`LESSONS_LEARNED` #147, this session's own finding, described above.

### The remainder, in order

1. **`fs.*`/`archive.*` (4)** and **`fw.*`/`container.*` (6)** are next by the "no new primitive"
   test that `pkg.*` and `identity.*` both matched; `fw.firewalld` already has a namespace
   directory (`internal/catalog/fw/firewalld`) started. Each will need its own fresh capability-
   wiring judgment call, not an assumption carried over from either prior namespace.
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
   `identity.user.*` this pass (see "What landed" above). Both need their own design before they
   can be added as new params on the existing three methods, not new FQCNs.
9. **The four pre-existing private int-param parsers** (`wait.port`, `net.catalyst.device_facts`,
   `http.request`, `exec.winrm.shell`) could migrate to the new `sdk.IntParam`, now that it exists.
   Deliberately not done this session (a hoist touching four shipped files is its own change); safe
   to pick up whenever, mechanical once started.

### Verification state

Full `go build ./...`, `go vet ./...`, `make fmt`, `go test -race ./...` (whole repo, not just the
new packages), `make gosec`, `go run ./tools/coverage-check`, and `make docs-lint` all pass clean
on top of `f481fbb` plus this session's uncommitted `identity.*` work. `go generate
./internal/forge/catalogdata` and `go run ./tools/gendocs` are both confirmed idempotent (a second
run produces no further diff), and `internal/archtest`'s full suite passes, including
`TestCatalogDataDocsMatchTheRegistry` and `TestCatalogPackagesImportOnlyPkg`.

`make docs-gen-check` "fails" for the same non-defect reason it did last session: its own `git diff
--exit-code` compares the regenerated tree against `f481fbb`, and this session's `identity.*` work
is real, intentional, uncommitted content in `docs/reference` and `internal/api/wellknown`. Resolves
on its own the moment this is committed.

**`govulncheck` still fails, still not this session's doing.** The same five real, unrelated CVEs
in `github.com/lib/pq@v1.10.9` (GO-2026-6172/6171/6170/6168/6166) that blocked `make ci` last
session, confirmed again this session, none with a fix available upstream ("Fixed in: N/A" on
every one). `go.mod`/`go.sum` are untouched by this session.

The module catalog now has 49 of 77 methods at `collection.StatusImplemented` in the working tree
(43 committed at `f481fbb`, plus these six), confirmed via `internal/archtest`'s
`TestEveryImplementedMethodAnswersReversibility`, which logs the count.

Two things are honestly unproven, unchanged from prior sessions: the WinRM gate's own conversion
path, and `exec.winrm.shell` exercised live only on a read-only task. Nothing about `identity.*` or
`pkg.*` was exercised against a real device either, for the capability-wiring reason above.

### Commit message

Drafted, not run; nothing is committed except `f481fbb`.

```
feat(catalog): identity.user.* and identity.group.*, the six POSIX account methods

Six of the 34 remaining declared-but-unimplemented methods, all in the
identity.* namespace: identity.user.create/modify/remove and
identity.group.create/modify/remove. Built entirely on pkg/remoteexec
via sdk.Connect, no new pkg/ primitive: account/group state comes from
getent passwd/getent group, mutation goes through
useradd/usermod/userdel/groupadd/groupmod/groupdel. Unlike pkg.*, there
is no generic-plus-concrete dispatcher here; these six FQCNs are
already the concrete layer.

create converges an existing account/group's mutable attributes
(uid/group/shell/home/comment for a user, gid for a group) toward
whichever the runbook named, rather than only ever creating fresh.
modify does the same converge but refuses outright if the target does
not exist, for changing a known-existing account rather than deciding
whether one should exist. remove captures the full attribute set
before deleting so its inverse is a real create pinned to the old
values. All six methods are Reversible: true: unlike pkg.upgrade,
every operation here has a safe, real inverse, because POSIX
attributes are simple captured-and-restored values, not an installed
build a repository may no longer offer.

Supplementary group membership (ansible.builtin.user's groups/append)
and account passwords are deliberately out of scope this pass; both
need their own separate design and both packages' doc comments say so
plainly.

No device type in this repository implements capability.PosixAccountCapable
(pkg/capability/capabilities_posix.go's PasswdPath accessor), the same
gap pkg.apt.*/pkg.dnf.* have for a related but distinct reason: this
time it isn't that the concrete value lacks an honest per-instance
default, it's that nothing has wired the accessor at all. Implemented
and tested against a real in-process SSH server with fake
getent/useradd/groupadd on PATH, the same tier pkg.apt.* ships at, not
yet reachable against a real inventory device end to end.

Added sdk.IntParam (pkg/sdk/params.go) for uid/gid parsing: the fourth
place in the catalog needing int/int64/float64 handling across the
Walk-tier-YAML vs Runner-subprocess-JSON boundary, which
exec/winrm/shell.go's own secondsParam comment had already flagged as
worth hoisting on its third occurrence. The three prior private copies
were left alone deliberately; migrating them is separate follow-up.

The first draft built each converge's inverse by diffing a
post-mutation requery against the original state, which broke against
a static test fake and, more importantly, was never a safe assumption
against a real NSS-backed account source either. Fixed by having
converge return the old value of each attribute at the point it
decides to change it; the inverse is built from that, never from a
requery. Written up as LESSONS_LEARNED #147.

internal/forge/catalogdata/collections_identity.go's six Doc entries
are hand-synced to match the registered manifests exactly, the same
TestCatalogDataDocsMatchTheRegistry discipline pkg.* established.

Both new packages measure 100.0% coverage against a floor already
recorded at 100.0% from their prior two-line stubs: sdk.Connect
failing with no SSH accessor, a connection dying at each call site via
remoteexectest.Options.SessionLimit, SetStat failures via
ctxStub.failOnKey, malformed/non-numeric getent output, and
failureDetail's three message sources.
```

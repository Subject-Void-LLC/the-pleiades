# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**Branch `feature/Catalog-First-Tier`, off `main`. The catalog still reads 34 of 77: this session
added no methods and instead closed the defect list the previous one left behind, including one
real vulnerability. Nothing is committed; the commit message is at the bottom. Two changesets are
in the tree wanting to be two commits: the previously-staged reversibility/Group-One work with its
own message in `HANDOFF_ARCHIVE.md`, and everything since.**

### What landed

**A vulnerable dependency, found by the gate and fixed.** `github.com/Azure/go-ntlmssp` before
v0.1.1 can panic parsing a malformed NTLM challenge (GO-2026-5543), and this platform reaches that
code on every `exec.winrm.shell` task and on any `http.request` whose server answers with an NTLM
challenge. It arrived as an indirect dependency of the WinRM client added last session. Now pinned
to v0.1.1, and the fix is verified against the real Windows host rather than just against
`govulncheck`, because no unit test here exercises NTLM authentication.

**The forge emits documentation.** A method's `Doc` travels to the scaffold as JSON on
`forge new-collection --doc-json` (or `@file.json`), rendered into Go source by
`collectionscaffold`'s `renderDoc`. The generator used to emit only `Doc.Summary`, so any method
with a real parameter table failed `internal/archtest`'s `TestCatalogDataDocsMatchTheRegistry`
until a human retyped a page of prose into the generated file. Unknown JSON keys are refused rather
than ignored: a mistyped one would decode to "field absent" and fail that guard with no hint about
the cause.

**`go generate ./internal/forge/catalogdata` works on an already-generated tree.** It never had.
`forge new-collection` refuses to overwrite, so the command CLAUDE.md documents failed on the first
existing file. Every subcommand now takes `--skip-existing`, gencatalog passes it, and a second run
over an unchanged table writes nothing and reports that it wrote nothing.

**A successful task's output is visible.** `pleiades run --verbose` prints each task's own stats,
masked through the run's complete secret set; `engine.NodeResult` carries `Stats` to make that
possible. `run` also moved to `splitPositional`, so `run site.yaml --verbose` parses the way a
person types it instead of requiring flags before the positional.

**The WinRM gate checks the thing that actually broke the lab machine.** Its precondition verified
the Public WinRM firewall rule, which is a real hazard and was the wrong diagnosis. It now reads
the adapter's real addresses in one round trip and refuses when the target address is the one the
host already holds by DHCP (FAILURE_PATTERNS #159), when the host is already on APIPA, when it is
already static at that address, or when the Public rule is off. Each is a skip naming what to
change. The three tasks that exited non-zero on purpose so their output would print are gone.

**Three `file.*` findings, closed.** `file.touch` validated no attribute parameters, so an unquoted
`mode: 0600` was silently dropped and the task reported success having applied nothing; the
mode/owner/group rules now live once in `internal/catalog/file/attributes.go` rather than in two
copies with a third method missing them. `file.permissions` derived `diff.after` from the request,
which is wrong in exactly the case that matters most, since the kernel silently clears setgid on an
unprivileged chmod and clears setuid and setgid on any ownership change; it re-reads now, and only
when something changed. `remotefile.Apply`'s chown-before-chmod ordering is pinned by an assertion
that observes the commands through fakes on PATH, so it holds without root.

**A worked example, end to end.** `docs/11-extending-pleiades.md` gained a nine-step walkthrough
taking `exec.winrm.shell` from naming it to running it against a real host, with real commands and
real output. That method is the example because it needed every step: a namespace that did not
exist, a primitive that had to move into `pkg/` first, a capability, an honest reversibility
answer, and a release gate. The same file's claim that nothing checks `RequiredCapabilities` was
corrected: the dispatcher does now, `validate` still does not, and the difference is the point.

### Read this first: three corrections that cost real work

**Module names are `xxx.xxx.xxx`.** WinRM shipped first as `winrm_exec`, copied from `ssh_exec`,
the oldest dispatch path in the module. FAILURE_PATTERNS #158. Do not add another bare action name.

**Runbooks are authored in sugar with a `metadata:` block.** Module-as-key, not `fqcn:`/`params:`.
`examples/upgrade_ios/pleiades/runbooks/upgrade_ios_xe_sugar.yaml` is the reference.

**A skip is per entry, never per file.** Inverting the forge's per-file refusal into a per-file skip
wrote fifteen generated starter tests underneath real implementations, each asserting the method is
declared and unimplemented. FAILURE_PATTERNS #160. Found by running the generator against the real
repository and reading `git status`, not by any test.

### The remainder, in order

1. **The remaining namespaces**, which is the standing goal and the largest thing left: `identity.*`
   (6), `pkg.apt/dnf/*` (9), `cloud.aws.*` (4), `fw.*` and `container.*` (6),
   `net.cli/ios/eos/junos/netconf` (6, needs a NETCONF transport), `fs.*`/`archive.*` (4),
   `svc.windows.*` and `win.feature.*` (7, transport-unblocked but needing the two Windows
   capability accessors), and `file.template` (renderer is `internal/render`, unreachable from a
   Collection). **`pkg.*` is the best next move**: nine methods, the generic-plus-concrete
   dispatcher shape `svc.*` already proves, no new primitive, and the walkthrough in
   `docs/11-extending-pleiades.md` is now the procedure to follow.
2. **Make `exec.shell` dispatch on capability**, the way `svc.start` resolves to `svc.systemd.start`.
   A design step rather than a port, which is why it is not done: the generic method needs a broad
   capability carrying a resolver (the equivalent of `ServiceManagerName`), the current SSH
   implementation has to move to its own concrete FQCN, and `windows.Server` has to satisfy whatever
   the generic one requires. `exec.winrm.shell` is already the concrete half and needs no change.
3. **`file.directory` still has its own mode validator**, accepting three or four digits where
   `attributes.go` accepts one to four. Unifying them changes which runbooks are accepted, so it was
   left alone rather than folded in as a side effect of removing duplication. Decide it on its own.

### The Windows lab

`examples/windows_lab/` is the worked example, with the inventory carrying the same host twice,
IPv4 and IPv6. The IPv6 entry is the rescue path and it is not theoretical: it was confirmed
reachable while IPv4 was completely dark. The host is healthy and on DHCP at its leased address,
confirmed this session through the platform.

The WinRM gate skips without the `PLEIADES_WINRM_*` variables, and **`PLEIADES_WINRM_IP` must be an
address outside the DHCP pool**, never the one the host currently holds. The precondition refuses
that case now, but the check exists because the mistake is easy and expensive, not because it makes
the mistake harmless.

### Coverage, including one gap that predated this session

The ratchet caught five regressions and every one was raised rather than waived. Four were this
session's: new code in `cmd/pleiades` (`parseDocJSONFlag`, `firstExistingFile`, `printNodeStats`),
`internal/catalog/file` (`attributes.go`), `tools/gencatalog` (the generation loop, now extracted as
`generateEntries` so it can be driven against a synthetic table the way `validateCatalogEntries`
already is), and `internal/engine`.

`internal/engine` was the awkward one: it measured 93.1% against a floor of 93.2% on one run and
exactly 93.2% on the next, which is a package sitting on its floor with a branch that is not always
reached. Chasing the flaky statement would have fixed the number and nothing else, so it gained
real margin instead, from tests over the conditional model's refusal paths: the YAML and JSON
shapes that are not conditions, an expression that does not compile, and an expression that
compiles and then cannot be evaluated. That last distinction is the one worth having, since
collapsing "could not evaluate" into "evaluated false" silently skips a task whose condition was
broken. 93.8% now, consistently.

One of those tests initially imported `gopkg.in/yaml.v3` rather than the `go.yaml.in/yaml/v3` this
repository uses. Both are in the module graph, so it compiled and ran, and the decoder it exercised
had never heard of `StringList.UnmarshalYAML`. Worth knowing about, because the symptom was an
assertion failing on an error message rather than anything that looked like a wrong import.

The fifth was inherited. `pkg/sdk` measured 84.5% against a recorded floor of 100.0% with no
working-tree changes at all, which means `make push-gate` had been failing on it at HEAD: the whole
of `RecordInverse` was uncovered, so the function that writes the undo instruction a rollback will
one day run had never been executed by a test. It is at 100% again, and the shape is pinned,
including that nil params encode as `{}` rather than `null`.

Two printers that had been at zero coverage are now tested, `printNodeStats` and `printMetadata`,
both including the masking they owe: each prints values a task read off a device, and a task can
register a value an earlier `register_mask` marked secret.

### Verification state

`make push-gate` passes, including every Docker-backed package (`internal/lock`, `internal/event`,
`internal/transport/ssh`, `internal/api`, `cmd/pleiades`, `cmd/runner`, `tests/e2e`), which the
previous session could not run at all. `govulncheck` is clean after the dependency bump; it is what
found it.

Every behavior change this session is pinned by a test proven to fail: the doc renderer against
three mutations, the Doc round trip against one, `run --verbose` against four (including the
negative case, where the default run prints stats anyway), the adapter parser against two,
`printMetadata`'s ordering against one, the attribute rules against one, and each `file.*` fix
against one.

One of those mutations found a defect in the test rather than the code, which is worth repeating
because it is the argument for the whole practice. `TestRecordInverse` asserted that nil params
become an empty map by type-asserting the result and checking its length, and a typed nil map
satisfies both, so the test passed with the normalization deleted. It asserts on the JSON encoding
now, which is the only place the distinction is observable and the only place it does damage.

Two things are honestly unproven. The WinRM gate's own conversion path has not run since it was
rewritten, because running it means converting a real adapter on the only lab host available; the
precondition's data gathering was confirmed live (the host really does report
`current=10.0.0.246|Dhcp`, in exactly the shape the parser expects) and the parser is unit tested,
but the four-line comparison between the parsed address and the configured one has been read rather
than executed. And `exec.winrm.shell` was exercised live only on a read-only task.

### Commit message

```
fix(forge,cli,file): close the outstanding defect list, and a vulnerable dependency

Adds no catalog methods. Everything here is a defect the previous
session recorded and left, plus one govulncheck found on the way past
and one the coverage ratchet had been failing on unnoticed.

go-ntlmssp before v0.1.1 can panic parsing a malformed NTLM challenge
(GO-2026-5543). It arrived as an indirect dependency of the WinRM
client, and this platform reaches it on every exec.winrm.shell task and
on any http.request whose server answers with a challenge, which makes
the panic reachable by whatever the platform connected to. Pinned to
v0.1.1 and verified against the real Windows host, because no test here
exercises NTLM at all.

The forge emits a method's whole Doc rather than its summary. A Doc
travels as JSON on new-collection --doc-json, so catalogdata's entry
reaches the generated manifest intact and a scaffolded method no longer
fails archtest's equality guard until a human retypes a page of prose.
An unknown JSON key is refused: it would otherwise decode to "field
absent" and fail that guard with no hint about the cause.

go generate ./internal/forge/catalogdata works on an already-generated
tree, which it never had. The three forge subcommands take
--skip-existing and gencatalog passes it. Skipping is per ENTRY rather
than per file, and the first attempt got that wrong: a per-file skip
wrote fifteen generated starter tests underneath real implementations,
each asserting the method is declared and not implemented.
FAILURE_PATTERNS.md #160.

run --verbose prints a task's own output. A successful task's stdout was
printed nowhere, so three tasks in the WinRM gate exited non-zero on
purpose to make the error path print it, which meant those assertions
ran against the error path while claiming to be about the success one.
LESSONS_LEARNED.md #145.

The WinRM gate's precondition checked the firewall rule, which was the
wrong diagnosis. It now refuses when the target address is the one the
host already holds by DHCP, which is what actually took the lab machine
down twice, and keeps the firewall check as the secondary hazard it is.

Three file.* findings: file.touch dropped an unquoted mode in silence
and now refuses it, with the mode, owner and group rules in one place
instead of two copies and one omission; file.permissions re-reads the
path instead of deriving its diff from the request, because the kernel
silently clears setgid on an unprivileged chmod; and Apply's
chown-before-chmod ordering is finally pinned by an assertion that does
not need root to hold.

pkg/sdk.RecordInverse had no test at all, which the ratchet had been
reporting as an 84.5 percent package against a 100 percent floor since
before this branch. It writes the undo instruction a rollback will run,
and only the forward run can capture those values, so its shape is now
pinned including that nil params encode as an object rather than null.

Also: docs/11-extending-pleiades.md gains a nine-step worked example
taking exec.winrm.shell from naming it to running it against a real
device, and loses a stale claim that nothing checks
RequiredCapabilities.
```

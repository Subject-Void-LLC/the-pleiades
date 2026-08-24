# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**Phase 86.5 is complete, built for real this time.** Its predecessor spec presented as 9 of 12 items
done; none of it existed (`FAILURE_PATTERNS.md` #202). This session built the real thing, verified
against `go test`/`-race` and a real device, not assumed. Nothing has been committed yet (no
autonomous commits; awaiting the user's own go-ahead).

`pkg/remoteexec/shell.go` (new): `Conn.Shell`/`Shell.WriteLine`/`Shell.ReadUntil`/`Shell.Close`, the
PTY-based primitive `pkg/netcli` builds on. `WriteLine` sends a bare `"\r"`, not `"\r\n"`, and the first
draft used `"\r\n"` and, run for real against a device, made every prompt appear to print twice (a
phantom empty Enter from the trailing `\n`), caught only by live testing, never by the fake-server
suite, which passed throughout.

`pkg/netcli` (new): `Dialect`/`Session`/`IOS`/`FromPrompt`. Every literal in the `IOS` dialect (prompt
shapes, `terminal length 0`, the `(config-if)#` sub-mode a real interface creation drives it into, the
`"% Invalid input..."` error convention) was pinned against a real device
(`pkg/netcli/live_probe_test.go`, `PLEIADES_E2E_IOS`-gated), not assumed.

`net.cli.command`, `net.cli.config`, `net.ios.config` all flip to `StatusImplemented`. Catalog now 78
registered, **74 implemented, 4 declared** (was 71/7); `CLAUDE.md`'s own tally corrected to match.

Two further real bugs found only by running this against a real device, both fixed, both with a
regression test:

1. **A hang, not a slow response.** `net.cli.command`'s generic dialect has no paging-disable
   convention, so a long-output command (`show version`) paused on the device's real `--More--`-style
   pager forever, because `cmd/pleiades run` calls it with `context.Background()`. Fixed:
   `internal/catalog/net/cli/cli.go`'s `runCommand` bounds every generic-dialect command to 30s
   regardless of the caller's own ctx (`net.ios.config`'s own reads are deliberately NOT given this
   bound: its paging genuinely is disabled, and a large legitimate backup taking longer than any fixed
   bound is a real, different risk).
2. **An unmasked secret.** `net.ios.config`'s `backup: true` stat is a full running-config; on the real
   device it held a real `enable secret`, `enable password`, a local user's password hash, and a
   TACACS+ shared key, printed unmasked in `--verbose` output. Fixed by documenting
   `register_mask: backup` as required in the method's own `Doc.Returns` entry (there is no way for the
   platform to know a stat is secret without being told).

Release Gate: `cmd/pleiades/net_ios_config_release_gate_test.go`
(`TestCLI_RunAppliesAndRevertsIOSConfig`, `PLEIADES_E2E_IOS`-gated), drives the real binary through
`init`/`add-host`/`add-credential`/`run` against the real DevNet Catalyst 8000 Always-On sandbox,
verifies every claim over a second, independent connection this test opens itself. Passed clean
(12.5s), confirmed independently afterward that nothing was left on the shared device.

`examples/catalyst8000_lab/` (new): the same round trip as a runnable demo runbook, module-as-key
sugar form with a `metadata:` block (a first draft used explicit `fqcn:`/`params:`, see "Debt" below),
run for real against the same device, README documents both bugs above.

Doc generation confirmed idempotent by diffing two consecutive `gendocs` runs byte-for-byte, not just
re-running and eyeballing it. `TestCatalogDataDocsMatchTheRegistry` and the full `internal/archtest`
suite pass; `TestEveryImplementedMethodAnswersReversibility` reports 74.

### Documentation pass (same session)

Every runbook example the project ships now uses module-as-key sugar. 278 conversions:
266 in `Doc.Examples` across `internal/catalog/**` and their byte-identical
`internal/forge/catalogdata` twins, which regenerated 133 `fqcn:` lines out of 70 pages under
`docs/reference/`, plus 12 in `docs/02-get-started.md` and `examples/webserver_lab/`. The
transform is exactly lossless, which the parser guarantees rather than the author claiming it:
`internal/engine/task_syntax.go`'s `rewriteModuleKeyNode` accepts a mapping (arguments) or a null
scalar (no arguments), so `fqcn: X` plus `params:` becomes `X:`, `params: {}` becomes `X: {}`, and
a bare `fqcn: X` becomes a bare `X:`. Both forms were validated side by side through the real
binary before any file was touched, and all 14 example runbooks validate afterward.

Two files were deliberately NOT converted, and should stay that way:
`examples/upgrade_ios/pleiades/runbooks/upgrade_ios_xe.yaml` and the "Two ways to write a Pleiades
task" section of `examples/upgrade_ios/README.md`. That runbook is the explicit-form twin of
`upgrade_ios_xe_sugar.yaml` and exists purely to show the two shapes side by side; converting it
would delete the comparison.

Three stale claims were found by the same pass and corrected. `README.md` and
`docs/01-start-here.md` both said the catalog has "77 declared methods; 34 are implemented",
understating implemented methods by forty; the real figure, from the generated
`docs/reference/schemas/module-catalog.json`, is 78 registered, 74 implemented, 4 declared, and
both passages were rewritten around the short list of four that are NOT implemented rather than a
now-unwieldy list of what is. `docs/02-get-started.md` said `ssh_exec` "is the one action that
genuinely reaches a device today", which stopped being true long before this session; it now says
plainly that `ssh_exec` is a legacy action name kept working, and points at `exec.command` and the
catalog. The same file had an orphaned `params:` fragment left by the conversion, since it is a
partial snippet with no `fqcn:` line above it for the transform to anchor on; found by sweeping
for `params:` afterward rather than by assuming the conversion was complete.

### Three more Cisco IOS methods (same session)

`net.ios.facts`, `net.ios.ping` and `net.ios.save`, all built on the Phase 86.5 interactive CLI
transport, scaffolded through the real `pleiades forge` CLI and then implemented. Catalog goes to
**81 registered, 77 implemented, 4 declared**.

`net.ios.facts` closes a real hole rather than adding a convenience: `facts.gather` requires
`FactGathererCapable`, which no Cisco device type declares, so before this a Cisco device could be
commanded and configured but never described, and the only device facts in the catalog came from
`net.catalyst.device_facts` over Catalyst Center's REST API. It parses `show version`,
`show inventory` and `show ip interface brief`, and emits through `EmitFact`, matching
`facts.gather` and `net.catalyst.device_facts`.

**Every parser was written against output captured from the real device first**, via a new
read-only diagnostic (`pkg/netcli/live_probe_test.go`'s `TestLiveIOSFactsShapes`), and the captured
output is pasted verbatim into the unit tests as fixtures. Two shapes only a real device would have
revealed: an interface status can be TWO words (`administratively down`) followed by a one-word
protocol column, which a positional split silently truncates; and IOS omits the
`round-trip min/avg/max` clause ENTIRELY at 0 percent success rather than printing zeroes, which is
the ping shape a parser written against only the success case gets wrong. Both are covered.

`net.ios.save` is built but deliberately NOT exercised against the DevNet sandbox: `write memory`
would copy whatever other users have left in running-config into startup-config on a device we do
not own. Covered by unit tests only, and that gap is stated in the Release Gate's own doc comment
rather than left for a reader to find.

Release Gate: `cmd/pleiades/net_ios_config_release_gate_test.go`'s
`TestCLI_RunGathersIOSFactsAndPings`, read-only so it needs no cleanup, passed against the real
device (14.0s). It discovers the device's own management address over its own independent
connection rather than hardcoding one, because this sandbox has no outbound path and a gate
pinging the public internet would fail for reasons unrelated to this platform.

Two defects in already-staged work were found by running the FULL suite, which the documentation
pass before it had not done (it covered catalog/engine/forge/archtest only, and that gap is what
let them through): `cmd/pleiades/doc_test.go` failed because the sugar conversion removed the
`fqcn:` line it asserted on, and `pleiades doc --snippet` itself generated explicit `fqcn:`/`params:`
skeletons, meaning the CLI was handing users the very form the examples had just stopped teaching.
The generator now emits module-as-key sugar; a method with no parameters prints a bare
`module.name:`, which `rewriteModuleKeyNode` accepts explicitly as a module with no arguments.

`internal/catalog/net/ios` coverage moved 48.8 to 80.3 percent; its floor is raised 47.0 to 79.0.

### Phase 74a: NETCONF (same session)

**`net.netconf.config` is implemented and proven against real hardware.** Catalog goes to **81
registered, 78 implemented, 3 declared**; the not-implemented list is now only `file.template`,
`net.junos.config` and `net.eos.config`. Phase 74 was split rather than done whole: 74a is NETCONF,
and RESTCONF (74b), gNMI/gNOI plus the credential model (74c) and the Junos/EOS device types (74d)
keep every one of Phase 74's own 26 items, unrenumbered, with the mapping written into
`.SPECIFICATION/IMPLEMENTATION.md`.

Three new pieces. `pkg/remoteexec/subsystem.go`: `Conn.Subsystem`, the third session shape on the
existing `Conn` after exec and the PTY shell, an `io.ReadWriteCloser` and nothing more.
`pkg/datastore`: the shared port, with no `Commit`, because RESTCONF has no candidate datastore and
gNMI's `Set` is atomic per request, so a three-way port declaring one would be false for two of its
three implementations. `pkg/netconf`: RFC 6241 over RFC 6242 framing, implementing that port,
dialing nothing.

**Phase 74's own design was corrected in three places, each dated.** The largest: that phase has
`pkg/netconf` dialing `golang.org/x/crypto/ssh` directly and spends a whole item mitigating the
duplicated host-key handling. Both reasons it gives are about `internal/transport/ssh`, and neither
is true of `pkg/remoteexec`. Phase 74's measured starting position is 2026-08-09; `pkg/remoteexec`
first landed 2026-08-16 in `591441e`, so **the item predates the package it should have used**. The
mitigation item is therefore moot rather than done. Second: `encoding/xml` is not new to this module
(`pkg/filters/structured.go` already imports it with byte and depth bounds), though that phase's
technical finding about entity expansion still holds and is now asserted by test rather than cited.
Third, and not named by that phase at all: its device-type item would have left `NetconfCapable`
unreachable, because it supplies only the structural half. `FAILURE_PATTERNS.md` #204.

**Everything was pinned against the real device before it was written**, and four observations
changed the design: NETCONF answers on port **830 and not 22**, where the device accepts the
subsystem request and then immediately ends the channel (a client checking only the reply calls that
working); the hello is **48,790 bytes**; both `base:1.0` and `base:1.1` are advertised, so "both" is
the ordinary case; and **`:candidate` is not offered at all**, only `:writable-running`, which is why
`edit-config` requests `rollback-on-error` whenever a device supports it. A fifth shaped the error
type: this device sends **no `<error-message>`** for an unknown-element error, so a client keyed on
that field renders an empty reason for a real failure.

Testing is at three levels with no mocked transport anywhere. Unit tests drive bytes captured
verbatim from the device, with the framing reader and writer each pinned independently against one
real frame so a matched pair of bugs cannot pass. Five fuzz targets, ~2.2M executions, no crashers. A
container conformance suite runs against **Netopeer2**, which disagrees independently and has earned
it (`unknown-namespace` with a message where Cisco says `unknown-element` with none) and is where
`:candidate`, `commit`, `discard-changes` and `lock`/`unlock` are proven, since the Cisco sandbox
cannot host them. Toxiproxy severs the connection mid-session, asserting the failure is *detected*
rather than merely timed out. Release Gate
(`cmd/pleiades/net_netconf_config_release_gate_test.go`) passed against the real Catalyst 8000,
verifying every claim **over an interactive CLI session it opens itself**: reading back over the same
NETCONF session proves the server echoes what it was sent, while a different protocol proves the
configuration actually changed.

One real defect was found by a test rather than by a user: `realOpenSession` dereferenced a nil
device to name it in a capability refusal, panicking instead of returning an error.

### Next step

Nothing is committed. This work is on `feature/Phase-74a-NETCONF`, cut from
`feature/cisco-cli-buildout` rather than from `main`, because it builds directly on `Conn.Shell` and
`pkg/netcli`, which exist only on that branch and are not merged. **Opening and merging the PR for
`feature/cisco-cli-buildout` first, then rebasing this branch onto `main`, is what keeps the
branch-per-phase rule intact rather than stacking a branch on a branch.** `make ci` passes apart from
`docs-gen-check`, which runs `git diff --exit-code` over regenerated documentation and so cannot pass
until this work is committed.

**A second real device arrived at the end of this session and is not yet used:** an IOS XR Always-On
sandbox (`sandbox-iosxr-1.cisco.com`, SSH 22, NETCONF 830, **gNMI 57777**). It matters twice over.
It is a second vendor NETCONF implementation, which is the only way to tell a NETCONF client from an
IOS-XE-shaped one; and its gNMI listener resolves Phase 74c's own open research item, which was
recorded as "not yet verified: a gNMI/gNOI target". The plan for extending testing onto it is in the
session that produced this work.

### Debt, carried deliberately

- Runbook authoring form was violated on the first draft of every file in `examples/catalyst8000_lab/`:
  explicit `fqcn:`/`params:` instead of module-as-key sugar, and no `metadata:` block, despite an
  existing memory (`pleiades-runbook-authoring-form`) already stating the rule, corrected 12+ times
  before this session. Fixed in place; the memory itself was strengthened with a note that the failure
  mode is not missing information, it is not consulting what is already loaded before writing runbook
  YAML.
- `net.junos.config`/`net.eos.config` remain `StatusDeclared`: zero device types implement
  `JunosCapable`/`AristaEOSCapable`, and there is no Junos or EOS image in this environment, so
  generating those device types would produce types proved only by their own scaffolded tests, which
  is the shape `FAILURE_PATTERNS.md` #202 punishes. Phase 74d owns them. `net.netconf.config` is no
  longer in this list: Phase 74a implemented it.
- `SupportsNETCONF() bool` on `capability.CiscoIOSCapable` is now redundant with `NetconfPort()` plus
  the `netconf_enabled` property `netconfBaseline` reads. It stays because removing a method from
  that interface is a separate breaking change with its own blast radius, which is what Phase 74
  already said to do; it is named debt, not a second one.
- `pkg/datastore`'s `Store` interface has exactly one implementation today. That was a deliberate,
  approved call rather than an oversight, and the defense is written into the phase's own Adversarial
  item: its VALUE types are provably the intersection the RFCs leave, its verb set is copied from
  three existing operation lists rather than coined, and the operations that do not generalize are
  excluded by name with the reason recorded. It is still an interface with one implementer until 74b
  lands RESTCONF, and that is the honest reading of it.
- `examples/catalyst8000_lab/`'s four runbooks all name `Loopback8990` literally, on a device shared
  with every other DevNet sandbox user, so two concurrent runs of the example would fight over one
  interface. The Release Gate covering the same two methods derives its interface name from its
  process ID; a runbook has no equivalent, because task-param rendering at run time is a separate
  unbuilt piece of work. Documented prominently in the example's own README and in each runbook's
  header rather than papered over.

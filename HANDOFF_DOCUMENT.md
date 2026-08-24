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

### Next step

Nothing is committed. This work is on `feature/cisco-cli-buildout`, cut from `main` at 49386d4; an
earlier revision of this document claimed it sat on `main` and still needed a branch, which was never
checked and was wrong. `make ci` has now been run in full: 13 of its 14 targets pass, and the one
failure is `docs-gen-check`, which runs `git diff --exit-code` over regenerated documentation and so
cannot pass until this work is committed. Remaining: the user's own review and go-ahead to commit.

### Debt, carried deliberately

- Runbook authoring form was violated on the first draft of every file in `examples/catalyst8000_lab/`:
  explicit `fqcn:`/`params:` instead of module-as-key sugar, and no `metadata:` block, despite an
  existing memory (`pleiades-runbook-authoring-form`) already stating the rule, corrected 12+ times
  before this session. Fixed in place; the memory itself was strengthened with a note that the failure
  mode is not missing information, it is not consulting what is already loaded before writing runbook
  YAML.
- `net.junos.config`/`net.eos.config` remain `StatusDeclared`: zero device types implement
  `JunosCapable`/`AristaEOSCapable` (Phase 74's own open item), and `net.netconf.config` remains
  Phase 74's entirely.
- `examples/catalyst8000_lab/`'s four runbooks all name `Loopback8990` literally, on a device shared
  with every other DevNet sandbox user, so two concurrent runs of the example would fight over one
  interface. The Release Gate covering the same two methods derives its interface name from its
  process ID; a runbook has no equivalent, because task-param rendering at run time is a separate
  unbuilt piece of work. Documented prominently in the example's own README and in each runbook's
  header rather than papered over.

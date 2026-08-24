# A non-destructive Pleiades runbook against a real Cisco device

Everything in this directory has been run, for real, against Cisco's own public
DevNet Catalyst 8000 Always-On sandbox (a real Catalyst 8000V running IOS XE
17.15.04c), using `net.cli.command` and `net.ios.config` (Phase 86.5). Unlike
`examples/upgrade_ios`, this is not a target shape for a future phase to fill in:
it genuinely executes today, and this README says so because that claim is
checkable (`pleiades run runbooks/round_trip.yaml`), not because it reads well.

## What it does

`runbooks/round_trip.yaml` creates a loopback interface, then immediately removes
it again, on a device you do not own and share with every other DevNet sandbox
user at the same time. A loopback interface carries no traffic and cannot affect
reachability to or through the device, which is what makes it the safe thing to
demonstrate a real write against a real, shared device with.

**Change `Loopback8990` to a number of your own before you run any of this.**
All four runbooks name that one interface literally, and the device is shared:
if someone else is running this example at the same time you are, one run's
cleanup task will remove the other run's interface, and each will then see a
verification it cannot explain. Pleiades cannot pick the number for you here,
because a runbook cannot yet compute a value at run time (task-param rendering
is a separate, unbuilt piece of work), so this is a genuine limit of today's
engine rather than an oversight in the example. The Release Gate covering these
same two methods derives its own interface name from its process ID for exactly
this reason; a runbook has no equivalent, so pick a number and use it in all
four files.

```
cd examples/catalyst8000_lab/pleiades
pleiades add-host cat8000 --type cisco_router \
  --set host=devnetsandboxiosxec8k.cisco.com --set cli_prompt='Cat8kv#'
pleiades add-credential cat8000 --username <your reservation's username>
pleiades run runbooks/round_trip.yaml --verbose
```

(`inventory.yaml` already declares `cat8000`; the `add-host` above is only needed
if you deleted or never ran `pleiades init` in this directory yourself.)

## The other three runbooks

- `runbooks/verify_present.yaml` -- read-only. Run it between `round_trip.yaml`'s
  two tasks (or right after, since the interface is gone again quickly) to see
  the interface really landed, independent of trusting Pleiades' own "changed".
- `runbooks/verify_absent.yaml` -- read-only. Confirms cleanup: IOS refuses `show
  running-config interface <name>` for a name that does not exist
  (`% Invalid input detected at '^' marker.`) rather than returning empty output,
  which is the proof of absence this runbook checks for.
- `runbooks/cleanup.yaml` -- a safety net, not part of the normal flow. See "What
  this does NOT protect you from" below for exactly when to reach for it.

## Two real bugs this exercise found

Both were found by actually running these runbooks against the real sandbox, not
by reading the code, and both are now fixed with a regression test:

**A hang, not a slow response.** `net.cli.command` has no vendor-specific paging
convention (`netcli.FromPrompt` builds a generic `Dialect` with no
`DisablePaging`), so the first draft of `round_trip.yaml` ran a plain `show
version`, and it hung forever: the device's default terminal length paginated the
output with a `--More--`-style prompt this method has no way to answer, and
`pleiades run` calls it with `context.Background()`, which never would have
stopped it either. Fixed two ways: every generic-dialect command is now bounded
to 30 seconds (`internal/catalog/net/cli/cli.go`'s `runCommand`), so this fails
loudly instead of hanging forever, and the runbook itself now uses `show version |
include Version` to avoid triggering the pager at all. `net.ios.config`'s own
reads are unaffected: its `netcli.IOS` dialect really does send `terminal length
0`, proven by the same run's `backup: true` task capturing a real, un-truncated
6.8 KB running-config in one read.

**An unmasked secret.** `net.ios.config`'s `backup: true` stat is a full
running-config, not sanitized fact data: on this exact device it came back
holding a real `enable secret`, `enable password`, a local user's password hash,
and a TACACS+ shared key, all printed in the clear the first time `--verbose`
was used. `runbooks/round_trip.yaml` now carries `register_mask: backup` on that
task (the same mechanism `examples/upgrade_ios` already uses for a plain `show
running-config`), and `net.ios.config`'s own reference doc now says to use it.

## What this does NOT protect you from

Pleiades does not yet execute `rescue:`/`always:` blocks. The runbook schema
accepts `block:`/`rescue:`, but nothing routes execution to them yet, so the platform
can guarantee the removal task in `round_trip.yaml` runs if the creation task
fails partway through -- a network hiccup after `description ...` was sent but
before `end` was would leave the device sitting in configuration mode with the
interface partially applied. The two tasks are adjacent, with nothing else
between them that could itself fail, to keep that window as small as today's
engine allows. If `round_trip.yaml` ever reports its creation task failed, run
`runbooks/cleanup.yaml` immediately afterward.

Pleiades also has no `assert:`/`fail:`/`pause:` module yet. These are Ansible
control-flow builtins with no Pleiades equivalent today: a real, internally
tracked gap, not something this example works around. `verify_present.yaml`/
`verify_absent.yaml` register their read's output for a human (or `--verbose`) to
read; they cannot fail the run on their own if what they find is wrong.

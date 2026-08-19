---
status: beta
---

# Extending Pleiades

This book covers how the catalog, device types, and sync plugins actually grow
today. Read the framing carefully: this is a contributor workflow, not a
third-party plugin system.

## The extension surface today: one repository, no stability tiers

Every Collection method, device type, and sync plugin lives under `internal/`. Go's
own visibility rule makes an `internal/` package reachable only from code inside
this module or a fork of it, so there is no way to add one from outside this
repository's own source tree today. That means:

- There are no stability tiers (`experimental`/`beta`/`stable` for an extension
  API) yet, because there is no external extension API yet to tier.
- "Extending Pleiades" means contributing to this repository (or a fork), building,
  and shipping your own binary. It does not mean installing a third-party package
  into a stock release, the way an Ansible collection or an AWX execution
  environment does.
- A real out-of-tree extension mechanism (loading a Collection method, device type,
  or plugin without a fork and a rebuild) is designed but not built. When it lands,
  this section is where its stability tiers will be documented; until then, treat
  every extension point below as "internal, contributor-only."

## Forge commands

`pleiades forge` scaffolds the three kinds of extension: a new Collection method
(`new-collection`), a new device type (`new-device`), and a new sync plugin
(`new-plugin`). See [the generated CLI reference](reference/cli.md#pleiades-forge)
for every flag each one accepts; this section covers what each one actually
produces and what you do with it afterward.

## Generated repository file reference

Each `forge` command writes exactly two files, gofmt-clean, via Go's own
`go/format` package (no `gofmt` subprocess involved):

| Command | Files written |
|---|---|
| `forge new-collection` | `internal/catalog/<namespace>/<method>.go`, plus a matching `_test.go` |
| `forge new-device` | `internal/inventory/devices/<vendor>/<type>.go`, plus a matching `_test.go` |
| `forge new-plugin` | `internal/inventory/plugins/<name>/<name>.go`, plus a matching `_test.go` |

None of the three wires its own output into the binary automatically. A generated
Collection method registers itself into `pkg/collection` via its own package
`init()`, but that `init()` only runs if something imports the package: adding a
blank import of the new path to `internal/catalog/builtins.go` (or your own
composition root) is what actually makes it reachable. The same is true for a
generated device type (`internal/inventory/builtins.go`) and a generated plugin
(`internal/inventory/plugins/builtins.go`). A generated-but-unwired file compiles
and its own tests pass; it is simply invisible to `pleiades doc --list`,
`pleiades validate`, and the real dispatcher until wired in. This is deliberate, not
an oversight: adding a blank import is a one-line, reviewable, deliberate act of
turning a new extension on.

## The Collection method contract

A Collection method is a `pkg/collection.Descriptor`: a namespaced `Name`
(`<namespace>.<method>`, always dotted, a bare name is rejected at registration
time), a `Manifest` (the declared contract: required capabilities, supported
transports, execution context, platform targets, engine version, and `Status`), and
an `Invoke` function matching:

```go
func(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error)
```

`forge new-collection` generates a stub whose `Manifest.Status` is
`StatusDeclared` and whose `Invoke` refuses with an explicit "not implemented"
error; hand-completing it means writing the real body and flipping `Status` to
`StatusImplemented`. A `Manifest` claiming `StatusImplemented` with a `nil` `Invoke`
is refused at registration time, not silently accepted: `pkg/collection.Register`
checks this itself, so the "declared is not implemented" guarantee cannot be broken
by a copy-paste mistake.

Add real reference content while you do: `Manifest.Doc` (`Summary`, `Description`,
`Params`, `Returns`, `Examples`, `SeeAlso`) is what
[the module catalog](reference/modules/index.md) and `pleiades doc` both render. A
completeness test in `tools/gendocs` fails the build if a method claiming
`StatusImplemented` has an empty summary, an undescribed or untyped parameter, or
zero examples: filling in `Doc` is not optional busywork, it is a real, enforced
release gate for anything you flip to implemented.

## A worked example: adding a module end to end

This section walks the whole path for one real method, `exec.winrm.shell`,
which runs a script on a Windows host over WinRM. It is used as the example
because it needed every step rather than the easy subset: a namespace that did
not exist, a transport primitive that did not exist, a capability check, an
honest answer about reversibility, and a release gate against a real machine.

Read [the Collection method contract](#the-collection-method-contract) first.
This is the order to do the work in, and what each step is actually for.

### 1. Name it, and name it correctly

A method's name is `<namespace>.<method>`, dotted, and reads as a path down
from a capability to a specific action: `exec.winrm.shell`, `svc.systemd.start`,
`file.line.set`. It is not the transport plus a verb.

That distinction is worth spelling out because getting it wrong is easy and
this repository contains the evidence. WinRM shipped first as `winrm_exec`,
copied from `ssh_exec`, which is a real, load-bearing, well-tested name in this
codebase and also the oldest dispatch path in it. Age is invisible in source.
The tell available at the time was that every name under `internal/catalog` has
at least two dotted segments and the one being copied had none.

Two rules follow from the shape:

- **The concrete method names the platform it speaks to.** `exec.winrm.shell`
  and `svc.systemd.start` say Windows and systemd out loud, rather than hiding
  them behind a parameter.
- **A generic sibling resolves and dispatches.** `svc.start` requires the broad
  `ServiceManagerCapable`, asks the device which service manager it runs, and
  dispatches through the registry to `svc.systemd.start`. Build the concrete
  one first: it is the half that does work, and the generic one is a lookup
  table on top of it.

### 2. Find out whether the primitive exists, because the layering rule decides

A Collection package may import `pkg/`, the standard library, and third-party
modules. It may not import `internal/`, and `internal/archtest` fails the build
if it does.

This is not a formality, and it is the step most likely to change your plan.
There was already a WinRM client in this repository, under
`internal/transport/winrm`, and it was unreachable from a Collection method by
construction. The work was therefore not "write a method"; it was "move the
primitive into `pkg/winrmexec`, leave a thin adapter behind, then write the
method." The same thing had already happened for SSH (`pkg/remoteexec`), files
(`pkg/remotefile`) and services (`pkg/remotesvc`).

So before writing anything, check what your method needs to talk to a device
with:

| It needs | Import |
|---|---|
| A shell command over SSH | `pkg/remoteexec` |
| To stat, write, or change a file | `pkg/remotefile` |
| To start, stop, or query a service | `pkg/remotesvc` |
| A script on a Windows host | `pkg/winrmexec` |
| A credential, a connection, or a stat | `pkg/sdk` |

If the thing you need lives under `internal/`, moving it is part of your change.
Do that first and separately: it is a refactor with its own tests, and mixing it
into the new method makes both harder to review.

### 3. Pick the capability the method requires

`RequiredCapabilities` is what the dispatcher checks before invoking your method,
and it is matched structurally: a capability is a Go interface, and a device has
it when it both declares the name and satisfies the interface.

Pick the narrowest capability that is actually required. `exec.winrm.shell`
requires `WinRMCapable`, which is `WinRMHost() string` plus `WinRMPort() int`,
because that is exactly what it needs to reach the device.

Expect this step to expose a device type that never declared something it
already satisfied. Turning capability checking on for the first time broke
fifteen already-implemented methods here, because `linux.Server` had never
declared `POSIXFileSystemCapable` or `FactGathererCapable` while `file.*`,
`wait.*` and `facts.gather` had been requiring them all along. The fix is to
declare what is already true on the device type, never to drop the requirement
from the method.

### 4. Write the entry in `internal/forge/catalogdata`

The catalog is generated from a data table, so a new method starts as a
`collectionscaffold.Config` in `internal/forge/catalogdata/collections_*.go`,
not as a hand-written file:

```go
{
    Name:          "exec.winrm.shell",
    Capabilities:  []capability.Name{capability.NameWinRM},
    Transports:    []string{"winrm"},
    EngineVersion: engineVersion,
    Doc: collection.Doc{
        Summary:     "Runs a script on a Windows target through PowerShell or cmd.exe, over WinRM.",
        Description: "...",
        Params:      []collection.Param{ /* ... */ },
        Returns:     []collection.ReturnField{ /* ... */ },
        Examples:    []collection.Example{ /* ... */ },
        SeeAlso:     []string{"exec.shell", "exec.command"},
    },
},
```

Write the full `Doc` here, now, rather than after the code. Three reasons, and
the third is the one people discover the hard way:

1. `tools/gendocs` renders it into [the module catalog](reference/modules/index.md),
   and its completeness gate fails the build for a method claiming
   `StatusImplemented` with an empty summary, an untyped parameter, or no examples.
2. Writing the parameter table before the implementation is the cheapest design
   review available. A parameter you cannot describe in one sentence is usually
   two parameters.
3. `internal/archtest`'s `TestCatalogDataDocsMatchTheRegistry` compares this
   `Doc` to the one the registered manifest carries and requires them to be
   equal. They are two copies on purpose, and the guard is what keeps them from
   drifting, but it also means a `Doc` written only in one place fails the build.

### 5. Run the forge

Never hand-write the generated file. Regenerate the catalog:

```console
$ go generate ./internal/forge/catalogdata
gencatalog: visited 77 collection(s), 4 device type(s), 1 sync plugin(s); wrote 2 new file(s), left the rest alone, and regenerated the catalog builtins aggregator
```

This rebuilds the real `pleiades` binary and drives it through
`forge new-collection` once per table entry, so the catalog is produced by the
command a user runs rather than by calling the generator library directly. It
passes `--skip-existing`, so entries already on disk are left exactly as they
are and only your new one is written. Running it twice writes nothing the second
time.

To scaffold a single method without touching the table, call the CLI directly:

```console
$ pleiades forge new-collection exec.winrm.shell \
    --capabilities WinRMCapable \
    --transports winrm \
    --engine-version '>=1.0.0' \
    --doc-json @winrm-shell-doc.json
wrote internal/catalog/exec/winrm/shell.go
wrote internal/catalog/exec/winrm/shell_test.go
"exec.winrm.shell" is registered and reachable through the real dispatcher, which refuses it with "declared but not implemented" until it is really implemented; see the generated package's own doc comment.
```

`--doc-json` takes a JSON `pkg/collection.Doc`, or `@path` to read it from a
file, which is the usable form for anything longer than a summary. An unknown
key is refused rather than ignored, because a mistyped one would otherwise
decode to "field absent" and produce a method whose documentation quietly failed
the equality guard in step 4.

What you get is two files. The source file carries the registration with
`Status: collection.StatusDeclared`, your `Doc` rendered in full, and a stub
`Invoke` that returns an explicit error. The test file proves the registration
happened and that the stub refuses.

### 6. Hand-complete the method

Three things change, and the middle one is the one people skip.

**The body.** Replace the stub with the real implementation. Read state, compare,
act only on the difference, and report `Changed` honestly: a converged run must
report `Changed: false` and send no command, which is what keeps a runbook from
reporting a change forever. Some methods cannot converge (a script's effect
cannot be inspected before running it), and those report `Changed: true` every
time and say why in their `Doc`.

**Reversibility.** `collection.Register` refuses an implemented method that
claims `Reversible: false` without a reason, so this is a question at authoring
time rather than a panic at process start. The manifest answers only *whether*:

```go
Reversibility: collection.Reversibility{
    Reversible: false,
    Notes: "This platform cannot see what a script did, so it cannot say what would undo it. ...",
},
```

*Which* inverse it is belongs to the run, not the manifest, because the same
method with the same parameters undoes to different things depending on what it
found. A run that changed something emits the concrete instruction with
`sdk.RecordInverse`, carrying an FQCN and resolved parameters:

```go
sdk.RecordInverse(rc, sdk.Inverse{
    FQCN:        "file.permissions",
    Params:      map[string]any{"path": path, "mode": before.Mode, "owner": before.Owner, "group": before.Group},
    Description: "restore the mode, owner and group " + path + " had before this run",
})
```

Nothing performs a rollback yet. The recording exists because only the forward
run can capture the values an undo would need, so a run that does not record
them destroys the information permanently.

**The status.** Flip `Status` to `collection.StatusImplemented`. Until you do,
the dispatcher short-circuits with "declared but not implemented" and your body
is never reached.

### 7. Wire it in

A generated package's `init()` only runs if something imports it. For a
Collection method that is `internal/catalog/builtins.go`, which `gencatalog`
regenerates for you; for a device type it is
`internal/inventory/builtins.go` and for a sync plugin
`internal/inventory/plugins/builtins.go`, both hand-maintained. Until the blank
import is there, the method compiles, its own tests pass, and it is invisible to
`pleiades doc --list`, `pleiades validate`, and the dispatcher.

Then regenerate the reference pages and confirm the tree matches:

```console
$ make docs-gen-check
```

### 8. Test it, then prove the test can fail

Three layers, in increasing order of what they prove.

**Unit tests against a real local SSH server.** `pkg/remoteexec/remoteexectest`
starts a real SSH server that runs every command through a real `/bin/sh`, so a
method's own tests exercise the real transport rather than a mock of it. When
you need to observe which commands were sent, put a fake executable on `PATH`
that records its arguments to a file named by an environment variable, rather
than pattern-matching the command string.

**A release gate against a real device.** A method's status moves to implemented
on the strength of a run against a real machine, not a unit test. For Linux
methods that is an ephemeral container (`cmd/pleiades/exec_command_release_gate_test.go`
starts a real `sshd` and drives the built binary through
`init`, `add-host`, `add-credential`, `run`). For Windows there is no container a
Linux machine can run, so `cmd/pleiades/winrm_static_ip_release_gate_test.go`
is gated on environment variables naming a real lab host and skips clearly
without one. A gate that skips is weaker than one that cannot, and saying so is
better than building a mock that would pass while proving nothing.

**A mutation of your own source.** Break the line the test is about, watch that
specific test fail, put it back. A test that has never failed has not been
tested. This is a rule here rather than a suggestion.

### 9. Run it for real, through the platform

```console
$ pleiades run runbooks/win_facts.yaml --verbose
```

`--verbose` prints each task's own output, which is how you read a device's
answer back on the success path. Runbooks are authored in sugar form, with the
module name as the mapping key:

```yaml
id: win_facts
hosts: win2025
tasks:
  - name: Report the OS caption
    exec.winrm.shell:
      shell: powershell
      command: (Get-CimInstance Win32_OperatingSystem).Caption
    register: os_caption
```

Reach the device through the platform, not through a side-channel client. If it
is unreachable, that is the next work item rather than something to route around:
every workaround proves something about the workaround.

### What this example did not need

For contrast, the steps a simpler method skips. A new `file.*` method needs no
new primitive (`pkg/remotefile` exists), no new capability
(`POSIXFileSystemCapable` exists and `linux.Server` declares it), and no new
namespace directory. It is steps 4 through 8 only, and the whole change is a
catalogdata entry, one `go generate`, one hand-completed body, and its tests.

## The sync plugin contract and its conformance suite

A sync plugin implements `syncplugin.Plugin`, a four-stage, strictly ordered
contract: `Connect` (authenticate against the upstream), `Discover` (stream raw,
unclassified records), `Classify` (map one raw record onto a device type and
capability set, or an explicit quarantine classification with a reason, never a
silent default), and `Sync` (run the full pipeline and reconcile into a
`Repository`, via the shared `Reconcile` helper rather than a hand-rolled
add/update/conflict decision). `Close` releases whatever `Connect` acquired.

This contract is verified by a real conformance suite
(`internal/inventory/plugins/conformance_test.go`), not by convention alone: one
shared set of assertions drives every real `Plugin` implementation through
identical call sequences. The two plugins that exist today (`static_yaml`, a local
file with no auth or paging, and `catalyst_center`, an authenticated, paged REST
API) are deliberately unalike, so the suite is shaped by both rather than by
whichever was written first. A new plugin joins the suite by adding one entry to
its backend table, never by editing a test function; if your new plugin fails an
assertion the other two pass, that is the suite doing its job.

## Doc requirements for contributed content

- A Collection method claiming `StatusImplemented` must carry a complete `Doc`
  block (see above); this is enforced, not a style suggestion.
- House style, enforced by this repository's own contributor guidelines (internal,
  not shipped): American English, no em-dashes, Google-style Go doc comments
  explaining *why* over *what*.
- Never cite this repository's internal, gitignored specification and roadmap
  documents from anywhere a real user can see them: `tools/docs-lint`, wired into
  `make ci`, fails the build if you do. Those files never ship, so a citation into
  one is a promise the shipped binary cannot keep.

## Testing your extension

`pleiades validate` against a runbook naming your new FQCN is the fastest signal: it
confirms registration succeeded (a name without a namespace, or an unknown capability
name, panics at process start instead) and that the method no longer refuses as
declared but not implemented.

`validate` does not confirm capability matching. No validator reads a manifest's
`RequiredCapabilities`: `internal/validate.CapabilityRule` keys off
`engine.ActionCapability`, a two-entry table holding only the legacy `ssh_exec`
and `ios_backup`, and skips every other FQCN. So `validate` will pass a runbook
whose method cannot run on its target.

The dispatcher does check, which is a different thing and worth being precise
about. Before invoking a method, the engine compares every name in its
`RequiredCapabilities` against the target device, resolving the capability
hierarchy (a device declaring `SystemdCapable` satisfies a requirement for its
parent `ServiceManagerCapable`) and asserting the device really implements the
interface rather than merely naming it. A mismatch is a clear refusal at run
time. What you do not get is that answer at plan time, so a runbook can pass
`validate` and fail on contact.

Write the test for it yourself: target a device lacking the capability and assert
the refusal. That test is also how you find out whether your requirement is the
right one, which is not obvious until something is checking it.

Beyond that, follow this repository's own
`make ci` (`build vet fmt test-race gosec govulncheck coverage docs-lint
docs-gen-check`) and `coverage-floor.json`'s ratchet: a new package starts
untracked (informational, not a failing gate) and is expected to get a real floor
entry soon after, per that file's own stated convention.

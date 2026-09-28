---
status: beta
---

# Extending The Pleiades

Pleiades is the execution engine. Its extension surface (the SDK, the code
generators, and the `pleiades forge` CLI namespace) is collectively called
Hephaestus. The full catalog convention and naming rationale are in
[The Forge](hephaestus.md).

This book covers how the catalog, device types, and sync plugins grow. There are
two ways to add a Collection method, and only one way to add anything else:

- **Contribute it** (every kind of extension): write it in this repository, or a
  fork, and build your own binary.
- **Ship it as an external Collection** (Collection methods only): build a separate
  program with the public `pkg/` SDK and point The Pleiades at the directory it lives in.
  No fork and no rebuild of The Pleiades. See [External Collections](#external-collections).

## The extension surface today

Built-in Collection methods, device types, and sync plugins live under `internal/`.
Go's own visibility rule makes an `internal/` package reachable only from code inside
this module or a fork of it, so device types and sync plugins can still only be added
by contributing to this repository. That means:

- "Extending The Pleiades" with a device type or a sync plugin means contributing to this
  repository (or a fork), building, and shipping your own binary.
- A Collection method is the exception. An external Collection imports only `pkg/`
  (`pkg/external`, `pkg/collection`, `pkg/sdk` and the other shared primitives there),
  so it builds outside this repository against a stock release. The contract it
  builds against is versioned and has a stability tier of its own; see
  [Releases and stability](13-releases-and-stability.md).
- What is not built yet: publishing and installing an external Collection through a
  container registry, and signature verification of one. Today you place the program
  in a directory yourself, and the directory's own permissions are the trust
  boundary. See [the trust model](#the-trust-model-and-its-limits).

## Before writing a device type: the generic types

A device type is Go code compiled into a release, which is cheap here and a wait for anyone
else. Four generic types cover a device nobody has written a type for, by the protocol it
speaks rather than by what it is:

| Type | Reached by | Baseline | Onboarding may add |
|---|---|---|---|
| `generic_ssh` | `host`, `port` | SSH transport, running a command | a POSIX shell, Linux, POSIX files, facts, systemd, firewalld, apt, dnf, POSIX accounts |
| `generic_netconf` | `host`, `netconf_port` (830) | SSH transport | `NetconfCapable` |
| `generic_http` | `base_url`, `http_auth` (`none`, `basic`, `bearer`), the TLS settings | an address | `HTTPAPICapable` |
| `generic_grpc` | `target` (`host:port`), `grpc_plaintext`, the TLS settings | an address | `GRPCCapable` |

The TLS settings (a pinned authority, a server name, mutual TLS, and the explicit flags an old
device needs) are described in [Running in production](10-running-in-production.md#device-tls-pinning-mutual-tls-and-old-devices).

A vendor type's capabilities are backed by its Go code. A generic type cannot do that, so
beyond its baseline its capabilities come from the device itself: `pleiades onboard <host>`
(or `POST /api/v1/inventory/devices/{name}/onboard`, scope `inventory:onboard`) probes it over
its protocol with its stored credential and records what the device's own answers prove, as
a revision in its history. The SSH probe runs one fixed script (no inventory value reaches
it) and grants a package manager only when both its tools answer; the NETCONF probe reads the
server's `<hello>`; the HTTP probe makes one verified, authenticated request to the base URL,
and reads the OpenAPI document when `openapi_path` names one; the gRPC probe asks the standard
health and reflection services. Nothing else grants a discovered capability: `add-host --set`,
`set-host`, a hand-written `hosts.yaml` and a sync plugin are each refused if they name the `discovered`
property, and a classification rule cannot add to a generic type. A generic device starts
`discovered`, which runs nothing, and is `active` once onboarding succeeds.

A generic type is enough when the methods you need run on what the protocol proves:
`exec.command` and `exec.shell` on an SSH login, the package, service and account methods on a
Linux host the probe recognizes, `net.netconf.config` on a NETCONF server, and `http.request`
against a device's own API (a `url` that is a path, such as `/interfaces`, is joined to the
device's base URL and carries the device's credential and nothing else's), on either tier. Write a vendor type
when a method needs something no protocol can report: a CLI prompt and paging convention
(`net.ios.*`), a version-specific accessor, or a capability whose truth depends on the model.
`pleiades forge new-device` below is still how that is done. A new type declares, with
`record.RegisterDispatchProperties`, the property keys its accessors read: they are what travels
to a Runner so it can rebuild the device as its real type, and `internal/archtest` holds the list
equal to what the type's code reads and refuses a key that names a secret. The scaffold writes an
empty declaration to add to as accessors are written.

## Forge commands

`pleiades forge` scaffolds the kinds of extension: a new Collection method
(`new-collection`), a new device type (`new-device`), a new sync plugin
(`new-plugin`), and a whole external Collection program (`new-external`). See
[the generated CLI reference](reference/cli.md#pleiades-forge) for every flag each one
accepts; this section covers what each one actually produces and what you do with it
afterward.

`forge new-external acme.motd.read` writes a directory, `acme-motd-read/` unless
`--dir` names another, holding a program that builds with `go mod tidy` and `go build`:
`main.go`, the method with a working read-only body (it runs `uname -a` on the target and
records the output), a test that runs the program's own `describe`, a README with the
build and install steps, and a `go.mod` holding only a `module` line and a `go` line. It
names no version of The Pleiades, because the right one is a version the `go` command works
out itself: `go mod tidy` resolves it through the module proxy and records its checksum.
The README also gives the offline route, a `replace` pointing at a local checkout, as a
step you take on purpose, since a `replace` is not checked against anything. The
`go.mod` makes the program a module of its own, so one scaffolded inside another
checkout stays out of that checkout's `./...`; `--no-go-mod` leaves it out, for a program
meant to join a module of yours. Nothing in this repository needs wiring afterward.

## Generated repository file reference

Each in-repository `forge` command writes exactly two files, and `new-external` writes
a program's five, with the Go files gofmt-clean via Go's own `go/format` package (no
`gofmt` subprocess involved):

| Command | Files written |
|---|---|
| `forge new-collection` | `internal/catalog/<namespace>/<method>.go`, plus a matching `_test.go` |
| `forge new-device` | `internal/inventory/devices/<vendor>/<type>.go`, plus a matching `_test.go` |
| `forge new-plugin` | `internal/inventory/plugins/<name>/<name>.go`, plus a matching `_test.go` |
| `forge new-external` | `<dir>/main.go`, `<dir>/<method>.go`, `<dir>/<method>_test.go`, `<dir>/README.md`, `<dir>/go.mod` (not with `--no-go-mod`) |

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

A method may also carry a `Check` function with the same signature, declared by
setting `Manifest.SupportsCheck`. It is what `pleiades run --mode check` calls in
place of `Invoke`: it reads the device, works out whether a real run would change
anything, reports that as `Result.Changed`, and changes nothing. See
[Check mode support](#check-mode-support) in the worked example.

A generated stub states `SupportsCheck: false` with a comment saying how to add check
support once the method is implemented; `collection.Register` refuses check support on a
method that is still a stub.

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
| To move a file's bytes to or from a device | `pkg/filexfer`, with `pkg/sftpxfer` or `pkg/scpxfer` |
| Structured configuration over NETCONF | `pkg/datastore`, with `pkg/netconf` |
| A credential, a connection, or a stat | `pkg/sdk` |

If what you need is a new protocol rather than a new use of an existing one,
read [What shape a new transport takes](#what-shape-a-new-transport-takes)
before writing it.

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

#### Check mode support

If the method reads state before it acts, it can also answer
`pleiades run --mode check`, and it should. Add a `Check` function beside `Invoke` and
set `SupportsCheck: true`; `collection.Register` refuses one without the other. The
reliable way to write it is to give `Invoke` and `Check` one shared code path that
differs only in whether the write happens, so the check reaches its answer through
exactly the read and comparison a real run makes:

```go
func Directory(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
    return directory(ctx, rc, device, params, collection.ModeExecute)
}

func CheckDirectory(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
    return directory(ctx, rc, device, params, collection.ModeCheck)
}
```

That is `file.directory`'s real code. The shared body parses the params, connects,
reads the path and refuses a file standing where the directory should be, in both
modes; only after that does the check branch off to predict instead of write. For an
attribute change, `pkg/remotefile` offers `Differs`, `PredictApply` and `PredictCreate`,
which are the same comparison `remotefile.Apply` acts on, so a prediction cannot
disagree with the real run.

Three rules for a check, and the engine enforces the third one:

- It changes nothing on the device, on every path.
- It reports `Changed: true` when a real run would change something. If it records a
  diff, `Before` is what it read and `After` is what a real run would leave.
- It never calls `sdk.RecordInverse`. Nothing was done, so there is nothing to undo.
  A check result that carries an undo instruction fails the task.

The engine marks every check result, and its diff, with `predicted: true`
(`sdk.StatPredicted`); a method never sets it, and a real run's result that carries it
is refused.

A method whose effect cannot be known without running it (a script, an arbitrary
command) leaves `SupportsCheck` false and says why in `Manifest.NoCheckReason`, in words
an operator planning a dry run can act on. A check run then names its task as "could not
check" with that reason and ends with status 3, rather than guessing or counting it as a
success; `pleiades validate` gives the same reason when it refuses `check_mode` on the
task, and the method's reference page prints it. `collection.Register` refuses a reason on
a method that does support check. Every built-in method without check support carries
one; for an external Collection's method it is optional, and without it the answer is the
bare "does not declare check support".

A method that can check some calls and not others supports check mode, and its `Check`
answers the calls it cannot with `collection.CannotCheck(reason)`:

```go
if params["creates"] == nil {
    return collection.Result{}, collection.CannotCheck("without creates, whether the command runs cannot be known without running it")
}
```

That task is then reported as "could not check" with your reason, exactly like a method
with no check support, while the calls your `Check` can answer are checked. Return it
only from `Check`: from `Invoke` it is an ordinary failure. It works the same from an
external Collection, where it crosses to The Pleiades as the response's `cannot_check` flag,
and a Pleiades build that predates the flag reports such a task as failed rather than
unchecked.

When the parameters alone decide it, as they do for `exec.command`, a built-in method
also sets `Descriptor.CheckCall` to that same function (`func(params map[string]any)
error`). Validation calls it, so `check_mode: true` on a call that could only ever be
reported unchecked is refused when the runbook is written rather than discovered when
it runs. Keep the two in step by having `Check` call the very function you set there.
`CheckCall` reads nothing but the parameters and never contacts a device; an answer that
depends on the device's state (a missing input a real run's command needs, say) belongs
in `Check` alone. It is a function, so an external Collection's description cannot carry
one, and its call-level answer arrives from its `Check` at run time instead.

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

## What shape a new transport takes

A transport is how bytes reach a device. Before writing one, decide its shape,
because the shape decides where it lives and which interface it implements.
This repository has three answers, and picking the wrong one is expensive to
undo.

**A command: `transport.Transport`.** If one operation sends a command string
and gets back standard output, standard error and an exit status, the protocol
is command-shaped. The engine's built-in task types (`ssh_exec`, `serial_exec`,
`serialtcp_exec`, `telnet_exec`, `winrm_exec`) reach devices through
`transport.Transport` in `internal/transport`, each named by a
`TransportBinding`. Reuse that interface unchanged.

A command-shaped protocol that can also run a command through a named shell
implements `transport.ShellTransport` beside it, rather than widening `Exec`.
WinRM is the one that does: `Exec` runs a Windows command line that no shell
parses, and `ExecShell` runs a script through cmd.exe or PowerShell when a task
sets `params.shell`. The executor asks for `ShellTransport` only when a task
names a shell, and refuses a task that names one its device's transport cannot
offer. See [the three Windows execution modes](#the-three-windows-execution-modes).

**A primitive a Collection calls: a `pkg/` package.** A Collection method may
import only `pkg/`, so anything a method calls lives there, whatever its shape:
`pkg/remoteexec` for SSH commands, `pkg/winrmexec` for WinRM. The engine's own
adapter over each (`internal/transport/ssh`, `internal/transport/winrm`) is a
thin translation onto the same primitive, so a Collection method and a
`winrm_exec` task send exactly the same WS-Man messages.

**Anything that is not a command: a narrow port of its own.** Some protocols
have no command string, no standard output and no exit status. NETCONF is
structured requests against a named datastore. A file transfer is a stream of
bytes, of any size, in either direction. For these, define a small interface in
`pkg/` holding only what every implementation genuinely shares, and write one
adapter per protocol that dials nothing itself:

| Port | Adapters | What it carries |
|---|---|---|
| `pkg/datastore` | `pkg/netconf` | a path and a payload against a named datastore |
| `pkg/filexfer` | `pkg/sftpxfer`, `pkg/scpxfer` | a file's bytes, put and got, confined to one root |

Keep a verb that only one protocol has off the shared interface, behind an
optional interface the caller asserts. NETCONF's `Commit` lives on
`netconf.Session`, and `Stat` lives on `filexfer.Stater`, because legacy SCP
cannot describe a file without sending it. A method on a shared interface that
some implementations cannot honor still compiles, and then fails device by
device at run time.

**Never widen `Exec`.** Adding `Put` and `Get` to `transport.Transport` looks
like less work, and it is the one answer here that is always wrong. `Exec`
returns output as a Go string, which cannot carry a multi-gigabyte binary file
without holding all of it in memory. Every existing transport (SSH, serial,
serial over TCP, Telnet) would also have to answer a question about files that
it was never asked. If a future protocol fits none of the shapes above, the
answer is another narrow port, not a wider `Exec`.

**If it rides SSH, reuse the connection.** `pkg/remoteexec` already dials,
verifies host keys, retries, trips a circuit breaker, and walks a bastion hop
chain. A protocol that runs over SSH starts from a `*remoteexec.Conn` instead of
dialing on its own. `Conn.Subsystem` opens a named subsystem (`"netconf"`,
`"sftp"`). `Conn.Start` keeps a command's input and output open as a live stream,
which is how legacy SCP talks. `Conn.Shell` opens an interactive terminal. A
second SSH dialer would have to rebuild all of that, and could get any part of
it wrong.

This is what a Collection method that moves a file looks like with the pieces
above. The path is resolved against the device's transfer root before anything
is dialed, so a destination that climbs out of the root is refused without a
connection ever being opened:

```go
ft, ok := device.(capability.FileTransferCapable)
if !ok {
	return fmt.Errorf("%s: device %q cannot transfer files", fqcn, device.Name())
}
dst, err := filexfer.Resolve(ft.FileTransferRoot(), dest)
if err != nil {
	return err // refused before any connection
}
conn, err := sdk.Connect(ctx, rc, device, params, fqcn)
if err != nil {
	return err
}
defer conn.Close()
sub, err := conn.Subsystem(ctx, "sftp")
if err != nil {
	return err
}
client, err := sftpxfer.Open(ctx, sub)
if err != nil {
	return err
}
defer client.Close()
return client.Put(ctx, dst, src, size, 0o644)
```

One gap to know about first: `sdk.Connect` dials the device directly. It does
not yet follow the device's bastion route, so a method that has to reach a
device behind a bastion needs that closed before it can ship.

## The three Windows execution modes

A Windows host can run a command three genuinely different ways, and a task
names which one. The WinRM service starts every command through `cmd.exe /C`,
whatever the client asks: the WS-Man option `WINRS_SKIP_CMD_SHELL` exists to ask
it not to, and Windows does not honor it (measured, and refused outright when
marked as required). So The Pleiades escapes every command line until that
`cmd.exe` passes it through unchanged, the same technique the Rust standard
library adopted for this problem after CVE-2024-24576, and the only parser
that acts on a command is the one the task chose.

| Mode | What reads the command | Use it for | What it costs |
|---|---|---|---|
| `none` | only the program the command line names | running a program with arguments | no builtins, no variables, no pipes; the program parses its own arguments |
| `cmd` | `cmd.exe /d /v:on /s /c` | a cmd builtin (`dir`, `set`, `%ERRORLEVEL%`) | one line only |
| `powershell` | `powershell.exe -NoProfile -NonInteractive -EncodedCommand` | cmdlets, the pipeline, the language | a PowerShell start per task; the script travels base64 encoded, which more than doubles its length |

Every mode shares one ceiling: the service's `cmd.exe` accepts at most 8191
characters, counted after escaping, so a script or value too large for that
belongs on standard input. The service's `cmd.exe` also runs any AutoRun command
the host's registry configures, before the task's own command; that is the
host's configuration, and nothing a client can switch off.

`winrm_exec` takes the mode as `params.shell` (default `none`), and
`exec.winrm.shell` takes it as `shell` (required).

**Values travel as data, never as script text.** `cmd.exe` and PowerShell have
disjoint metacharacter sets, so no one escape is safe for both, and a runbook
value spliced into a script is code. Put values in `env` instead: each name
arrives as an environment variable called `PLEIADES_` plus the name, read as
`$env:PLEIADES_NAME` in PowerShell or `!PLEIADES_NAME!` in cmd. Not
`%PLEIADES_NAME%`: cmd.exe expands that form before it parses the line, so a
value containing `&` would run as a command, and a cmd script that reads one of
its own values that way is refused. `cmd` mode turns on delayed expansion
(`/v:on`) for exactly this, which means a pair of literal `!` characters in a
cmd script needs escaping as `^^!`. An environment is visible to other
processes on the device, so it is for data, never a secret. A secret belongs on
standard input, which `pkg/winrmexec.RunWithStdin` provides for a method that
needs one.

**Exit codes are real.** PowerShell's `-EncodedCommand` normally reports only 0
or 1. The Pleiades adds one line after the script so a failing native program's own
exit code survives, a failed cmdlet reports 1, and a script that recovers from
an earlier failure reports 0.

**The interpreters are named by absolute path.** With no shell in front of it,
Windows looks for a bare program name in the working directory before the
system directory, so a file called `powershell.exe` planted there would run
instead. A Windows device's `cmd_path` and `powershell_path` properties say
where its interpreters live (a device that should use PowerShell 7 names
`pwsh.exe`), and `working_directory` says where commands start.

**Building a Windows command line by hand is the one hard part.** For `none`,
use `winrmexec.CommandLine(program, args...)`, which quotes each argument the
way the standard Windows argument parser expects: a program that uses it
receives exactly the arguments given. `cmd.exe` does not use that parser, which
is why `cmd` mode builds its own line.

## External Collections

An external Collection is a Collection method (or several) built as a separate program,
outside this repository, and run by The Pleiades as a child process once per task. It
imports only `pkg/`, so it builds against a stock release, and a runbook calls its
methods exactly as it calls a built-in one. [`examples/external_collection`](../examples/external_collection/)
is a complete, working one.

### The program

The whole `main` function is one call to `external.Main`, and each method is an
ordinary `collection.Descriptor`, written exactly as a built-in method's is:

```go
package main

import "github.com/Subject-Void-LLC/the-pleiades/pkg/external"

func main() {
    external.Main(noteWriteDescriptor())
}
```

`external.Main` validates every method through the same `collection.Register` a
built-in method goes through: a namespaced name, known capabilities, a reversibility
answer, check support that agrees with itself, and `StatusImplemented` (a program
exists to run code, so a declared stub is refused).

### Installing it

Build it and put the binary in a directory, then name that directory in
`PLEIADES_COLLECTIONS_DIR` wherever `pleiades` or `pleiades-runner` runs:

```sh
go build -o ~/pleiades-collections/note ./examples/external_collection
chmod 700 ~/pleiades-collections
export PLEIADES_COLLECTIONS_DIR=~/pleiades-collections
pleiades collection approve note
pleiades doc --list example
```

Nothing in the directory runs until you approve its exact build. `pleiades collection
approve note` runs the program's `describe`, confined, shows its SHA-256 digest and
every method it says it provides (what each needs, and whether it supports check mode),
and asks. The approval is recorded, with your account name and the time, in
`.pleiades-approvals.json` inside the directory. A rebuilt program is a new build and
must be approved again. For an image build, or to approve the next build on every
Runner before it arrives, `pleiades collection approve note --digest sha256:<hex>`
records a build without running anything; a program may have several approved builds
at once. `pleiades collection revoke note` withdraws them, and takes effect from the
next call, even in a Runner that is already running. `pleiades collection list` shows
what is approved.

Every program in the directory is loaded when `pleiades run`, `pleiades validate` or
`pleiades doc` starts, and when the Runner starts. From then on its methods are
checked by `pleiades validate`, listed by `pleiades doc`, and dispatched by the engine
like any other. The Controller never runs a Collection method, so it needs no copy.

### The contract

Pleiades runs the program with one argument:

- `describe`: the program prints every method and its full manifest as JSON on
  stdout. This happens once, when the directory is loaded.
- `invoke`: the program reads one request from stdin (the method, the mode, the
  task's params, the target device's name, address and capabilities, and the
  credential The Pleiades resolved for the task), runs the method, and writes one response
  to file descriptor 3: whether anything changed, the stats it recorded, or an error.

The credential is not a new mechanism. It is exactly what a built-in method receives
through `InjectSecrets()`: on the Walk tier, the machine credential bound to the
template, resolved when the job fans out, or the device's own stored credential when the
template binds none; on the Crawl tier, the device's stored credential. Bind credentials
to templates as you already do, and an external method uses them.

The request and response are the same JSON messages the Runner already exchanges with
its own per-task child process, served by the same code (`external.ServeChild`), so a
method's result is the same to the engine whichever kind of process produced it.

The program never runs on a managed device. It runs beside The Pleiades and reaches the
device the way a built-in method does, through `sdk.Connect` and the credential in the
request.

### The trust model and its limits

An external Collection runs with the same privileges as the `pleiades` process that
starts it and receives device credentials, so what may be loaded is decided by the
directory, and checked every time:

- **The directory and every program in it** must be owned by you or by root and must
  not be writable by your group or by anyone else. Anything else in the directory
  (a non-executable file, a symlink) is refused rather than skipped. Names starting
  with `.` are ignored.
- **Each program's exact build must be approved** in the directory's approval list
  (`pleiades collection approve`). An unapproved program is refused before it runs at
  all, naming the command; a build changed since it was approved is refused naming
  both digests. The list is held to the same ownership rules as the programs and is
  refused whole if any entry in it is malformed. It records a deliberate step and who
  took it; it is not a new trust root, since whoever can replace a program can also
  edit the list.
- **Each program's SHA-256** is recorded when it is loaded and checked again, with its
  approval, immediately before every run. A program changed after loading is refused,
  naming both digests. The run then executes the very file that was checked, through
  its open descriptor rather than its name, so a program swapped in under the same
  name in between is not what runs.
- **Pleiades's namespaces are reserved.** An external method may not use a namespace
  that any built-in method uses (`file`, `svc`, `net` and the rest, read from the
  running build, so a namespace the catalog adds later is reserved too), nor
  `pleiades` or `ansible`. A name in one of them can only mean code that ships with
  The Pleiades, so name your methods under your organization's name. `pleiades run
  --verbose` also prints, beside every result from an external program, the program
  and digest that produced it.
- **Nothing is replaced.** A method name that is already registered, whether by a
  built-in method or by another program, is refused at load, and a program that fails
  to load stops the command. A refused program never falls through to some other
  implementation of the same name.
- **What crosses the boundary** is only what is listed above. The credential travels on
  stdin, never in the command line or the environment. The program starts with its
  environment reduced to `PATH`, `HOME`, `TMPDIR`, `LANG`, `LC_ALL`, `TZ` and
  `PLEIADES_KNOWN_HOSTS`, so nothing else is handed to it. That limits what it is
  given; confinement, below, is what limits what it can reach. Its own stdout and
  stderr are captured, capped, masked for the credential, and logged, never parsed.
- **A program's text cannot take over your terminal.** A description holding a
  character a terminal acts on instead of showing (an escape sequence, a carriage
  return, a bidirectional override) is refused at load. A method's own error message,
  its results, and everything `pleiades collection approve` shows are printed with
  such characters escaped, so a program cannot draw fake output, such as a fake
  "approved" line, over the real output.
- **Every run is confined.** A program runs as the user running The Pleiades (on a Runner,
  the Runner's user), so on its own it could read whatever that user can: the project's
  credential store and its key under `.pleiades/`, your SSH keys, and The Pleiades
  process's own starting environment under `/proc`. Instead, every run is confined with
  Linux's Landlock to its own directory, the system's libraries, certificates, resolver
  files and time zone database, your `known_hosts` file, `/dev/null`, and a private
  temporary directory (its `TMPDIR`, removed when it exits). On a kernel with Landlock
  ABI 6 or later it also cannot signal The Pleiades. The Pleiades marks itself non-dumpable
  before starting a program, so the program can't read The Pleiades's memory or
  environment. Network access is not confined, since a method must reach devices on
  their own ports.
- **Checks stay off locked devices.** A method's `Check` from an external program is
  never run against a simulate-locked device (a device nobody has approved for changes
  yet, which is where a read-only sync source puts every device it adds). The task is reported as not checked there, because
  nothing has proven a third party's check only reads. Built-in checks, each tested
  against its real run, still reach such a device.
- **Granting more to read.** A method that needs a local file, for example one it
  uploads, can be given read access with `PLEIADES_COLLECTIONS_READ_PATHS`: absolute
  paths separated like `PATH`, each of which must exist. A path that is, contains or
  sits inside the project's `.pleiades` directory is refused, and so is one that is or
  contains your home directory. The collections directory itself is held to the same
  rule.
- **Every run is bounded**: a wall-clock limit, a cap on output and on the response,
  and a defined error for a program that hangs, floods its output, exits without
  answering, or answers with something that is not a valid response.

What this does not give you yet, stated plainly:

- **No signature verification.** Nothing checks who built a program. The directory's
  permissions are the whole trust decision, so treat adding a program to it the way you
  would treat installing any other binary that will hold your device credentials.
- **No registry.** Publishing and installing through a container registry is designed
  and not built. The approval list is the lockfile it is meant to extend.
- **No engine version check on development builds.** A method's `EngineVersion`
  (`">=1.2.0"`) is enforced by a release build, which refuses a program stating a newer
  release than itself; a release candidate counts as the release it is for. Every build
  says which it is: `pleiades version` and `runner version` print the same string, the
  release on a release build and `0.0.0-dev+<commit>` otherwise. A development build,
  which is every build until the first release, cannot compare and loads the method,
  warning once per program, and `pleiades doc` marks the constraint "not checked".
  `forge new-external` writes the generating release as the program's constraint on a
  release build, and none on a development build.
- **Linux only.** Loading needs Landlock (Linux 5.13 or later, enabled in the
  kernel), so it is refused on an older kernel, on macOS, BSD and Windows, rather than
  running anything unconfined. WSL 2 is Linux and works.

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
shared set of assertions drives every real `Plugin` implementation through identical
call sequences. The three plugins that exist today (`static_yaml`, a local file with
no auth or paging; `catalyst_center`, an authenticated, paged REST API; and `aws`,
which reads EC2 instances through the AWS SDK) are deliberately unalike, so the
suite is shaped by all of them rather than by whichever was written first. A new
plugin joins the suite by adding one entry to its backend table, never by editing a
test function; if your new plugin fails an assertion the others pass, that is the
suite doing its job.

## Doc requirements for contributed content

- A Collection method claiming `StatusImplemented` must carry a complete `Doc`
  block (see above); this is enforced, not a style suggestion.
- Every parameter a method reads must be declared, in `Doc.Params` or through a
  named fragment. `pleiades validate` refuses a task that passes a parameter its
  method does not declare, since the method would otherwise ignore it without a
  word. This holds for an external Collection program's methods too: one that
  reads `path` and declares nothing makes every runbook passing `path` fail
  validation until the program documents it.
- Never cite this repository's internal, gitignored specification and roadmap
  documents from anywhere a real user can see them: `tools/docs-lint`, wired into
  `make ci`, fails the build if you do, and that includes a Go string literal
  such as an error message or a flag's help text. Those files never ship, so a
  citation into one is a promise the shipped binary cannot keep.
- House style, enforced by this repository's own contributor guidelines (internal,
  not shipped): American English, no em-dashes, Google-style Go doc comments
  explaining *why* over *what*.

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

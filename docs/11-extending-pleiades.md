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

`validate` does not confirm capability matching, before or after your method is
implemented. No validator reads a manifest's `RequiredCapabilities`:
`internal/validate.CapabilityRule` keys off `engine.ActionCapability`, a two-entry
table holding only the legacy `ssh_exec` and `ios_backup`, and skips every other
FQCN. So `validate` will pass your method against a device that cannot run it. Check
that yourself, with a test that targets a device lacking the capability and asserts
the failure your method returns.

Beyond that, follow this repository's own
`make ci` (`build vet fmt test-race gosec govulncheck coverage docs-lint
docs-gen-check`) and `coverage-floor.json`'s ratchet: a new package starts
untracked (informational, not a failing gate) and is expected to get a real floor
entry soon after, per that file's own stated convention.

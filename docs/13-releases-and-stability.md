---
status: beta
---

# Releases and stability

## Changelog

[`CHANGELOG.md`](../CHANGELOG.md) at the repository root records what changed from one
release to the next. 0.3.0 is the first release, so its entry points to the release notes
below; the history kept under it is the development before any release, not a list of
changes to one. From 0.3.0 on, each change is a `changelog/*.md` fragment (see
[`changelog/README.md`](../changelog/README.md) for the naming convention and the change
types), assembled into `CHANGELOG.md` when a release is cut, by hand for now.

## Release notes

- [0.3.0 "Paranoid Porpoise"](releases/v0.3.0.md), the first release.

## Breaking changes and deprecations

A breaking change is recorded as a `changelog/<slug>.breaking.md` fragment, and the
release notes list each one. A change that breaks a generated schema (see
[How the contracts are checked](#how-the-contracts-are-checked)) cannot merge until a
fragment names it.

## Versioning and deprecation policy

Releases are numbered `MAJOR.MINOR.PATCH`. Every binary reports its own
(`pleiades version`, `runner version`, `controller version`): a release build's
version, stamped by `make release`, or `0.0.0-dev+<commit>` for any other build.

**`main` is on the 0.4.0 line.** 0.4.0 changes values 0.3.0 shipped (the facts
`facts.gather` records, for one), so it is a minor release, not a patch. A 0.3.x
patch release, if one is needed, branches from the `v0.3.0` tag and takes fixes only.

**A point release is always safe.** A patch release (`x.y.Z`) never breaks a
contract below, never removes or deprecates anything, and never adds a rule that
refuses something the release before it accepted. It may add: a new method,
parameter, runbook keyword or template feature (more of Jinja, for example) that
nothing already written depends on behaving otherwise. A runbook that ran on 0.3.0
runs the same on every 0.3.x.

**Before 1.0.0, a minor release may break compatibility**, and each one is a step
along a planned ladder rather than a point on a support calendar. Each break is
named in that release's notes, with what to change. Anything documented as
`status: beta` or narrower may change shape in the next minor release; check the
badge at the top of a page before depending on what it describes.

**1.0.0 is the first release whose stored data has to survive an upgrade**, and the
compatibility promises below are measured from it. No support window or
deprecation notice period has been published yet. When one is, it will be here.

A Collection method's `engineVersion` constraint names the oldest engine it runs
on, so a method built against one release states that release rather than a round
number.

## The contracts

Each contract below says what stays compatible, what forces a change of version or
a breaking-change note, and how that is tested. "Holds within a minor" means no
patch release breaks it, and a minor release breaks it only with a
`.breaking.md` fragment naming the break. "Versioned" means the format carries a
version number, and a reader refuses a version newer than its own by name rather
than guessing.

| Contract | Before 1.0 | Versioned by | Checked by |
|---|---|---|---|
| Runbook schema | holds within a minor | the release | the schema check |
| Module catalog (methods, parameters, returns, facts) | holds within a minor | the release | the schema check |
| Facts | holds within a minor | the release | the schema check, over the catalog's fact declarations |
| Run report (`--json`) | holds within a minor | `schema_version` | the schema check |
| API | holds within a minor | the release | the schema check, over the OpenAPI document |
| Rollback planning | holds within a minor | the release | golden tests |
| Template and variable semantics | holds within a minor | the release | golden tests |
| Run journal | may change at each minor | `format` on every line | golden tests |
| Dispatch payload (Controller to Runner) | may change at each minor | `payload_version` | tests on both sides |
| Database schema | expand by default | migrations | migration and upgrade tests |
| External Collection protocol | a window of versions, see its section | `protocol`, `protocol_min`, `protocol_max` | the loader's tests, and the schema check over the published window |

### Runbook schema and the module catalog

**What stays compatible:** a runbook that validated keeps validating and means the
same. New optional keywords and parameters, new methods, new values a parameter
accepts.

**What breaks:** a keyword or parameter removed or renamed, one becoming required, a
type or set of choices narrowed, a default changed, a method removed, a return value
or fact a method records removed or retyped, or a method needing a capability or
losing a transport it had.

### Facts

**What stays compatible:** a fact keeps its key, its type and whether it is stable or
volatile. New facts and new item families may be added.

**What breaks:** a key renamed or removed, a value's type changed, or a value's
meaning changed (a version reported as a string becoming an integer, for example).
Facts are stored by key, so a key is the identity of a fact's history: renaming one
starts a new history.

### Run report

The `--json` document `pleiades run`, `adhoc` and `rollback` print,
published as `docs/reference/schemas/run-report.json`.

**What stays compatible:** new fields, and new values in its open sets (a status or
kind, which the schema describes in prose rather than as an `enum`). A reader ignores
a field it does not know, treats a status or kind it does not know as not a pass, and
refuses a `schema_version` it does not know.

**What breaks:** a field removed or renamed, a required field becoming optional, a
field given another type or meaning, or a value added to a closed set (an `enum`).
`schema_version` rises with any of these; a reviewer holds a change to that rule, since
the schema check finds the break but not the missing bump.

### API

The Controller's HTTP API, published as `docs/reference/schemas/openapi.json`.

**What stays compatible:** new routes, new optional request fields, new response
fields.

**What breaks:** a route or method removed, a request field or parameter becoming
required or narrowed, a success status no longer returned, a required response field
removed or retyped, or a route needing a scope it did not.

### Rollback planning

**What stays compatible:** the plan `pleiades rollback` and `POST /jobs/{id}/rollback`
build from a given journal: which steps undo what, in which order, and which gaps
refuse until accepted by name.

**What breaks:** the same journal producing a different plan, a gap that refused now
passing, or one that passed now refusing.

### Template and variable semantics

**What stays compatible:** what a template renders to, and which value wins when one
name is set at several levels (runbook `vars:`, `set_fact`, device variables, block
and task `vars:`, then extra variables, which win over everything). New filters and
template features may be added.

**What breaks:** a template rendering differently, an error becoming a value or a
value becoming an error, or a change to the order of precedence.

### Run journal

The journal under `.pleiades/journal` records every task of a run, for audit and for
rollback. Each line carries `format`, the version of its shape; a line without one
was written before the field existed and reads as format 1.

**Until 1.0, the format may change at any minor release.** A build refuses a format
newer than its own by name, so an older build never plans a rollback from fields it
does not understand. A journal written by one release is read by every later one: the
change that adds a format keeps every earlier one readable, and a frozen format-1
journal holding every kind of entry is read back by every build's tests. 0.3.0 predates the field: it refuses a journal 0.4.0 wrote,
naming the unknown `format` key rather than the version.

From 0.4.0 each entry also names the Collection its method came from: the group, the
version of The Pleiades it was linked into, and a digest of the group's method
declarations, with a digest of the undo's own declaration beside it. A rollback
compares the undo's digest with its own build's, and only when none was recorded the
group's. The same digest replays as before. A different one is refused, naming both
versions and both digests, until `pleiades rollback --allow-skew <node>` (the API's
`allow_skew`) accepts it by node, and the plan then says which digests it accepted.
A group whose documentation alone changed moves only the group's digest, so it needs
no flag, and a method a later release moves to another group is a warning, never a
refusal. Whatever is named, every recorded value is still held to the build's own
declarations: a value under a parameter the undo no longer records, a value of another
type than the parameter now takes, and a parameter the undo now requires that the run
did not record are each refused, and only `--leave` answers them. An entry with no
identity (a journal from before 0.4.0) is checked against the build's declarations
alone, and the plan says so. The Controller stores a Runner's rows with only an
identity a run's journal writes; any other is stored as none, with a warning, and the
row is kept. On the Walk tier the Runner makes the same comparison
against its own build, so a node named in `allow_skew` is accepted even where the
Controller's declarations match the recording; the CLI, which plans and runs in one
process, refuses a node named with no skew to accept.

**`--allow-skew` is advisory until Collection identities are signed.** The digests are
computed from public declarations, so whoever can write a journal can write the
digests a build computes and suppress the comparison. It keeps an operator from
replaying an undo recorded under other declarations unawares; the checks of each
recorded value against the build's declarations are what hold either way.

### Dispatch payload

The message a Controller sends a Runner for each device of a job. It carries
`payload_version`.

**Until 1.0, the payload may change at any minor release**, and a Controller and its
Runners are upgraded to the same release. A new field whose absence means the old
behavior needs no new version. A field an older Runner would misread raises the
version, and a Runner refuses a payload newer than it reads: the job fails with the
reason rather than running part of what was asked. Runners from before 0.4.0 predate
the field and cannot refuse one.

A payload also names, under `collections`, each first-party Collection its runbook
runs, as the Controller's catalog identifies it: the group, the Controller's version
and a digest of the group's method declarations. The Runner compares each with its own
build and warns where the declarations differ, in its own log and in the job's log,
then runs the job: it runs its own code and records its own identity in the run
journal, which is what a later rollback compares. The digests decide whether the
declarations differ; the versions only choose the warning's wording, since the same
version with other declarations points to a stale catalog or a corrupt build. Two
builds with the same declarations warn nowhere, and the Runner notes the different
versions only at debug level. A built-in method whose Collection the Controller did
not name is noted at info level. A Runner of a release that cannot read the key
compares nothing; a Controller of one names nothing, and its Runners compare
nothing.

Two other Walk-tier messages carry no version yet: the journal batch a Runner sends
the Controller, and a job's result. Each is read leniently, so a field a newer Runner
adds is dropped by an older Controller. Upgrading the Controller and its Runners
together, as above, is what keeps them in step.

### The database schema's compatibility window

The database schema is a contract, between two builds of the
controller running against one database. The rule is written and enforced now, and
it becomes a promise to deployments from the first release on:

- **A migration expands the schema by default.** It adds tables, columns that may be
  empty or have a default, and indexes that are not unique, so the build before it
  keeps working against the schema it leaves. That is what lets the old controllers
  of a rolling upgrade keep serving, and a controller be rolled back without touching
  the database.
- **A migration that removes or narrows something is a contract**, declared with the
  oldest build that can still serve after it. Builds older than that refuse the
  database, and a running one stops.
- **Both are tested, not trusted.** Every migration's real effect on each dialect's
  schema is compared with its declaration, and an upgrade test runs the previous
  build against the newly migrated database and makes it do its ordinary work.

Until the first release, the history behind this rule is squashed rather than
carried: the 1.0.0 release starts from one baseline migration, and the compatibility
window is measured from there. [Running in
production](10-running-in-production.md#upgrading-and-rolling-back) has what this
means for an upgrade.

### The external Collection protocol

This is the contract between The Pleiades and an
[external Collection](11-extending-pleiades.md#external-collections). It covers the
`describe` output and the `invoke` request and response, and it is versioned by one
number. The versions a build speaks are `pkg/external.Protocols` (today `1`, which is
`ProtocolVersion`), and every build says so: `pleiades version`, `runner version`, the
Controller's `/.well-known/pleiades/extension-protocols.json` and `pleiades collection
list`.

- **A window, not one version.** A program names the range it speaks and the loader
  takes the highest version both sides speak. A release speaks the newest version
  and the one before it; the one before stays at least one minor release after the
  newest goes final (longer after 1.0), and is deprecated, with the release that
  removes it named, before it goes. A program agreed at a deprecated version warns
  once each time programs are loaded (every command, every Runner start).
- **A point release never moves the window.** A patch release never removes or
  deprecates a version, starts or shortens a window, adds a rule refusing a program
  the release before loaded, or changes which builds an approval list admits. It may
  add an optional field inside the current version. `make schema-compat` holds the
  window's published list to the last release, so a removed or deprecated version is
  a break a patch line cannot make.

- **What stays compatible within a version.** New optional fields may be added to
  the request, the response, or a method's manifest. Both sides ignore fields they
  do not know, so a program built against an earlier release keeps loading.
- **What forces a new version.** Renaming or removing a field, making a new field
  required, or changing how the response is delivered. The Pleiades refuses a program
  that speaks no version it does rather than guessing.
- **What every version refuses.** A program declaring a fact a built-in method holds,
  or one inside a built-in family, is refused at load (0.4.0 on): such a fact would
  steer targeting and baselines.
- **What is not covered.** The Go API of `pkg/` itself (the packages an external
  Collection imports) is not yet under a compatibility promise. Until the first
  release, rebuild an external Collection against the release you run.

### How the contracts are checked

`make schema-compat` (part of `make ci-remote` and every pull request's checks)
compares each schema in `docs/reference/schemas` with the same file at the newest
release tag at or below this tree's release line. A change to a schema for something a
user writes (a runbook, an inventory file, an API request) breaks when it refuses a
document the release accepted. A change to a schema for something the platform writes
(the run report, an API response) breaks when it removes a field or makes one
optional, or widens a type, an `enum` or a bound, so a reader meets something it was
never told about; a new field is not a break, since a reader ignores a field it does
not know. A keyword the check has no rule for counts as a break whenever it changes.
The module catalog is checked for the removals and changes listed above.

Each break is printed as one token, such as
`property-removed run-report.json#/properties/nodes`. On the 0.4.0 line, a
`changelog/<slug>.breaking.md` fragment that says what changed for a user and names
the token, in backticks, accepts it; a fragment naming a token that matches no break
fails too. On a patch line, nothing accepts a break.

The line is `internal/buildinfo.CurrentRelease`. Tagging a release makes the branch
it was cut from a patch line until the change that opens the next minor moves
`CurrentRelease`, which is the first change after a release that may break anything.

What has no schema (the journal, rollback plans, rendering and variable precedence) is
held by golden tests: a change to what they produce fails until its golden file is
updated on purpose, in a change a reviewer sees.

## Platform and compatibility matrix

Not generated yet. This page will list which OS/architecture combinations the
static binary is built and tested for, once a release process produces more than
one.

## Known issues

See [Start here](01-start-here.md)'s Limitations section and
[Running in production](10-running-in-production.md) for the honest, current list
of what does not work yet. This project does not maintain a separate "known issues"
list distinct from what those pages already state plainly.

## Roadmap

Not published externally today. The internal roadmap this project tracks against is
a private planning document, not a public artifact; there is no public roadmap page
to link here yet.

## Developer updates

None published. `CHANGELOG.md`'s `Unreleased` section is the closest thing to a
running account of what has landed recently.

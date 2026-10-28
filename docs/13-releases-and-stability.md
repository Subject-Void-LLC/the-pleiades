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

None yet, for the same reason: nothing has shipped a version a later one could break
compatibility with. `CHANGELOG.md`'s own `breaking`/`deprecated`/`removed` fragment
types exist and are ready to record one the first time it happens.

## Versioning and deprecation policy

Releases are numbered `MAJOR.MINOR.PATCH`. Every binary reports its own
(`pleiades version`, `runner version`, `controller version`): a release build's
version, stamped by `make release`, or `0.0.0-dev+<commit>` for any other build.

**Before 1.0.0, a minor release may break compatibility**, and each one is a step
along a planned ladder rather than a point on a support calendar. A patch release
never breaks anything, and it may add: a new method, parameter, runbook keyword or
template feature (more of Jinja, for example) that nothing already written depends on
behaving otherwise. A runbook that ran on 0.3.0 runs the same on every 0.3.x. What
that means in practice:

- Anything documented as `status: beta` or narrower may change shape in the next
  minor release. Check the badge at the top of a page before depending on what it
  describes.
- The two contracts below are the exceptions, and they hold from the first release
  rather than from 1.0.0: the database schema's compatibility window, and the
  external Collection protocol version.
- A Collection method's `engineVersion` constraint names the oldest engine it runs
  on, so a method built against one release states that release rather than a round
  number. Today every built-in method declares the release it ships in.

**1.0.0 is the first release whose stored data has to survive an upgrade**, and the
compatibility promises below are measured from it.

No support window or deprecation notice period has been published yet. When one is,
it will be here.

### The database schema's compatibility window

The second versioned contract is the database schema, between two builds of the
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

### The one versioned extension contract

One interface is versioned today, because code outside this repository builds
against it: the contract between The Pleiades and an
[external Collection](11-extending-pleiades.md#external-collections). It is
`pkg/external.ProtocolVersion`, currently `1`, and it covers the `describe` output
and the `invoke` request and response.

- **What stays compatible within a version.** New optional fields may be added to
  the request, the response, or a method's manifest. Both sides ignore fields they
  do not know, so a program built against an earlier release keeps loading.
- **What forces a new version.** Renaming or removing a field, making a new field
  required, or changing how the response is delivered. The Pleiades refuses a program
  built for any other protocol version rather than guessing.
- **What is not covered.** The Go API of `pkg/` itself (the packages an external
  Collection imports) is not yet under a compatibility promise. Until the first
  release, rebuild an external Collection against the release you run.

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

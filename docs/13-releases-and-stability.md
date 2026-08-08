---
status: beta
---

# Releases and stability

## Changelog

[`CHANGELOG.md`](../CHANGELOG.md) at the repository root is the real, current
record. This project has not cut a tagged release yet, so everything lives under an
`Unreleased` heading, grouped by date once a first release exists. Entries are
assembled from `changelog/*.md` fragments (see
[`changelog/README.md`](../changelog/README.md) for the naming convention and the
five change types); a generator to automate that assembly is planned but not built,
so assembly is manual today.

## Release notes

None yet. There is no tagged release to write notes for.

## Breaking changes and deprecations

None yet, for the same reason: nothing has shipped a version a later one could break
compatibility with. `CHANGELOG.md`'s own `breaking`/`deprecated`/`removed` fragment
types exist and are ready to record one the first time it happens.

## Versioning and deprecation policy

Not decided yet. `pleiades version` reports `dev` until a real release process
exists to set it at build time. No semver commitment, support window, or
deprecation notice period has been published.

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

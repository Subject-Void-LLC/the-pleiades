---
status: beta
---

# Project

## Contributing

See [`CONTRIBUTING.md`](../CONTRIBUTING.md) at the repository root: the development
workflow, the `make ci` checklist, the coverage ratchet, and how a documentation
change is expected to accompany a code change.

## Style guide

Covered in [`CONTRIBUTING.md`](../CONTRIBUTING.md#code-style) rather than
duplicated here: American English, no em-dashes, Google-style Go doc comments
explaining *why* over *what*.

## Code of conduct

See [`CODE_OF_CONDUCT.md`](../CODE_OF_CONDUCT.md).

## Security policy

See [`SECURITY.md`](../SECURITY.md) for how to report a vulnerability. Do not open
a public issue for one. [Running in production](10-running-in-production.md)'s
security section covers the current threat model and credential handling in detail.

## License, and what it means for an extension

Pleiades is licensed under GPLv3 (see [`LICENSE`](../LICENSE) at the repository
root). Every new source file must be GPLv3-compatible; an Apache 2.0 dependency is
fine, a proprietary or more restrictive one is not. Because there is no out-of-tree
extension loading mechanism today (see
[Extending Pleiades](11-extending-pleiades.md)), extending Pleiades means
contributing to this repository or maintaining a fork, and GPLv3's copyleft applies
to the whole binary either way: a distributed modified build must itself be GPLv3,
with source available to whoever receives it.

## Third-party licenses

Not generated yet. This page will list every direct and indirect Go module
dependency and its license once a scan is wired into the documentation pipeline;
until then, `go.mod` at the repository root is the authoritative dependency list.

## Where to get help

This project does not yet have a public issue tracker, chat channel, or mailing
list link to publish here. Until one exists, `CONTRIBUTING.md` and this reference
are the primary self-service resources.

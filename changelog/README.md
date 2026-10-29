# Changelog fragments

One file per pull request that changes user-facing behavior, named
`changelog/<short-slug>.<type>.md`, where `<type>` is one of:

- `breaking` - an existing behavior changed incompatibly
- `deprecated` - something still works but is on its way out
- `removed` - something that used to work no longer exists
- `added` - a new capability
- `fixed` - a bug fix
- `security` - a vulnerability fix or hardening change

Content is one or two plain sentences, present tense, no em-dashes: what changed, from
a user's point of view, not an implementation narrative. Example
(`changelog/pleiades-doc-command.added.md`):

```markdown
Added `pleiades doc <fqcn>` to print a collection method's reference offline, from the
same registry `pleiades validate` reads.
```

A pull request that changes nothing a user can see (refactoring, internal tests, CI
plumbing) needs no fragment. At a release the fragments are assembled into that
version's section of `CHANGELOG.md` and removed. A generator will eventually do this;
until it lands, assembly is manual. The development fragments from before 0.3.0 were
retired at that release, whose notes (`docs/releases/v0.3.0.md`) summarize them.

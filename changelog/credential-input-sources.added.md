Added credential input sources: an input can now be supplied by another credential
rather than stored, bound through `PUT /api/v1/credentials/{id}/input-sources` or in
the same request that creates the credential.

This is the model AWX uses, and it exists because a vault address and a vault token
are themselves credentials that need rotating, an audit trail and RBAC, which a
reference string in a column cannot give them. The older reference-string form still
works and is still the right one for a file, where the source is a property of the
deployment rather than of any one credential.

Rotating a source takes effect on the next run of every credential that reads through
it, with no edit to any of them, because a binding is resolved when a job dispatches
rather than when it was written.

A binding is refused at the moment it is written, rather than by the job that later
trips over it, when it names an input the credential's type does not declare, a source
in another organization, a source that is not an external-kind credential, or a set
that would make resolution return to the credential it started from. A chain of
sources is bounded at four hops and refused by name past that.

One limit worth knowing: the binding model ships here and the first secret manager a
binding can resolve through does not. A binding whose source nothing can build fails
with an explicit error naming it, rather than resolving to an empty secret.

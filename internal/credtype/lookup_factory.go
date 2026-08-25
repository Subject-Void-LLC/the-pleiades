// Row-backed external secret sources: the same Lookup port, configured
// from a credential row instead of from the composition root.
//
// # Why this is a factory beside the port rather than a change to it
//
// lookup.go's own doc comment records the model Phase 22 shipped and the
// heavier model AWX uses. In the shipped model a source is wired once at
// process start and selected by name out of a "<source>:<reference>"
// string, so every credential pointing at Vault points at the SAME Vault,
// configured from the Controller's environment. In AWX's model the source
// is itself a credential, carrying its own address, token and namespace,
// linked to a target credential's field by a CredentialInputSource row.
//
// The second model is the one this platform needs, because a Vault address
// and a Vault token are themselves credentials that need rotating, RBAC and
// an audit trail, and a string is none of those. Phase 22 recorded that the
// upgrade would be ADDITIVE, that a reference string could migrate into a
// row and "this port does not change shape." This file is where that claim
// is either kept or broken, so it is worth being explicit that it is kept:
//
//   - Lookup is untouched. Name and Resolve have the same signatures they
//     had, and lookup/file's implementation compiles against them unchanged.
//   - What is new is who CONSTRUCTS a Lookup. A factory turns one source
//     credential's resolved inputs into one configured Lookup.
//   - Both selection paths live in the same Lookups value, so a deployment
//     may run the string form and the row form at once. That is not a
//     transitional state to be cleaned up later: the file source is
//     genuinely deployment-wide configuration (a Kubernetes projected
//     volume is not per-credential), and a Vault genuinely is not.
//
// # What a factory may assume about its inputs
//
// Nothing beyond "these are the source credential's resolved input values,
// with the type's declared defaults already filled in." In particular a
// factory must not assume the values are non-empty or well formed: they
// came from a row an operator wrote, and validating them is the factory's
// own job, reported as an error rather than as a Lookup that fails later.

package credtype

import "sort"

// LookupFactory builds one configured Lookup from one source credential's
// resolved input values.
//
// An implementation is registered against the namespace of the credential
// TYPE it serves, rather than against a source name, because the namespace
// is what a row actually carries: a target credential's input names a
// source credential, that credential names a type, and that type has a
// stable namespace. Selecting on the type's namespace is therefore a
// lookup rather than a second naming convention to keep in sync.
type LookupFactory interface {
	// Namespace is the credential type namespace this factory builds a
	// source for, matching CredentialType.Namespace. It matches AWX's own
	// namespace for the same source wherever one exists, so an import
	// maps.
	Namespace() string

	// New returns a Lookup configured from one source credential's
	// resolved inputs.
	//
	// It returns an error rather than a Lookup that fails on first use
	// when the inputs cannot configure a usable source. The distinction
	// matters because the two are reported in different places: a
	// configuration error names the source credential and is actionable
	// by whoever wrote it, and a resolution error lands on a job record
	// belonging to whoever launched it.
	//
	// An implementation must never include an input VALUE in the error it
	// returns. The inputs are secrets by construction: a Vault token is
	// the whole point of this model existing.
	New(inputs map[string]string) (Lookup, error)

	// Reference turns one binding's per-field metadata into the reference
	// string Resolve understands.
	//
	// This is the bridge that lets the row model reach the unchanged
	// Lookup port. A string-form binding already IS a reference; a
	// row-form binding is a small map (for a Vault source: a secret path,
	// a key within it, optionally a version), and something has to know
	// that source's addressing convention to flatten it. That knowledge
	// belongs here, beside the source it describes, rather than in the
	// resolver, which would otherwise need a switch on source kind and
	// would stop being extensible by registration alone.
	//
	// It returns an error rather than a best-effort reference when the
	// metadata is missing a field the source requires, so a binding an
	// operator half-filled fails naming the missing field instead of
	// resolving against a path that happens to parse.
	Reference(metadata map[string]string) (string, error)
}

// Factory returns the factory registered for a credential type namespace.
//
// The second return distinguishes "no factory for this namespace" from a
// nil factory, which NewLookupsWith already refuses, so a caller can report
// the unbuilt source by name rather than panicking on it.
func (l *Lookups) Factory(namespace string) (LookupFactory, bool) {
	f, ok := l.byNamespace[namespace]
	return f, ok
}

// Namespaces returns every credential type namespace this set can build a
// row-backed source for.
//
// The sibling of Names, and it exists for the same reason: an API or a form
// offering the operator a choice should read the real set rather than
// restate it.
func (l *Lookups) Namespaces() []string {
	out := make([]string, 0, len(l.byNamespace))
	for ns := range l.byNamespace {
		out = append(out, ns)
	}
	sort.Strings(out)
	return out
}

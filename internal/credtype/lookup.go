package credtype

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// External secret sources: where an input's value comes from when this
// platform does not store it.
//
// # The reference format, and what it deliberately is not
//
// A credential row's external map binds an input id to a reference of the
// form "<source>:<reference>", for example "file:prod_api_token" or
// "hashivault_kv:secret/data/prod#token". The source names a Lookup; the
// rest is that Lookup's own to interpret.
//
// AWX models this differently and more heavily: an external secret is a
// second CREDENTIAL, of a type like hashivault_kv, carrying its own inputs
// (a Vault address, a token, a namespace), linked to the target credential
// field by a CredentialInputSource row that also carries per-field
// metadata. That model is the right one eventually, because a Vault address
// and a Vault token are themselves credentials that need rotating, RBAC and
// an audit trail, and a string is none of those.
//
// A single string is what Phase 22 ships, and the honest reason is that the
// heavier model needs a second binding table, a second API surface, and a
// recursion rule for "an external credential whose own token is external."
// Building it here would roughly double the phase. What the string form
// does buy is the whole just-in-time path exercised for real: the reference
// is stored, the value is not, and resolution happens at dispatch. The
// upgrade is additive, because a reference string can be migrated into a
// row and this port does not change shape.
//
// # Why the one real source is files
//
// It is how secrets actually arrive in a large fraction of real
// deployments. A Kubernetes projected volume, a Vault Agent sidecar and an
// External Secrets Operator all land a secret as a file on disk, so the
// file source is not a placeholder for a real integration, it is the
// integration those three already provide. It needs no new dependency, and
// it exercises the full resolve-at-dispatch path, which is what RULE 0
// requires of anything called verified.
//
// The alternative considered and rejected was an environment-variable
// source. A Controller environment variable is exactly what PLAN.md Section
// 17.5 says a secret should not live in, so shipping one would be building
// the thing the section forbids in order to satisfy a checklist.

// Errors this file returns.
var (
	// ErrLookupNotImplemented reports an external secret source this
	// platform names but does not implement.
	//
	// It is an explicit error rather than a silent empty value, matching
	// the module catalog's own convention for a declared-but-unbuilt FQCN.
	// An empty value would inject an empty secret, which authenticates
	// against nothing and reports the failure somewhere unrelated.
	ErrLookupNotImplemented = errors.New("credtype: this external secret source is declared but not implemented")

	// ErrLookupUnknown reports a reference naming no source at all.
	ErrLookupUnknown = errors.New("credtype: no such external secret source")

	// ErrLookupReference reports a malformed reference, or one a source
	// refuses.
	ErrLookupReference = errors.New("credtype: external secret reference is not valid")
)

// Lookup resolves one external secret reference to its value.
//
// An implementation must never include the resolved value in an error, and
// should keep the reference itself out of any error it cannot control: a
// reference is a pointer to a secret rather than a secret, but a path can
// still name a customer's environment.
type Lookup interface {
	// Name is the source name a reference selects this Lookup with. It
	// matches AWX's own credential type namespace for the same source
	// wherever one exists, so an import maps.
	Name() string

	// Resolve returns the secret the reference names.
	Resolve(ctx context.Context, reference string) (string, error)
}

// DeclaredLookups are the external secret sources this platform names and
// does not implement, under AWX's own namespaces so an import maps onto
// them rather than failing to find a source at all.
//
// Naming them is the point. A credential imported from AWX pointing at
// HashiCorp Vault resolves to an error saying exactly that, which an
// operator can act on, rather than to "no such source", which reads as a
// typo in their own data.
func DeclaredLookups() []Lookup {
	names := []string{
		"hashivault_kv",
		"hashivault_ssh",
		"aws_secretsmanager",
		"azure_kv",
		"centrify_vault",
		"conjur",
		"thycotic_dsv",
		"thycotic_tss",
	}
	out := make([]Lookup, 0, len(names))
	for _, name := range names {
		out = append(out, declaredLookup(name))
	}
	return out
}

// declaredLookup is a source this platform names and does not implement.
type declaredLookup string

// Name returns the source name.
func (d declaredLookup) Name() string { return string(d) }

// Resolve reports that the source is declared and not implemented.
func (d declaredLookup) Resolve(context.Context, string) (string, error) {
	return "", fmt.Errorf("%w: %s", ErrLookupNotImplemented, string(d))
}

// Lookups is the set of external secret sources a resolver may use.
//
// It is a plain type over a map rather than pkg/registry.Registry because
// this set is per-resolver rather than process-wide: a deployment wires the
// sources it has configured, and a test wires a fake one without disturbing
// anything else. The Target strategies are the opposite case, and use the
// Registry, because their set is fixed by PLAN.md Section 29.2 rather than
// by a deployment.
type Lookups struct {
	byName map[string]Lookup
}

// NewLookups builds a lookup set from the sources a deployment has,
// refusing two sources claiming one name.
//
// The declared-not-implemented sources are always included, and a real
// source overrides the declared one of the same name. That ordering is what
// lets a later phase ship a real hashivault_kv without touching a caller.
func NewLookups(sources ...Lookup) (*Lookups, error) {
	l := &Lookups{byName: make(map[string]Lookup)}
	for _, d := range DeclaredLookups() {
		l.byName[d.Name()] = d
	}

	seen := make(map[string]struct{}, len(sources))
	for _, s := range sources {
		if s == nil {
			return nil, fmt.Errorf("%w: a nil external secret source was wired", ErrLookupUnknown)
		}
		name := s.Name()
		if name == "" {
			return nil, fmt.Errorf("%w: an external secret source has no name", ErrLookupUnknown)
		}
		if _, dup := seen[name]; dup {
			return nil, fmt.Errorf("%w: two external secret sources are both named %q", ErrLookupUnknown, name)
		}
		seen[name] = struct{}{}
		l.byName[name] = s
	}
	return l, nil
}

// Names returns every source this set can select, sorted, including the
// declared-not-implemented ones. It exists so an API or a form can offer
// the real list rather than restating it.
func (l *Lookups) Names() []string {
	out := make([]string, 0, len(l.byName))
	for name := range l.byName {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// Resolve resolves one "<source>:<reference>" string.
//
// The error names the source and, for a malformed reference, the input it
// was bound to, and never the value: this is called at dispatch and its
// error becomes a reason on a job record.
func (l *Lookups) Resolve(ctx context.Context, inputID, reference string) (string, error) {
	source, rest, found := strings.Cut(reference, ":")
	if !found || source == "" || rest == "" {
		return "", fmt.Errorf(
			"%w: input %q names an external secret without a source, which must be written as <source>:<reference>",
			ErrLookupReference, inputID)
	}

	lookup, ok := l.byName[source]
	if !ok {
		return "", fmt.Errorf("%w: input %q names the source %q, which is not one of %v",
			ErrLookupUnknown, inputID, source, l.Names())
	}

	value, err := lookup.Resolve(ctx, rest)
	if err != nil {
		return "", fmt.Errorf("resolving input %q from %s: %w", inputID, source, err)
	}
	if value == "" {
		// An empty external secret is refused rather than injected. The
		// value reaching a run empty is how a rotation that emptied a file
		// presents as an authentication failure against the target device.
		return "", fmt.Errorf("%w: input %q resolved to an empty value from %s",
			ErrLookupReference, inputID, source)
	}
	return value, nil
}

package credtype

import (
	"fmt"
	"sort"
)

// Credential is one credential with its REAL input values, resolved and
// ready to inject.
//
// # The type this is deliberately not
//
// There are two credential types in this codebase and the difference
// between them is a security boundary rather than a modelling preference.
//
// This one carries plaintext. It is produced only by
// internal/credstore/resolve, which is a separate package for exactly that
// reason, and it is consumed only by the injector at dispatch time.
// internal/archtest fails the build if internal/api ever imports the
// package that produces it, which is what makes PLAN.md Section 29.3's "no
// plaintext read API" a structural property rather than a promise a
// reviewer has to keep noticing.
//
// credstore.Credential is the other one. It is what every read path holds:
// the same shape with every secret input replaced by a redaction marker. A
// handler holding one cannot reach a plaintext value, because the value is
// not in the object it has.
//
// A value of this type should be held for as long as the injection takes
// and no longer. It is not a cache.
type Credential struct {
	// ID identifies the credential.
	ID int

	// Name is used in an error and in an audit record, never a value.
	Name string

	// Type is the credential's own type, carried whole because injection
	// needs both halves: the input schema to know what is secret and what
	// is required, and the injector document to know where it goes.
	Type CredentialType

	// Inputs are the real values, keyed by input id, with the type's
	// declared defaults already filled in and any external lookup already
	// resolved. Whatever produces this has done that work; the injector
	// does not reach back out.
	Inputs map[string]string

	// External maps an input id to the reference it was resolved from.
	// Kept after resolution so an audit record can say where a value came
	// from without saying what it was.
	External map[string]string
}

// SecretValues returns the plaintext values of every input this
// credential's type marks secret, sorted longest first.
//
// This is what feeds the masking ruleset. Every value here is registered
// before injection so that any later log line, captured command output, or
// error containing one is scrubbed. The ordering matches what the scrub
// wants: longest first, so a password that is a prefix of a passphrase
// cannot carve the passphrase in half and leave its tail exposed.
//
// A non-secret input's value is deliberately absent. Registering it would
// scrub an ordinary value (a username, a region, a URL) out of every later
// line for the rest of the process, which corrupts output without
// protecting anything.
func (c Credential) SecretValues() []string {
	out := make([]string, 0, len(c.Inputs))
	for _, id := range c.Type.Inputs.SecretFields() {
		if v := c.Inputs[id]; v != "" {
			out = append(out, v)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if len(out[i]) != len(out[j]) {
			return len(out[i]) > len(out[j])
		}
		return out[i] < out[j]
	})
	return out
}

// Bound projects this credential into the shape the binding rule reads.
func (c Credential) Bound() Bound {
	return Bound{
		CredentialID:    c.ID,
		CredentialName:  c.Name,
		Kind:            c.Type.Kind,
		VaultIdentifier: VaultIdentifierOf(c.Type.Kind, c.Inputs),
	}
}

// RenderVars builds the variable map an injector template evaluates
// against: every input id bound to its value.
//
// The reserved filename namespace is NOT here. It is added by the injector
// after the file pass, because the paths it holds do not exist until the
// files they name have been decided. Building it here would mean either
// guessing the paths or handing templates a namespace with nothing in it.
func (c Credential) RenderVars() map[string]any {
	vars := make(map[string]any, len(c.Inputs))
	for id, v := range c.Inputs {
		vars[id] = v
	}
	return vars
}

// Validate reports whether this credential's values satisfy its own type.
//
// prompted names the inputs a launch supplied for the type's ask-at-runtime
// fields, which are legitimately absent from stored values and must not be
// reported missing.
func (c Credential) Validate() error {
	if c.Type.Name == "" {
		return fmt.Errorf("%w: credential %q carries no type", ErrInvalidCredential, c.Name)
	}
	return c.Type.Inputs.CheckValues(c.Inputs)
}

// WithDefaults returns a copy with the type's declared defaults filled in
// for every input the credential did not supply.
//
// A copy rather than a mutation, because the caller's map may be the one
// the store handed out and filling defaults into it would make a stored
// credential appear to hold values nobody set.
func (c Credential) WithDefaults() Credential {
	merged := make(map[string]string, len(c.Inputs))
	for id, v := range c.Type.Inputs.Defaults() {
		merged[id] = v
	}
	for id, v := range c.Inputs {
		if v != "" {
			merged[id] = v
		}
	}
	c.Inputs = merged
	return c
}

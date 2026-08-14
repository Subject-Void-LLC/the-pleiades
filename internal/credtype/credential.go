package credtype

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
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
// against: every DECLARED input id bound to its value, with the ones this
// credential did not supply bound to their empty value.
//
// # Why every declared id is seeded, and why that is not a hole in strict-undefined
//
// The renderer is strict-undefined: a referenced name absent from the map is
// an error rather than an empty string. Seeding looks like it gives that up.
// It does not, and the distinction is the whole reason the seeding is safe.
//
// Injectors.Validate has already proved, at the moment the type was SAVED,
// that every name every template references is a declared input or the
// reserved namespace. So by the time a template renders, an absent name
// cannot be a typo; it can only be a declared optional input that this
// particular credential left blank. Strict-undefined catches the first,
// which is the failure worth catching, and it catches it at the write.
// Failing on the second would mean a type whose optional input nobody
// filled in cannot inject at all.
//
// It is also what AWX does, which matters more than the reasoning above
// because a migrated playbook observes the result. AWX builds its injector
// namespace from the credential's truthy inputs and renders under Jinja's
// ordinary Undefined, so a blank optional input renders as the empty string
// and its environment variable is still SET, to "". A platform that instead
// refused the whole injection would fail every AWX job template whose
// machine credential uses a token rather than a password.
//
// Booleans render in Python's capitalisation, and unset ones render as
// False, for the same observable-result reason: a playbook migrated from
// AWX may compare the injected value against "True", and AWX's namespace
// holds a real Python bool. Storage is unaffected, since only the value a
// template sees is spelled this way.
//
// A private key gains a trailing newline if it lacks one, which is again
// AWX's own normalisation and is a correctness fix rather than cosmetics:
// a PEM body pasted into a form without its final newline is rejected by
// several of the readers these types generate files for.
//
// The reserved filename namespace is NOT here. It is added by the injector
// after the file pass, because the paths it holds do not exist until the
// files they name have been decided. Building it here would mean either
// guessing the paths or handing templates a namespace with nothing in it.
func (c Credential) RenderVars() map[string]any {
	schema := c.Type.Inputs
	vars := make(map[string]any, len(schema.Fields)+len(c.Inputs))

	for _, f := range schema.Fields {
		vars[f.ID] = renderValue(f, c.Inputs[f.ID])
	}
	// An input the schema does not declare cannot be referenced by any
	// validated template, but it is carried through rather than dropped so
	// that this map is a faithful view of what the credential holds.
	for id, v := range c.Inputs {
		if _, declared := schema.Field(id); !declared {
			vars[id] = v
		}
	}
	return vars
}

// renderValue spells one input's stored value the way an injector template
// should see it. See RenderVars for why each rule is AWX's rather than ours.
func renderValue(f InputField, stored string) string {
	if f.Type == InputBoolean {
		// Lenient on the way in, exact on the way out: whatever spelling
		// reached storage, a template sees Python's.
		parsed, err := strconv.ParseBool(stored)
		if err != nil || !parsed {
			return "False"
		}
		return "True"
	}
	if f.Format == FormatSSHPrivateKey && stored != "" && !strings.HasSuffix(stored, "\n") {
		return stored + "\n"
	}
	return stored
}

// Validate reports whether this credential's values satisfy its own type.
//
// It is called by the injector on a credential whose externals have already
// been resolved into Inputs, so External is passed through only to keep the
// two callers of CheckValues identical: a resolved credential's external
// ids are already present in Inputs, and an unresolved one has not reached
// injection yet.
func (c Credential) Validate() error {
	if c.Type.Name == "" {
		return fmt.Errorf("%w: credential %q carries no type", ErrInvalidCredential, c.Name)
	}
	if err := c.Type.Inputs.CheckValues(c.Inputs, c.External); err != nil {
		return err
	}
	return c.checkReady()
}

// checkReady demands a real value for every required input, with none of
// the exemptions CheckValues allows.
//
// The two checks answer different questions and the difference is the point.
// CheckValues asks whether a credential may be SAVED, and there a required
// input is legitimately absent three ways: it has a default, it reads from
// an external secret source, or it is prompted at launch. This asks whether
// a credential may be USED, and by the time it is asked all three have
// happened already: defaults are filled by WithDefaults, external references
// are resolved by internal/credstore/resolve, and prompted values were
// merged a few lines above. A required input still empty here was not
// answered by any of them.
//
// It exists because strict-undefined used to catch this by accident. An
// unanswered prompt left the name out of the render namespace entirely, so
// the renderer refused it. RenderVars now seeds every declared input, for
// the AWX-fidelity reasons its own comment gives, which is correct and
// removes that accident. Losing the check with it would mean an operator
// who skipped a required prompt gets an empty token injected and an
// authentication failure against the remote service, with nothing naming
// the input they left blank.
func (c Credential) checkReady() error {
	for _, id := range c.Type.Inputs.Required {
		if c.Inputs[id] == "" {
			return fmt.Errorf(
				"%w: input %q is required and has no value at injection: it was not stored, not defaulted, not resolved from an external source, and not supplied at launch",
				ErrInvalidCredential, id)
		}
	}
	return nil
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

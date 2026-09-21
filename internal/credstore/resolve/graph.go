// The resolution graph: filling a credential's inputs when some of them
// live somewhere else, and why the walk is bounded in two directions.
//
// # Two binding forms, one port
//
// An input whose value this platform does not store is filled from an
// external secret manager, and there are two ways a row says so.
//
// The string form is Phase 22's. Credential.external maps an input id to
// "<source>:<reference>", the source names a Lookup the composition root
// wired, and the reference is that Lookup's to interpret. Every credential
// naming a given source names the SAME one, because a string has nowhere to
// put an address or a token.
//
// The row form is Phase 78a's. A CredentialInputSource row binds the input
// to a SOURCE CREDENTIAL, an ordinary row of an external-kind type carrying
// that vault's address and token, plus the per-field metadata that says
// where in it to look. The source is built from those inputs by a
// credtype.LookupFactory registered for its type's namespace.
//
// Both end at the same unchanged credtype.Lookup, and both go through
// Lookups.ResolveThrough, so the refusals (an empty value, an error that
// must not carry the value) are written once.
//
// # Why the walk is bounded, and why the two bounds are separate errors
//
// The row form makes resolution RECURSIVE, which the string form never was.
// A source credential holds a Vault token, and that token is an input, and
// that input may itself be bound to another source. Every link is an
// ordinary row an operator may write through an ordinary API, so an
// unbounded walk is a denial of service against the Controller reachable
// from ordinary data, and it costs a network round trip per link on the
// dispatch path.
//
// So there are two refusals, and they are deliberately distinguishable:
//
//   - A CYCLE is a chain that returns to a credential already on it. It is
//     a mistake in the data and it is fixable by editing one binding.
//   - DEPTH is a chain that is legal at every link and simply too long. It
//     is a topology decision, and the operator's action is different.
//
// An undetected cycle would eventually trip the depth bound, which is
// exactly why the cycle check runs first: reporting "too deep" for two
// credentials pointing at each other sends somebody looking for a chain
// that does not exist.

package resolve

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/credential"
)

// maxSourceDepth is the most source hops one resolution may take.
//
// The credential a job binds is hop zero. Its own source is hop one, that
// source's source is hop two, and so on.
//
// Four is chosen against the topologies that can actually be named rather
// than picked for roundness. AWX allows exactly one hop: a credential's
// input source is a credential, and that source's inputs may not themselves
// be external. One hop is therefore the parity floor. Two covers a vault
// whose own token is issued by a second vault, which is an ordinary
// transit or unwrap arrangement. Four leaves room above every arrangement
// anyone here has described while keeping the worst case to four network
// round trips on the dispatch path, which is the cost that matters: this
// runs per credential per dispatch, with a device fan-out waiting behind it.
const maxSourceDepth = 4

// resolveCredential loads one credential and fills every input it does not
// store itself, following source bindings as far as the bounds allow.
//
// chain holds the credential ids already being resolved above this one,
// root first, and is what both bounds are measured against. It is a slice
// rather than a set because the error names the chain: "12 to 40 to 12" is
// something an operator can act on, where "a cycle exists" is not.
func (r *entResolver) resolveCredential(ctx context.Context, id int, chain []int) (credtype.Credential, error) {
	// Cycle before depth, per this file's own doc comment: a cycle would
	// eventually trip the depth bound too, and reporting it as depth sends
	// somebody looking for a chain that does not exist.
	for _, seen := range chain {
		if seen == id {
			return credtype.Credential{}, fmt.Errorf("%w: %s",
				credtype.ErrLookupCycle, formatChain(append(chain, id)))
		}
	}
	if len(chain) > maxSourceDepth {
		return credtype.Credential{}, fmt.Errorf("%w: %s is %d hops, and the limit is %d",
			credtype.ErrLookupDepth, formatChain(chain), len(chain)-1, maxSourceDepth)
	}

	row, err := r.client.Credential.Query().
		Where(credential.IDEQ(id)).
		WithCredentialType().
		WithInputSources(func(q *ent.CredentialInputSourceQuery) {
			q.WithSourceCredential()
		}).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			// Named by id rather than by name, because the name is not
			// available for a row that was not found, and because an id is
			// what the caller has.
			return credtype.Credential{}, fmt.Errorf("resolve: credential %d is bound but no longer exists", id)
		}
		// The error names no credential and no value: it is returned to a
		// dispatch path that records a failure reason on a job record.
		return credtype.Credential{}, fmt.Errorf("resolve: reading credentials: %w", err)
	}

	ct := row.Edges.CredentialType
	if ct == nil {
		return credtype.Credential{}, fmt.Errorf("resolve: credential %d was read without its type", id)
	}

	resolved := credtype.Credential{
		ID:   row.ID,
		Name: row.Name,
		Type: credtype.CredentialType{
			Name:        ct.Name,
			Description: ct.Description,
			Kind:        credtype.Kind(ct.Kind),
			Namespace:   ct.Namespace,
			Managed:     ct.Managed,
			Inputs:      ct.Inputs,
			Injectors:   ct.Injectors,
		},
		Inputs:   cloneStrings(row.Inputs),
		External: cloneStrings(row.External),
	}

	// Defaults are filled in here rather than at the injector, so the
	// injector receives a complete value set and never has to reach back to
	// the type to find out what a missing input should have been.
	resolved = resolved.WithDefaults()

	// Both binding forms resolve here, at dispatch, which is the
	// just-in-time point Section 17.4 requires: a job queued behind a
	// capacity limit holds a pointer rather than a secret, and a relaunch a
	// week later reads whatever the source holds now rather than what it
	// held then.
	if err := r.resolveExternal(ctx, &resolved); err != nil {
		return credtype.Credential{}, err
	}
	if err := r.resolveInputSources(ctx, &resolved, row.Edges.InputSources, append(chain, id)); err != nil {
		return credtype.Credential{}, err
	}

	return resolved, nil
}

// resolveInputSources fills every input bound to a source credential.
//
// A credential with no bindings costs one already-loaded edge slice and no
// query, which is every credential in a deployment that has configured no
// source.
func (r *entResolver) resolveInputSources(
	ctx context.Context,
	cred *credtype.Credential,
	bindings []*ent.CredentialInputSource,
	chain []int,
) error {
	if len(bindings) == 0 {
		return nil
	}

	// Sorted so a credential with two broken bindings reports the same one
	// every time, matching resolveExternal's own reason for sorting.
	sortBindingsByInputID(bindings)

	for _, b := range bindings {
		source := b.Edges.SourceCredential
		if source == nil {
			return fmt.Errorf("resolve: credential %d: input %q was read without its source credential",
				cred.ID, b.InputID)
		}

		// The source's own inputs may themselves be external, so it is
		// resolved through the same walk rather than read directly. This is
		// the recursion the bounds above exist for.
		//
		// It happens BEFORE the branch below, and that ordering is what makes
		// the two forms compose: a linked Password credential whose own
		// password comes out of Vault is already filled in by the time this
		// reads a field off it, and nothing in the linked branch knows that
		// happened.
		resolvedSource, err := r.resolveCredential(ctx, source.ID, chain)
		if err != nil {
			return fmt.Errorf("resolve: credential %d: input %q: %w", cred.ID, b.InputID, err)
		}

		var value string
		if resolvedSource.Type.Kind == credtype.KindExternal {
			value, err = r.resolveThroughSource(ctx, cred, b, resolvedSource)
		} else {
			value, err = resolveFromLinkedCredential(cred, b, resolvedSource)
		}
		if err != nil {
			return err
		}
		cred.Inputs[b.InputID] = value
	}
	return nil
}

// resolveThroughSource reads a bound input out of an external secret
// manager, which is what every binding did before Phase 78d.
func (r *entResolver) resolveThroughSource(
	ctx context.Context,
	cred *credtype.Credential,
	b *ent.CredentialInputSource,
	source credtype.Credential,
) (string, error) {
	if r.lookups == nil {
		return "", fmt.Errorf(
			"resolve: credential %d reads inputs from an external secret source and this controller has none configured",
			cred.ID)
	}

	factory, ok := r.lookups.Factory(source.Type.Namespace)
	if !ok {
		return "", fmt.Errorf(
			"%w: credential %d input %q reads from a %q source, which is not one of %v",
			credtype.ErrLookupUnknown, cred.ID, b.InputID,
			source.Type.Namespace, r.lookups.Namespaces())
	}

	lookup, err := factory.New(source.Inputs)
	if err != nil {
		// Names the SOURCE credential by id, because that is the row
		// whoever fixes this has to edit.
		return "", fmt.Errorf("resolve: credential %d input %q: source credential %d is not usable: %w",
			cred.ID, b.InputID, source.ID, err)
	}

	reference, err := factory.Reference(b.Metadata)
	if err != nil {
		return "", fmt.Errorf("resolve: credential %d input %q: %w", cred.ID, b.InputID, err)
	}

	value, err := r.lookups.ResolveThrough(ctx, b.InputID, lookup, reference)
	if err != nil {
		return "", fmt.Errorf("resolve: credential %d: %w", cred.ID, err)
	}
	return value, nil
}

// resolveFromLinkedCredential reads a bound input out of another
// credential's own field, with no network in the path at all.
//
// This is what PLAN.md Section 17.4 means by linking a standard Password
// credential to a certificate bundle: the passphrase is not stored twice
// and not fetched from anywhere, it is read from the credential that
// already holds it at the moment of use.
//
// It is deliberately NOT a second mechanism. The entity, the recursion, the
// depth bound, the cycle check and the ordering are all the ones Phase 78a
// built; the only thing that differs is where the value comes from once the
// source has been resolved, which is why this is one branch rather than a
// parallel path.
//
// No LookupFactory is involved and none is required, which is the whole
// correction: until Phase 78d every binding demanded a registered factory
// for the source's namespace, so naming an ordinary Password credential as
// a source failed with an error about an unknown secret source.
func resolveFromLinkedCredential(
	cred *credtype.Credential,
	b *ent.CredentialInputSource,
	source credtype.Credential,
) (string, error) {
	field := b.Metadata[credtype.SourceFieldMetadataKey]
	if field == "" {
		return "", fmt.Errorf(
			"%w: credential %d input %q reads from credential %d, which is an ordinary credential rather than a "+
				"secret source, so the binding has to name which of its fields to read in its %q metadata",
			credtype.ErrLookupReference, cred.ID, b.InputID, source.ID, credtype.SourceFieldMetadataKey)
	}

	// Checked against the type rather than only against the values, so a
	// field that is declared but empty is reported differently from one that
	// was never part of the source's type at all. Those are different
	// mistakes: the first is an unfilled credential, the second is a typo.
	sourceField, declared := source.Type.Inputs.Field(field)
	if !declared {
		return "", fmt.Errorf(
			"%w: credential %d input %q reads field %q of credential %d, whose type %q declares no such input",
			credtype.ErrLookupReference, cred.ID, b.InputID, field, source.ID, source.Type.Namespace)
	}

	// A secret may only be read into an input that is itself secret.
	//
	// This is a containment rule rather than a tidiness one, and it replaces
	// something the old external-kind-only refusal was providing by accident.
	// The masking ruleset is built from the TARGET credential type's own
	// secret fields (credtype.Credential.SecretValues), so a value read out
	// of a secret field and written into an input its type does not mark
	// secret is never registered with the redactor. It would then render into
	// an extra var, an environment variable or a generated file in cleartext,
	// and appear unmasked in captured output, while the credential it came
	// from is masked everywhere else. That turns a binding into a way of
	// reading any same-organization secret out through a type the reader
	// controls.
	if sourceField.Secret && !targetSecret(cred, b.InputID) {
		return "", fmt.Errorf(
			"%w: credential %d input %q is not declared secret, and field %q of credential %d is, so reading it "+
				"would move a secret into a value nothing masks: declare the input secret, or bind a field that is not",
			credtype.ErrLookupReference, cred.ID, b.InputID, field, source.ID)
	}

	value := source.Inputs[field]
	if value == "" {
		return "", fmt.Errorf(
			"%w: credential %d input %q reads field %q of credential %d, and that field is empty",
			credtype.ErrLookupReference, cred.ID, b.InputID, field, source.ID)
	}
	return value, nil
}

// targetSecret reports whether the bound input is declared secret on the
// credential being filled in.
//
// An input the type does not declare at all counts as not secret, which is
// the safe direction: the store refuses such a binding at write time, so
// reaching here means a row written behind it, and the conservative answer
// is the one that refuses.
func targetSecret(cred *credtype.Credential, inputID string) bool {
	field, ok := cred.Type.Inputs.Field(inputID)
	return ok && field.Secret
}

// formatChain renders a resolution chain for an error message.
//
// Ids rather than names, for the reason every other error in this package
// gives: this text reaches a job record, and a credential name is chosen by
// an operator and can carry anything they typed.
func formatChain(chain []int) string {
	parts := make([]string, 0, len(chain))
	for _, id := range chain {
		parts = append(parts, "credential "+strconv.Itoa(id))
	}
	return strings.Join(parts, " -> ")
}

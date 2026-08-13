// Package resolve is the only way to obtain a credential's real input
// values.
//
// # Why this is its own package
//
// It is one interface and one implementation, and both could sit perfectly
// comfortably in internal/credstore. Putting them here is the entire
// security control.
//
// PLAN.md Section 29.3 requires that secret fields read back as a redaction
// marker, and Section 17.6 that there be no plaintext read API. The usual
// way to satisfy that is a convention: handlers redact, and reviewers
// notice when one forgets. Conventions hold until the day somebody adds a
// handler in a hurry, and this particular failure is silent and permanent,
// because a secret that reached an HTTP response has reached a log, a
// proxy, a browser history and a screenshot.
//
// So the convention is a package boundary. credstore.Store returns a
// projection with no field a plaintext secret could occupy. This package
// returns the real thing, and internal/archtest fails the build if
// internal/api imports it. A handler cannot leak a secret by forgetting
// something; it can only leak one by importing a package the build refuses.
//
// # Who may import this
//
// internal/dispatch, at fan-out, immediately before injection. That is the
// just-in-time point PLAN.md Section 17.4 requires: secrets are resolved
// when a job dispatches rather than when it was queued, so a job waiting
// behind a capacity limit holds no secret, and a relaunch a week later
// picks up a credential that has since been rotated.
//
// Nothing else. If a second consumer appears, it needs a reason written
// down here beside this paragraph, not just an import line.
package resolve

import (
	"context"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/credential"
)

// Resolver returns credentials with their real input values.
//
// It is deliberately a different interface, in a different package, from
// credstore.Store, and the difference is the point: a caller holding a
// Store cannot reach a plaintext value because the method does not exist on
// the type it holds.
type Resolver interface {
	// Resolve returns the named credentials with real values, type
	// defaults filled in.
	//
	// It returns them in the order the ids were given, because the caller
	// binds them to a template whose binding order is meaningful for vault
	// credentials, and re-sorting here would silently reorder the
	// --vault-id arguments a playbook receives.
	//
	// A missing id is an error rather than a silent omission. Injecting
	// three of four bound credentials produces a run that authenticates
	// partially, which fails somewhere unrelated and much later.
	Resolve(ctx context.Context, ids []int) ([]credtype.Credential, error)
}

// entResolver is the ent-backed Resolver.
//
// Rows arrive decrypted because the composition root registers
// crypto.CredentialInputsInterceptor on the client. This package performs
// no decryption of its own; it is the seam that decides WHO may see the
// result, not the mechanism that produces it.
type entResolver struct {
	client *ent.Client
}

// NewEntResolver returns a Resolver over an ent client.
//
// It panics on a nil client, following internal/launch's own NewEntStore
// precedent: the client is supplied by a composition root, so a nil one is
// a wiring error that must fail at process start rather than at the first
// dispatch.
func NewEntResolver(client *ent.Client) Resolver {
	if client == nil {
		panic("resolve: NewEntResolver requires an ent client")
	}
	return &entResolver{client: client}
}

// Resolve returns the named credentials with real values.
func (r *entResolver) Resolve(ctx context.Context, ids []int) ([]credtype.Credential, error) {
	if len(ids) == 0 {
		return nil, nil
	}

	rows, err := r.client.Credential.Query().
		Where(credential.IDIn(ids...)).
		WithCredentialType().
		All(ctx)
	if err != nil {
		// The error names no credential and no value: it is returned to a
		// dispatch path that records a failure reason on a job record.
		return nil, fmt.Errorf("resolve: reading credentials: %w", err)
	}

	byID := make(map[int]*ent.Credential, len(rows))
	for _, row := range rows {
		byID[row.ID] = row
	}

	out := make([]credtype.Credential, 0, len(ids))
	for _, id := range ids {
		row, ok := byID[id]
		if !ok {
			// Named by id rather than by name, because the name is not
			// available for a row that was not found, and because an id is
			// what the caller has.
			return nil, fmt.Errorf("resolve: credential %d is bound but no longer exists", id)
		}
		ct := row.Edges.CredentialType
		if ct == nil {
			return nil, fmt.Errorf("resolve: credential %d was read without its type", id)
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
		// injector receives a complete value set and never has to reach
		// back to the type to find out what a missing input should have
		// been.
		out = append(out, resolved.WithDefaults())
	}

	return out, nil
}

// cloneStrings copies a map so a caller cannot mutate what ent holds.
//
// It matters more here than in an ordinary accessor: the injector fills
// prompted values and resolved external lookups into this map, and doing
// that to ent's own cached entity would leave a plaintext secret attached
// to a live object for as long as that entity is referenced.
func cloneStrings(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

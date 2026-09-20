// This file pairs the two things a write path has to ask about a launchable:
// what it is, and whether launching it would be refused.
//
// They come from different places (a table, and the type's own launcher), and
// every consumer needs both together: a schedule's store cannot accept a
// target it cannot read, nor one whose launch could only ever fail. Pairing
// them here rather than in each consumer keeps a consumer's dependency to one
// value and keeps the order of the checks in one place.
package launchable

import "context"

// Admission answers both questions a write path asks about a launchable.
type Admission struct {
	// Store reads the launchable itself.
	Store Store

	// Router preflights it. Nil is honest and means no preflight, which is
	// what a caller that has no launchers composed (a read-only surface) has.
	Router *Router
}

// Get returns one launchable by id.
func (a Admission) Get(ctx context.Context, id int) (Target, error) {
	if a.Store == nil {
		return Target{}, ErrNotFound
	}
	return a.Store.Get(ctx, id)
}

// Preflight reports whether launching this would be refused, and answers
// "nothing to object to" when no router is composed.
func (a Admission) Preflight(ctx context.Context, req Request) error {
	if a.Router == nil {
		return nil
	}
	return a.Router.Preflight(ctx, req)
}

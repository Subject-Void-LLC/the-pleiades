// Package storage holds the Unit of Work port and its ent-backed adapter:
// the seam that groups a sequence of writes into one atomic transaction,
// so a caller with more than one write to make (a device update plus its
// audit-trail revisions, for instance) never has to hand-roll its own
// begin/commit/rollback bookkeeping.
package storage

import "context"

// UnitOfWork groups a sequence of writes into one atomic transaction: fn
// either observes every one of its own writes committed together, or none
// of them, never a partial subset.
type UnitOfWork interface {
	// WithTx runs fn inside a new transaction. If fn returns a non-nil
	// error, the transaction is rolled back and that error (augmented
	// with any rollback failure) is returned. If fn panics, the
	// transaction is rolled back and the panic is re-raised after
	// rollback, so a panic can never leave a half-committed transaction
	// behind for a caller further up the stack to trip over. If fn
	// returns nil, the transaction commits.
	//
	// fn receives a context carrying the transaction-scoped client. A
	// caller reaching a repository or query function from inside fn must
	// pass this context through (not the one WithTx itself was called
	// with) so that code runs against the same transaction rather than
	// escaping it.
	WithTx(ctx context.Context, fn func(ctx context.Context) error) error
}

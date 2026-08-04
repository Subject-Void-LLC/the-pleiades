package storage

import (
	"context"
	"fmt"

	"github.com/SubjectVoidLLC/the-pleiades/internal/ent"
)

// entUnitOfWork implements UnitOfWork on top of *ent.Client's own
// transaction support, following ent's own documented WithTx idiom
// (entgo.io/ent's transactions.md): begin a Tx, recover-and-rollback on
// panic, roll back on error, commit on success.
type entUnitOfWork struct {
	client *ent.Client
}

// NewEntUnitOfWork returns a UnitOfWork backed by client.
func NewEntUnitOfWork(client *ent.Client) UnitOfWork {
	return &entUnitOfWork{client: client}
}

// WithTx implements UnitOfWork.
//
// Coverage note: ent_unitofwork_test.go proves the transaction-start
// failure, rollback-on-error, rollback-on-panic, and commit-on-success
// paths against a real database, per this project's own RULE 0 (no
// mocks for the behavior under test). Two branches stay uncovered: a
// real Tx.Rollback failure while already handling fn's own error, and a
// real Tx.Commit failure after fn returned nil. Both require the
// underlying connection to fail at that exact, narrow window without
// otherwise disrupting the test (closing the pool mid-transaction risks
// database/sql.DB.Close blocking on the still-open transaction instead
// of failing it), so they are documented here rather than forced with a
// fragile or mocked reproduction.
func (u *entUnitOfWork) WithTx(ctx context.Context, fn func(ctx context.Context) error) error {
	tx, err := u.client.Tx(ctx)
	if err != nil {
		return fmt.Errorf("storage: starting transaction: %w", err)
	}

	// txCtx carries the transaction-scoped client (tx.Client(), which
	// issues every query inside this transaction rather than
	// auto-committing each one) via ent.NewContext, ent's own documented
	// mechanism for this. Any code reachable from fn that itself calls
	// ent.FromContext(ctx), rather than holding a client reference handed
	// to it directly, transparently participates in this same
	// transaction instead of the top-level client.
	txCtx := ent.NewContext(ctx, tx.Client())

	// A panic must roll back before propagating, or the transaction is
	// left open (and, depending on the driver, the connection pinned)
	// for as long as nothing downstream recovers it.
	defer func() {
		if v := recover(); v != nil {
			_ = tx.Rollback()
			panic(v)
		}
	}()

	if err := fn(txCtx); err != nil {
		if rerr := tx.Rollback(); rerr != nil {
			return fmt.Errorf("%w (rolling back transaction: %v)", err, rerr)
		}
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("storage: committing transaction: %w", err)
	}
	return nil
}

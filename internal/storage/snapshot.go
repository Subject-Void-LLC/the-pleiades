// This file holds ReadSnapshot, the read-only counterpart of the unit of
// work: one transaction for a read whose statements must agree.
package storage

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
)

// ReadSnapshot runs read inside one read-only transaction, so every
// statement read makes sees the database as it stood at one moment.
//
// It exists for a read that judges one row by another. ent eager-loads an
// edge in a statement of its own, after the row's, and outside a transaction
// each statement sees whatever was committed when it ran: under PostgreSQL's
// read committed, and under SQLite in WAL mode, where each statement outside
// a transaction takes its own snapshot. A credential redacted by its type's
// schema is the case that matters (internal/credstore's type_inputs.go): a
// type edit committed between the two statements pairs the values stored
// under one schema with another.
//
// The transaction asks for repeatable read, which on PostgreSQL takes one
// snapshot at the first statement and holds it to the end. SQLite's driver
// (mattn/go-sqlite3) ignores the isolation level and the read-only flag and
// begins a deferred transaction, which in WAL mode reads from one snapshot,
// taken at its first read, until it ends, and does not block a writer.
//
// read receives a context carrying the transaction and its client, and the
// same client as db, and must make every query through one of them. When ctx
// already carries a transaction (ent.TxFromContext: WithTx's, or an outer
// ReadSnapshot's), read runs inside that one instead: a transaction cannot be
// nested, and a write's own reads are already ordered by the locks it takes.
// A client in ctx without a transaction is not one, so it gets a snapshot of
// its own. The transaction is always rolled back, since read writes nothing.
func ReadSnapshot(ctx context.Context, client *ent.Client, read func(ctx context.Context, db *ent.Client) error) error {
	if tx := ent.TxFromContext(ctx); tx != nil {
		return read(ctx, tx.Client())
	}
	tx, err := client.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return fmt.Errorf("storage: starting a read transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	db := tx.Client()
	return read(ent.NewTxContext(ent.NewContext(ctx, db), tx), db)
}

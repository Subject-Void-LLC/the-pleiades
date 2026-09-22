// This file applies one migration: its own connection, its own transaction,
// the version recorded first, the script, and the checks that must pass
// before it may commit.
package migrate

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"time"
)

// postgresPrelude is run inside every PostgreSQL migration transaction, after
// the migration's version is recorded and before its script. Each setting is
// LOCAL, so it ends with the transaction and never reaches the pool.
//
// The order is the point. Recording the version is the one wait that must be
// unbounded: it is where a second controller waits for the first to finish
// the same migration. Everything after it is bounded, because from there on a
// wait is on somebody else entirely.
//
// Unbounded has to be said, not inherited. The claim used to run before any
// of these settings, under whatever the session carried, and a managed
// PostgreSQL role often carries a statement or lock timeout: a second starter
// then gave up its wait on the first one's uncommitted version row, failed
// to start, and crash-looped until the first committed (FAILURE_PATTERNS.md
// #288). So postgresClaimPrelude runs first and unbounds the claim, and
// postgresPrelude, after the claim, bounds what follows.
var postgresClaimPrelude = []string{
	// A controller cut off from the database mid-migration would otherwise
	// hold its transaction, and with it every other starter's wait, open
	// until TCP keepalive noticed, which can be hours. The server ends a
	// session that sits idle inside a transaction this long, which rolls the
	// migration back and releases the others (FAILURE_PATTERNS.md #134's
	// "one bad holder blocks everybody", closed by the database rather than
	// by a lease this package would have to manage). It is set before the
	// claim, so a winner cut off between its claim and anything after it is
	// covered too; a starter waiting on a claim is not idle, so this never
	// ends a wait.
	`SET LOCAL idle_in_transaction_session_timeout = '60s'`,

	// The claim's own wait is on the first starter finishing the very same
	// migration, however long that takes. Neither a role's lock timeout nor
	// its statement timeout may end it.
	`SET LOCAL lock_timeout = 0`,

	// A role's own statement timeout, which is a sensible limit for the
	// application's queries, must not cut a long migration off halfway
	// through either. It would roll back and start again on every restart,
	// and never finish. It stays off for the whole transaction.
	`SET LOCAL statement_timeout = 0`,
}

// postgresPrelude is run after the version is recorded and before the script.
var postgresPrelude = []string{
	// A migration that has to wait for a table lock queues every query behind
	// it, including the reads of older controllers still serving during a
	// rolling upgrade, so a wait that never ends is an outage. Bounded, the
	// migration fails, the controller exits, and its orchestrator's restart
	// is the retry; this package keeps no retry loop of its own.
	`SET LOCAL lock_timeout = '10s'`,
}

// migrationBusyTimeout is how long a SQLite migration waits for another
// writer, in milliseconds.
//
// The ordinary connection waits five seconds (internal/ent/open_sqlite.go),
// and the writer a migration waits for is usually another process applying
// the very same migration, which on a large database can take far longer
// than that. A starter that gave up after five seconds would read the
// history, find its migration not yet recorded, and fail to start for no
// reason but patience. A SQLite writer lives on the same machine and its
// lock dies with its process, so there is no partitioned holder to guard
// against here, only a slow one.
const migrationBusyTimeout = 30 * 60 * 1000

// applyOne executes one migration's SQL and records it as applied in a
// single transaction, so a failure partway through a migration's own
// statements never leaves it half-applied and unrecorded.
//
// src supplies the dialect's own version-record statement (see
// migrationSource), because its placeholder spelling is the one part of this
// function that is not portable across dialects. floor is the compatibility
// floor recorded with it (compat.go). src also decides whether this dialect
// needs the foreign-key dance and the longer busy wait below.
//
// # Why one pinned connection, and why the pragmas are set out here
//
// SQLite has no ALTER TABLE ADD CONSTRAINT, so every generated migration
// that changes a table's foreign keys rebuilds the table: create a new
// one, copy the rows, DROP the old one, rename. With foreign keys
// enforced, that DROP performs an implicit DELETE FROM, which fires the
// children's ON DELETE actions: a CASCADE child silently loses its rows,
// and a NO ACTION child fails the whole migration. Every such file
// therefore opens with "PRAGMA foreign_keys = off".
//
// That pragma is a no-op inside a transaction, and the script used to run
// inside one, so it never took effect and the enforcement it was written
// to suspend stayed on (FAILURE_PATTERNS.md #266). The pragma is also
// per connection, so setting it on the pool would land on whichever
// connection answered. Hence: take one connection, set the pragma on it
// before the transaction opens, read it back to prove it took, and open
// the transaction on that same connection. The busy timeout is per
// connection too, and is raised and restored the same way.
//
// script may hold many statements. Both supported drivers execute a
// multi-statement script in a single zero-argument ExecContext: SQLite
// natively, and lib/pq through the simple query protocol, which it
// selects precisely because no arguments are bound here.
func applyOne(ctx context.Context, db *sql.DB, src migrationSource, name, script, floor string) error {
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("migrate: taking a connection for %s: %w", name, err)
	}
	// The connection is handed back to the pool on the way out, unless
	// restoring a setting failed, in which case it is discarded instead.
	discard := false
	defer func() {
		if discard {
			// Returning driver.ErrBadConn tells database/sql this
			// connection must not be reused. A connection with foreign
			// keys still off would otherwise serve application queries.
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		}
		_ = conn.Close()
	}()

	if src.waitForWriters {
		previous, err := busyTimeout(ctx, conn)
		if err != nil {
			return fmt.Errorf("migrate: reading the busy timeout for %s: %w", name, err)
		}
		if err := setBusyTimeout(ctx, conn, migrationBusyTimeout); err != nil {
			return fmt.Errorf("migrate: raising the busy timeout for %s: %w", name, err)
		}
		defer func() {
			if err := setBusyTimeout(ctx, conn, previous); err != nil {
				discard = true
			}
		}()
	}

	if src.suspendForeignKeys {
		if err := setForeignKeys(ctx, conn, false); err != nil {
			return fmt.Errorf("migrate: suspending foreign keys for %s: %w", name, err)
		}
		defer func() {
			if err := setForeignKeys(ctx, conn, true); err != nil {
				discard = true
			}
		}()
	}

	return applyOneOn(ctx, conn, src, name, script, floor)
}

// applyOneOn runs one migration's transaction on an already-prepared
// connection. It is split out so that restoring the connection's settings
// happens after the transaction has committed or rolled back, which a single
// function would express as competing defers.
//
// The version is recorded as the transaction's FIRST statement, before the
// script. That order is what makes concurrent starts safe (see the package
// comment): a second starter's identical record waits on this one's
// uncommitted primary key, and fails on it once this commits, before it has
// run any of the script or taken any lock this transaction could need. Were
// the record last, the second starter would run the script first and could
// fail on this one's DDL, or deadlock with it over a foreign key's parent
// table, and neither of those failures says who won.
func applyOneOn(ctx context.Context, conn *sql.Conn, src migrationSource, name, script, floor string) error {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("migrate: beginning transaction for %s: %w", name, err)
	}
	// Roll back on any early return. A commit sets committed to true
	// first, so this is a no-op on the success path.
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	for _, statement := range src.claimPrelude {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("migrate: preparing the claim of %s: %w", name, err)
		}
	}
	if _, err := tx.ExecContext(ctx, src.insertVersion, name, time.Now().UTC(), floor); err != nil {
		return fmt.Errorf("migrate: recording %s as applied: %w", name, err)
	}
	for _, statement := range src.prelude {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("migrate: preparing the transaction for %s: %w", name, err)
		}
	}
	if _, err := tx.ExecContext(ctx, script); err != nil {
		return fmt.Errorf("migrate: applying %s: %w", name, err)
	}
	if src.suspendForeignKeys {
		if err := checkForeignKeys(ctx, tx); err != nil {
			return fmt.Errorf("migrate: %s: %w", name, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("migrate: committing %s: %w", name, err)
	}
	committed = true
	return nil
}

// busyTimeout reads SQLite's busy timeout for one connection, in
// milliseconds.
func busyTimeout(ctx context.Context, conn *sql.Conn) (int, error) {
	var ms int
	if err := conn.QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&ms); err != nil {
		return 0, err
	}
	return ms, nil
}

// setBusyTimeout sets SQLite's busy timeout for one connection, and reads it
// back to prove it took, for the reason setForeignKeys gives.
func setBusyTimeout(ctx context.Context, conn *sql.Conn, ms int) error {
	// ms is always this package's own constant or a value just read back
	// from the same pragma, never input, and a pragma takes no parameters.
	if _, err := conn.ExecContext(ctx, fmt.Sprintf("PRAGMA busy_timeout = %d", ms)); err != nil {
		return fmt.Errorf("setting busy_timeout = %d: %w", ms, err)
	}
	got, err := busyTimeout(ctx, conn)
	if err != nil {
		return fmt.Errorf("reading busy_timeout back: %w", err)
	}
	if got != ms {
		return fmt.Errorf("busy_timeout reads %d after setting it to %d", got, ms)
	}
	return nil
}

// setForeignKeys turns SQLite's foreign-key enforcement on or off for one
// connection, and reads the setting back to prove it took.
//
// The read-back is the whole point rather than a belt-and-braces extra.
// SQLite answers this pragma with silence when it cannot honor it, which
// is exactly what hid #266 for thirty migrations: the statement
// succeeded, the setting did not change, and nothing said so.
func setForeignKeys(ctx context.Context, conn *sql.Conn, on bool) error {
	setting := "off"
	if on {
		setting = "on"
	}
	if _, err := conn.ExecContext(ctx, "PRAGMA foreign_keys = "+setting); err != nil {
		return fmt.Errorf("setting foreign_keys = %s: %w", setting, err)
	}

	var enforced bool
	if err := conn.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&enforced); err != nil {
		return fmt.Errorf("reading foreign_keys back: %w", err)
	}
	if enforced != on {
		return fmt.Errorf("foreign_keys reads %t after setting it %s", enforced, setting)
	}
	return nil
}

// checkForeignKeys refuses a migration that left a row pointing at
// nothing.
//
// Suspending enforcement is what lets a table be rebuilt at all, and the
// price is that a mistake in the rebuild (a copy that drops rows, a
// backfill that misses a join) writes a database no later statement would
// have accepted. SQLite's own foreign_key_check reports every such row,
// so the migration is refused and rolled back while the tree that
// produced it is still in front of somebody, rather than failing months
// later on an unrelated insert.
func checkForeignKeys(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return fmt.Errorf("checking foreign keys: %w", err)
	}
	defer rows.Close()

	violations := 0
	first := ""
	for rows.Next() {
		// The columns are the child table, the offending rowid (null for a
		// WITHOUT ROWID table), the parent table, and the index of the
		// foreign key within the child. Only the table names are worth
		// reporting: a rowid means nothing to whoever reads the failure.
		var child, parent sql.NullString
		var rowID sql.NullInt64
		var fkID sql.NullInt64
		if err := rows.Scan(&child, &rowID, &parent, &fkID); err != nil {
			return fmt.Errorf("scanning a foreign-key violation: %w", err)
		}
		violations++
		if first == "" {
			first = fmt.Sprintf("%s -> %s", child.String, parent.String)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterating foreign-key violations: %w", err)
	}
	if violations > 0 {
		return fmt.Errorf("left %d row(s) referencing nothing (first: %s); refusing the migration", violations, first)
	}
	return nil
}

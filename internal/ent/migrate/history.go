// This file owns the schema_migrations table itself: creating it, proving it
// still has the shape the concurrency guarantee depends on, adding the
// compatibility floor column to a table created before it existed, and
// reading every row back.
//
// Every write here is written to be raced. Any number of controllers, admin
// commands and restores may run these functions against one database at the
// same instant, and none of them takes a lock: each tries its write, and when
// the write fails, reads again to learn whether somebody else already made the
// change, the same reasoning internal/tlscert's Ensure applies to a
// certificate directory.
package migrate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// createVersionTable is the statement that creates schema_migrations.
//
// Its text must never change. A database created before the compatibility
// floor existed and one created today have to end up with the same table,
// because restore compares a restored database's full shape with a fresh
// one's and refuses any difference. So the floor column is added afterwards,
// by ensureFloorColumn, on both paths alike, rather than written in here.
const createVersionTable = `CREATE TABLE IF NOT EXISTS schema_migrations (
		version TEXT PRIMARY KEY,
		applied_at TIMESTAMP NOT NULL
	)`

// ensureVersionTable creates the migration-tracking table if this is the
// first time any migration runner has touched db. It is intentionally a
// separate table from anything ent generates, so it can never collide with a
// domain schema change.
//
// CREATE TABLE IF NOT EXISTS is not atomic against a concurrent CREATE on
// PostgreSQL: two sessions that both find the name free can both try to
// create it, and one fails with a unique violation on a system catalog. That
// failure means the table now exists, which is what this function was asked
// for, so a failed create is followed by a read, and only a table that still
// does not exist is an error.
func ensureVersionTable(ctx context.Context, db *sql.DB, src migrationSource) error {
	_, createErr := db.ExecContext(ctx, createVersionTable)
	if createErr == nil {
		return nil
	}
	exists, err := versionTableExists(ctx, db, src)
	if err == nil && exists {
		return nil
	}
	return fmt.Errorf("migrate: creating schema_migrations table: %w", createErr)
}

// versionTableExists reports whether schema_migrations exists, asking the
// dialect's own catalog.
//
// It asks rather than trying a read and treating a failure as "not there",
// because a read can fail for reasons that have nothing to do with the table:
// a severed connection answered as "no history" would make a database full of
// data look new, and the plan built on that answer would skip the backup an
// upgrade takes.
func versionTableExists(ctx context.Context, db *sql.DB, src migrationSource) (bool, error) {
	var n int
	if err := db.QueryRowContext(ctx, src.historyTableCount).Scan(&n); err != nil {
		return false, fmt.Errorf("migrate: looking for schema_migrations: %w", err)
	}
	return n > 0, nil
}

// verifyVersionKey refuses a schema_migrations whose version column is not its
// whole primary key.
//
// That key is what makes concurrent starts safe: every migration records its
// version as the first statement of its own transaction, and the key is what
// makes a second starter's identical record wait for the first and then fail,
// rather than both running the migration. A table rebuilt by hand, or restored
// from somewhere that dropped its constraints, would silently lose that
// guarantee, and nothing else would notice until two controllers applied the
// same migration twice.
func verifyVersionKey(ctx context.Context, db *sql.DB, src migrationSource) error {
	rows, err := db.QueryContext(ctx, src.primaryKeyColumns)
	if err != nil {
		return fmt.Errorf("migrate: reading schema_migrations' primary key: %w", err)
	}
	defer rows.Close()

	var columns []string
	for rows.Next() {
		var column string
		if err := rows.Scan(&column); err != nil {
			return fmt.Errorf("migrate: scanning schema_migrations' primary key: %w", err)
		}
		columns = append(columns, column)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("migrate: iterating schema_migrations' primary key: %w", err)
	}
	if len(columns) != 1 || columns[0] != "version" {
		return fmt.Errorf("%w: schema_migrations' primary key is %v, not (version); without it two controllers starting together could apply one migration twice, refusing to start", errVersionKey, columns)
	}
	return nil
}

// errVersionKey marks a history table whose primary key is not its version,
// so a caller can tell that refusal from a database it could not read.
var errVersionKey = errors.New("migrate: the history table has lost its key")

// ensureFloorColumn adds the compatible_from column to a schema_migrations
// table created before the column existed.
//
// It looks before it alters, rather than issuing ALTER TABLE ... ADD COLUMN IF
// NOT EXISTS on every start, because on PostgreSQL that statement takes an
// ACCESS EXCLUSIVE lock even when it has nothing to do, and would queue every
// start behind any migration another controller holds open. The column is
// nullable, so a build that predates it keeps inserting rows without it.
//
// Two starters can both find the column missing; the second ALTER then fails
// because the first succeeded, so a failed ALTER is followed by another look.
//
// This is transitional. It exists only for history tables created before the
// floor did, and the 1.0.0 golden image, which squashes the migration history
// into one baseline, creates none; it goes with that step (and
// createVersionTable may then include the column).
func ensureFloorColumn(ctx context.Context, db *sql.DB, src migrationSource) error {
	has, err := hasFloorColumn(ctx, db, src)
	if err != nil {
		return err
	}
	if has {
		return nil
	}
	_, alterErr := db.ExecContext(ctx, `ALTER TABLE schema_migrations ADD COLUMN compatible_from TEXT`)
	if alterErr == nil {
		return nil
	}
	if has, err := hasFloorColumn(ctx, db, src); err == nil && has {
		return nil
	}
	return fmt.Errorf("migrate: adding the compatibility floor to schema_migrations: %w", alterErr)
}

// hasFloorColumn reports whether schema_migrations has its compatible_from
// column.
func hasFloorColumn(ctx context.Context, db *sql.DB, src migrationSource) (bool, error) {
	var count int
	if err := db.QueryRowContext(ctx, src.floorColumnCount).Scan(&count); err != nil {
		return false, fmt.Errorf("migrate: looking for schema_migrations' compatibility floor: %w", err)
	}
	return count == 1, nil
}

// readHistory returns every row schema_migrations holds, in the order the
// database returns them, duplicates included.
//
// It reads the compatibility floor when the column exists and treats every
// floor as NULL when it does not, so the same function can read a database
// no build of this version has touched yet, which is what the read-only plan
// does.
func readHistory(ctx context.Context, db *sql.DB, src migrationSource) ([]appliedRow, error) {
	withFloor, err := hasFloorColumn(ctx, db, src)
	if err != nil {
		return nil, err
	}
	query := `SELECT version, CAST(NULL AS TEXT) FROM schema_migrations`
	if withFloor {
		query = `SELECT version, compatible_from FROM schema_migrations`
	}

	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("migrate: reading schema_migrations: %w", err)
	}
	defer rows.Close()

	var history []appliedRow
	for rows.Next() {
		var version sql.NullString
		var row appliedRow
		if err := rows.Scan(&version, &row.floor); err != nil {
			return nil, fmt.Errorf("migrate: scanning schema_migrations row: %w", err)
		}
		// A NULL version cannot come from any migration runner; the column
		// is the primary key. It is a tampered table, and it is refused here
		// rather than turned into an empty name the gate would then have to
		// explain.
		if !version.Valid {
			return nil, errors.New("migrate: schema_migrations holds a row with no version; refusing to start")
		}
		row.version = version.String
		history = append(history, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("migrate: iterating schema_migrations: %w", err)
	}
	return history, nil
}

// prepareHistory brings schema_migrations to the shape this build needs and
// reads it: the table exists, its key is the version, it has the floor column,
// and here is every row.
func prepareHistory(ctx context.Context, db *sql.DB, src migrationSource) ([]appliedRow, error) {
	if err := ensureVersionTable(ctx, db, src); err != nil {
		return nil, err
	}
	if err := verifyVersionKey(ctx, db, src); err != nil {
		return nil, err
	}
	if err := ensureFloorColumn(ctx, db, src); err != nil {
		return nil, err
	}
	return readHistory(ctx, db, src)
}

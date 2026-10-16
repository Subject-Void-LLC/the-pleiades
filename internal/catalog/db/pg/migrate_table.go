// db.pg.migrate's version table: what it records, and how a file claims
// its version there before it runs.
package pg

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/pgclient"
)

// versionTableSQL is every statement migrate sends that names the version
// table. An identifier cannot be a bind parameter, so this is the one place
// a task's table parameter becomes SQL text: its schema and name each
// passed qualifiedName (no quote, dot or control character, at most 63
// bytes) and are quoted here (quoteIdent), and every statement is built
// once, from them, rather than at each call.
type versionTableSQL struct {
	table, create, read, claim, setDuration, finish, unclaim, claimed string
}

// newVersionTableSQL builds the statements for schema.table.
func newVersionTableSQL(schema, table string) versionTableSQL {
	t := quoteIdent(schema) + "." + quoteIdent(table)
	return versionTableSQL{
		table: t,
		create: "CREATE TABLE IF NOT EXISTS " + t + ` (
  version bigint PRIMARY KEY,
  name text NOT NULL,
  checksum text NOT NULL,
  applied_at timestamptz NOT NULL DEFAULT now(),
  applied_by text NOT NULL DEFAULT session_user,
  duration_ms bigint NOT NULL DEFAULT 0,
  finished boolean NOT NULL DEFAULT true
)`,
		read:        "SELECT version, name, checksum, finished FROM " + t,
		claim:       "INSERT INTO " + t + " (version, name, checksum, finished) VALUES ($1, $2, $3, $4)",
		setDuration: "UPDATE " + t + " SET duration_ms = $1 WHERE version = $2",
		finish:      "UPDATE " + t + " SET finished = true, duration_ms = $1 WHERE version = $2",
		unclaim:     "DELETE FROM " + t + " WHERE version = $1 AND NOT finished",
		claimed:     "SELECT checksum, finished FROM " + t + " WHERE version = $1",
	}
}

// appliedRow is one row of the version table. A row not finished is a
// no-transaction file's claim whose run has not (or never) finished.
type appliedRow struct {
	name, checksum string
	finished       bool
}

// appliedVersions reads the version table, empty when it does not exist.
func appliedVersions(ctx context.Context, c *sql.Conn, req migrateRequest) (map[int64]appliedRow, error) {
	var exists bool
	if err := c.QueryRowContext(ctx, "SELECT to_regclass($1) IS NOT NULL", req.quotedTable()).Scan(&exists); err != nil {
		return nil, err
	}
	out := map[int64]appliedRow{}
	if !exists {
		return out, nil
	}
	rows, err := c.QueryContext(ctx, req.sql.read)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", req.quotedTable(), err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var v int64
		var r appliedRow
		if err := rows.Scan(&v, &r.name, &r.checksum, &r.finished); err != nil {
			return nil, err
		}
		out[v] = r
	}
	return out, rows.Err()
}

// pendingMigrations is what is left to apply, refusing a changed applied
// file and, unless allowed, a pending version below the newest applied
// one; an applied version whose file is gone is a warning.
func pendingMigrations(files []migration, applied map[int64]appliedRow, req migrateRequest) ([]migration, []string, error) {
	var newest int64 = -1
	for v := range applied {
		newest = max(newest, v)
	}
	present := map[int64]bool{}
	var pending, early []migration
	var problems []string
	var warnings []string
	for _, m := range files {
		present[m.version] = true
		if row, ok := applied[m.version]; ok && !row.finished {
			// A claim not finished: a run applying it, or one that died.
			// Applying it again meets the claim and waits on it.
			warnings = append(warnings, fmt.Sprintf("%s is claimed by a run that has not finished", m.label()))
		} else if ok {
			if row.checksum != m.checksum {
				problems = append(problems, fmt.Sprintf("%s was applied with checksum %s and now has %s", m.label(), row.checksum, m.checksum))
			}
			continue
		}
		if req.hasTarget && m.version > req.target {
			continue
		}
		if m.version < newest {
			early = append(early, m)
		}
		pending = append(pending, m)
	}
	if len(problems) > 0 {
		return nil, nil, fmt.Errorf("applied migrations changed since they ran; restore them, or record the change with a new migration%s", listed(problems))
	}
	if len(early) > 0 && !req.outOfOrd {
		names := make([]string, len(early))
		for i, m := range early {
			names[i] = m.label()
		}
		return nil, nil, fmt.Errorf("pending migrations come before version %d, which is applied; set %s to apply them anyway%s", newest, paramAllowOutOfOrder, listed(names))
	}
	var gone []int64
	for v := range applied {
		if !present[v] {
			gone = append(gone, v)
		}
	}
	sort.Slice(gone, func(i, j int) bool { return gone[i] < gone[j] })
	for _, v := range gone {
		warnings = append(warnings, fmt.Sprintf("applied migration %d (%s) has no file in %s", v, applied[v].name, req.dir))
	}
	return pending, warnings, nil
}

// ensureVersionTable creates the version table when it is missing, as the
// task's role, so that role owns it.
func ensureVersionTable(ctx context.Context, c *sql.Conn, req migrateRequest) error {
	tx, err := c.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := asRole(ctx, tx, req.role); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, req.sql.create); err != nil {
		// CREATE TABLE IF NOT EXISTS is not safe for two sessions at once:
		// the second can meet the first's catalog row as a duplicate key
		// (or a duplicate table). Either means another run made the table.
		if code := pgclient.ErrorCode(err); code == pgclient.UniqueViolation || code == duplicateTable {
			_ = tx.Rollback()
			var exists bool
			if qerr := c.QueryRowContext(ctx, "SELECT to_regclass($1) IS NOT NULL", req.quotedTable()).Scan(&exists); qerr == nil && exists {
				return nil
			}
		}
		return fmt.Errorf("creating %s: %w", req.quotedTable(), err)
	}
	return tx.Commit()
}

// duplicateTable is the SQLSTATE of a table that already exists.
const duplicateTable = "42P07"

// errClaimed is applyMigration's answer when another run holds or has
// recorded the file's version.
var errClaimed = errors.New("another run claimed this version")

// applyMigration applies one file and records it, claiming its version
// first: the version is the table's primary key, so a second run's claim
// waits on the first's (for up to claim_wait) and then meets a duplicate
// key, which is errClaimed. A transactional file's claim, body and record
// are one transaction. A no-transaction file's claim is committed first,
// unfinished, then its one statement runs, and the claim is marked
// finished, or removed when the statement fails.
func applyMigration(ctx context.Context, c *sql.Conn, req migrateRequest, m migration) error {
	if err := requireStandardStrings(ctx, c); err != nil {
		return err
	}
	start := time.Now()
	tx, err := c.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := asRole(ctx, tx, req.role); err != nil {
		return err
	}
	if err := pgclient.SetLimits(ctx, tx, pgclient.Limits{Lock: req.claimWait}); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, req.sql.claim,
		m.version, m.name, m.checksum, !m.directives.NoTransaction); err != nil {
		if pgclient.ErrorCode(err) == pgclient.UniqueViolation {
			return errClaimed
		}
		return fmt.Errorf("claiming version %d: %w", m.version, err)
	}
	if m.directives.NoTransaction {
		if err := tx.Commit(); err != nil {
			return err
		}
		return applyOutsideTransaction(ctx, c, req, m, start)
	}
	if err := pgclient.SetLimits(ctx, tx, req.limits); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, m.text); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, req.sql.setDuration, time.Since(start).Milliseconds(), m.version); err != nil {
		return err
	}
	return tx.Commit()
}

// requireStandardStrings refuses to send a file unless the session reads
// backslashes in '...' literally (standard_conforming_strings on), as
// sqllint split and judged it. With it off, the server reads a backslash
// before a quote as an escape, so it splits
//
//	INSERT INTO n VALUES ('it\''); DROP TABLE t; --');
//
// into an INSERT and a DROP where the lint saw one INSERT. The server
// lexes a whole file before running any of it, so a file that turns the
// setting off changes only the files after it, and this check, made
// before each one, stops the run there.
func requireStandardStrings(ctx context.Context, c *sql.Conn) error {
	var v string
	if err := c.QueryRowContext(ctx, "SHOW standard_conforming_strings").Scan(&v); err != nil {
		return fmt.Errorf("reading standard_conforming_strings: %w", err)
	}
	if v != "on" {
		return fmt.Errorf("standard_conforming_strings is %s for this session, so the server would read a backslash in a string literal differently from the check of these files; turn it on for the database and role, and remove any migration that turns it off", v)
	}
	return nil
}

// applyOutsideTransaction runs a claimed no-transaction file's one
// statement with the limits and role set for the session, resets them,
// and finishes or removes the claim.
func applyOutsideTransaction(ctx context.Context, c *sql.Conn, req migrateRequest, m migration, start time.Time) error {
	set := map[string]string{
		"statement_timeout": strconv.FormatInt(req.limits.Statement.Milliseconds(), 10),
		"lock_timeout":      strconv.FormatInt(req.limits.Lock.Milliseconds(), 10),
	}
	if req.role != "" {
		set["role"] = req.role
	}
	var runErr error
	for name, value := range set {
		if _, err := c.ExecContext(ctx, "SELECT set_config($1, $2, false)", name, value); err != nil {
			runErr = fmt.Errorf("setting %s: %w", name, err)
			break
		}
	}
	if runErr == nil {
		_, runErr = c.ExecContext(ctx, m.text)
	}
	_, _ = c.ExecContext(context.WithoutCancel(ctx), "RESET ROLE; RESET statement_timeout; RESET lock_timeout")
	if runErr != nil {
		if _, err := c.ExecContext(context.WithoutCancel(ctx), req.sql.unclaim, m.version); err != nil {
			return fmt.Errorf("%w; and its claim could not be removed, so version %d stays claimed until its row is deleted: %v", runErr, m.version, err)
		}
		return runErr
	}
	if _, err := c.ExecContext(ctx, req.sql.finish, time.Since(start).Milliseconds(), m.version); err != nil {
		return fmt.Errorf("its statement ran, but its claim could not be marked finished, so version %d reads as unfinished: %w", m.version, err)
	}
	return nil
}

// awaitClaim waits, up to claim_wait, for another run's claim on m's
// version to finish, and holds what it recorded to m's checksum.
func awaitClaim(ctx context.Context, c *sql.Conn, req migrateRequest, m migration) error {
	deadline := time.Now().Add(req.claimWait)
	for {
		var checksum string
		var finished bool
		err := c.QueryRowContext(ctx, req.sql.claimed, m.version).Scan(&checksum, &finished)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			// Its claim was removed: the other run's statement failed.
			return fmt.Errorf("another run claimed version %d and its statement failed", m.version)
		case err != nil:
			return err
		case finished && checksum != m.checksum:
			return fmt.Errorf("another run applied version %d with checksum %s, and this file has %s", m.version, checksum, m.checksum)
		case finished:
			return nil
		case time.Now().After(deadline):
			return fmt.Errorf("version %d is claimed by a run that has not finished after %s; if no run is applying it, delete its row from %s", m.version, req.claimWait, req.quotedTable())
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// asRole sets the transaction's role, when the task names one.
func asRole(ctx context.Context, tx *sql.Tx, role string) error {
	if role == "" {
		return nil
	}
	if _, err := tx.ExecContext(ctx, "SELECT set_config('role', $1, true)", role); err != nil {
		return fmt.Errorf("becoming %s: %w", role, err)
	}
	return nil
}

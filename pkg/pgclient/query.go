// Package pgclient: one statement, in a transaction with timeouts, its
// rows read within bounds.
package pgclient

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/lib/pq"
)

// ErrTooManyRows is returned when a query returns more rows than its
// bound allows; no rows are returned with it.
var ErrTooManyRows = errors.New("the query returned more rows than its bound")

// ErrTooLarge is returned when a query's values add up to more bytes
// than its bound allows; no rows are returned with it.
var ErrTooLarge = errors.New("the query returned more data than its bound")

// Limits are a statement's timeouts, set for its transaction only.
type Limits struct {
	// Statement bounds the statement's own run (statement_timeout).
	Statement time.Duration
	// Lock bounds any wait for a lock (lock_timeout).
	Lock time.Duration
}

// Table is a query's result: its columns, and each row's values as text,
// nil for NULL.
type Table struct {
	Columns []string
	Rows    [][]*string
}

// QueryOptions is one statement to run and return rows from.
type QueryOptions struct {
	SQL  string
	Args []any
	// ReadOnly runs it in a read-only transaction, rolled back after.
	// Otherwise the transaction is committed when the rows are read.
	ReadOnly bool
	Limits   Limits
	// MaxRows and MaxBytes bound the result; past either the query fails
	// with ErrTooManyRows or ErrTooLarge rather than return part of it.
	MaxRows  int
	MaxBytes int
}

// Query runs a statement and returns its rows. The statement is prepared,
// which the server refuses for more than one statement, so
// "SELECT 1; DELETE FROM t" is an error and not a read then a write. Text
// that starts with COPY (after any comments) is the exception: the driver
// sends it unprepared, as a simple query, and the server runs every
// statement in it. A caller that must hold text to one statement splits
// it first (pkg/sqllint) and refuses more, as db.pg's query does.
func (s *Session) Query(ctx context.Context, o QueryOptions) (*Table, error) {
	if o.MaxRows < 1 || o.MaxBytes < 1 {
		return nil, errors.New("pgclient: a query needs a row and a byte bound")
	}
	tx, err := s.begin(ctx, o.ReadOnly, false, o.Limits)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	stmt, err := tx.PrepareContext(ctx, o.SQL)
	if err != nil {
		return nil, err
	}
	defer func() { _ = stmt.Close() }()
	rows, err := stmt.QueryContext(ctx, o.Args...)
	if err != nil {
		return nil, err
	}
	table, err := read(rows, o.MaxRows, o.MaxBytes)
	if err != nil {
		return nil, err
	}
	if !o.ReadOnly {
		if err := tx.Commit(); err != nil {
			return nil, err
		}
	}
	return table, nil
}

// Exec runs a statement that returns no rows, in a transaction it
// commits, and reports how many rows it changed. The statement is
// prepared, as Query's is, so the server refuses more than one, with the
// same exception: text starting with COPY is sent as a simple query, every
// statement in it run.
func (s *Session) Exec(ctx context.Context, sqlText string, args []any, limits Limits) (int64, error) {
	tx, err := s.begin(ctx, false, false, limits)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	stmt, err := tx.PrepareContext(ctx, sqlText)
	if err != nil {
		return 0, err
	}
	defer func() { _ = stmt.Close() }()
	res, err := stmt.ExecContext(ctx, args...)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	return n, tx.Commit()
}

// Snapshot runs fn in a read-only transaction at REPEATABLE READ, so
// everything fn reads is one moment of the database, and rolls it back.
func (s *Session) Snapshot(ctx context.Context, limits Limits, fn func(*sql.Tx) error) error {
	tx, err := s.begin(ctx, true, true, limits)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	return fn(tx)
}

// Begin opens a read-write transaction with limits set for it, for a
// caller that runs several statements in one (a migration file and its
// version row). The caller commits or rolls it back.
func (s *Session) Begin(ctx context.Context, limits Limits) (*sql.Tx, error) {
	return s.begin(ctx, false, false, limits)
}

// begin opens a transaction and sets its limits for it alone.
func (s *Session) begin(ctx context.Context, readOnly, repeatable bool, limits Limits) (*sql.Tx, error) {
	opts := &sql.TxOptions{ReadOnly: readOnly}
	if repeatable {
		opts.Isolation = sql.LevelRepeatableRead
	}
	tx, err := s.db.BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	if err := SetLimits(ctx, tx, limits); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	return tx, nil
}

// SetLimits sets limits for the rest of tx, as parameters rather than
// text, so a duration can never become SQL.
func SetLimits(ctx context.Context, tx *sql.Tx, limits Limits) error {
	for name, d := range map[string]time.Duration{"statement_timeout": limits.Statement, "lock_timeout": limits.Lock} {
		if d <= 0 {
			continue
		}
		if _, err := tx.ExecContext(ctx, "SELECT set_config($1, $2, true)", name, strconv.FormatInt(d.Milliseconds(), 10)); err != nil {
			return fmt.Errorf("setting %s: %w", name, err)
		}
	}
	return nil
}

// read reads rows within the bounds.
func read(rows *sql.Rows, maxRows, maxBytes int) (*Table, error) {
	defer func() { _ = rows.Close() }()
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	table := &Table{Columns: cols}
	size := 0
	values := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range values {
		ptrs[i] = &values[i]
	}
	for rows.Next() {
		if len(table.Rows) == maxRows {
			return nil, fmt.Errorf("%w (%d)", ErrTooManyRows, maxRows)
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		row := make([]*string, len(cols))
		for i, v := range values {
			if v == nil {
				continue
			}
			text := Text(v)
			size += len(text)
			row[i] = &text
		}
		if size > maxBytes {
			return nil, fmt.Errorf("%w (%d bytes)", ErrTooLarge, maxBytes)
		}
		table.Rows = append(table.Rows, row)
	}
	return table, rows.Err()
}

// Text is a value as the driver returned it, written as text: bytes that
// are UTF-8 as they are and others as PostgreSQL writes bytea (\x and
// hex), a time in ISO 8601 with its offset, and anything else as Go
// formats it.
func Text(v any) string {
	switch v := v.(type) {
	case string:
		return v
	case []byte:
		if utf8.Valid(v) {
			return string(v)
		}
		return `\x` + hex.EncodeToString(v)
	case time.Time:
		return v.Format("2006-01-02 15:04:05.999999Z07:00")
	case bool:
		return strconv.FormatBool(v)
	case int64:
		return strconv.FormatInt(v, 10)
	case float64:
		return strconv.FormatFloat(v, 'g', -1, 64)
	default:
		return fmt.Sprint(v)
	}
}

// CSV writes the table as RFC 4180 CSV with a header row. A NULL is an
// empty field and an empty string is "", so the two stay apart.
func (t *Table) CSV() string {
	var b strings.Builder
	writeRow := func(fields []*string) {
		for i, f := range fields {
			if i > 0 {
				b.WriteByte(',')
			}
			if f == nil {
				continue
			}
			if *f == "" || strings.ContainsAny(*f, ",\"\r\n") {
				b.WriteByte('"')
				b.WriteString(strings.ReplaceAll(*f, `"`, `""`))
				b.WriteByte('"')
				continue
			}
			b.WriteString(*f)
		}
		b.WriteString("\r\n")
	}
	header := make([]*string, len(t.Columns))
	for i := range t.Columns {
		header[i] = &t.Columns[i]
	}
	writeRow(header)
	for _, r := range t.Rows {
		writeRow(r)
	}
	return b.String()
}

// UniqueViolation is the SQLSTATE of a duplicate key.
const UniqueViolation = "23505"

// InvalidCatalogName is the SQLSTATE a server answers a connection to a
// database that does not exist with, once the login is accepted.
const InvalidCatalogName = "3D000"

// ErrorCode is the SQLSTATE the server answered err with, or "" when err
// is not the server's.
func ErrorCode(err error) string {
	var pqErr *pq.Error
	if errors.As(err, &pqErr) {
		return string(pqErr.Code)
	}
	return ""
}

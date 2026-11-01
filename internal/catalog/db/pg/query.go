// db.pg.query: one statement, read-only unless the task says otherwise,
// its rows returned as CSV within bounds.
package pg

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/pgclient"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sqllint"
)

// query's own parameters and returns.
const (
	paramSQL                 = "sql"
	paramArgs                = "args"
	paramWrite               = "write"
	paramMaxRows             = "max_rows"
	paramAllowPrivilegedRole = "allow_privileged_role"

	statColumns      = "columns"
	statRowCount     = "row_count"
	statCSV          = "csv"
	statRowsAffected = "rows_affected"
)

// query's bounds.
const (
	defaultQueryTimeout = 30 * time.Second
	maxQueryTimeout     = time.Hour
	defaultLockTimeout  = 5 * time.Second
	maxLockTimeout      = 10 * time.Minute
	defaultMaxRows      = 1000
	maxMaxRows          = 100000
	maxCSVBytes         = 1 << 20
	maxSQLBytes         = 1 << 20
)

// privilegedRoles are the built-in roles whose members can act outside
// the database from inside a read-only transaction.
var privilegedRoles = []string{"pg_execute_server_program", "pg_read_server_files", "pg_write_server_files"}

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: fqcnQuery,
		Manifest: collection.Manifest{
			SupportedTransports:  []string{"ssh"},
			RequiredCapabilities: []capability.Name{capability.NameSSHTransport},
			ExecutionContext:     collection.ExecutionContext{Site: collection.SiteTarget, Device: collection.DeviceRequired},
			EngineVersion:        ">=0.2.0",
			Status:               collection.StatusImplemented,
			NoBecomeReason:       noBecomeForward,
			SupportsCheck:        true,
			Reversibility: collection.Reversibility{
				Reversible: false,
				Notes:      "With write it runs arbitrary SQL, whose effect the method cannot invert; write the reverse as a rollback: list. Without write it changes nothing.",
			},
			Doc: queryDoc(),
		},
		Invoke:    Query,
		Check:     CheckQuery,
		CheckCall: queryCheckCall,
	})
}

// queryRequest is a query task's parameters, read and checked.
type queryRequest struct {
	sql          string
	args         []any
	write        bool
	returnsRows  bool
	limits       pgclient.Limits
	maxRows      int
	allowPrivRol bool
	database     string
}

// parseQuery reads and checks a query task's parameters before anything is
// contacted.
func parseQuery(params map[string]any) (queryRequest, error) {
	var r queryRequest
	var err error
	if r.sql, err = sdk.RequiredStringParam(params, paramSQL); err != nil {
		return r, err
	}
	if len(r.sql) > maxSQLBytes {
		return r, fmt.Errorf("%s is over %d bytes", paramSQL, maxSQLBytes)
	}
	stmts, err := sqllint.Split(r.sql)
	if err != nil {
		return r, fmt.Errorf("%s: %w", paramSQL, err)
	}
	if len(stmts) != 1 {
		return r, fmt.Errorf("%s holds %d statements; a query runs exactly one", paramSQL, len(stmts))
	}
	if raw, ok := params[paramArgs]; ok && raw != nil {
		list, ok := raw.([]any)
		if !ok {
			return r, fmt.Errorf("%s is a list of values, the first for $1", paramArgs)
		}
		r.args = list
	}
	if r.write, err = sdk.BoolParamOr(params, paramWrite, false); err != nil {
		return r, err
	}
	r.returnsRows = !r.write || returnsRows(stmts[0])
	if r.limits.Statement, err = durationParam(params, paramTimeout, defaultQueryTimeout, maxQueryTimeout); err != nil {
		return r, err
	}
	if r.limits.Lock, err = durationParam(params, paramLockTimeout, defaultLockTimeout, maxLockTimeout); err != nil {
		return r, err
	}
	if r.maxRows, err = intParam(params, paramMaxRows, defaultMaxRows, maxMaxRows); err != nil {
		return r, err
	}
	if r.allowPrivRol, err = sdk.BoolParamOr(params, paramAllowPrivilegedRole, false); err != nil {
		return r, err
	}
	if r.database, err = databaseName(params, paramDatabase); err != nil {
		return r, err
	}
	return r, nil
}

// returnsRows reports whether a write statement returns rows: a data
// change without RETURNING does not, and is run for its count instead.
func returnsRows(s sqllint.Statement) bool {
	w := s.Words()
	switch w[0] {
	case "INSERT", "UPDATE", "DELETE", "MERGE":
		for _, word := range w {
			if word == "RETURNING" {
				return true
			}
		}
		return false
	}
	return true
}

// queryCheckCall refuses check_mode for a write: running it and rolling
// it back is not changing nothing (sequences advance, locks are taken,
// triggers and dblink act outside the transaction).
func queryCheckCall(params map[string]any) error {
	if write, _ := params[paramWrite].(bool); write {
		return collection.CannotCheck("a write is known only by running it, and running it then rolling it back still advances sequences, takes locks and lets triggers act outside the transaction")
	}
	return nil
}

// Query implements db.pg.query.
func Query(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return query(ctx, rc, device, params)
}

// CheckQuery is db.pg.query's check: a read is the task itself, and a
// write is unchecked.
func CheckQuery(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	if err := queryCheckCall(params); err != nil {
		return collection.Result{}, err
	}
	return query(ctx, rc, device, params)
}

// query is Query's body.
func query(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	req, err := parseQuery(params)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcnQuery, err)
	}
	conn, err := connectionFor(rc, params, paramConnection)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcnQuery, err)
	}
	ssh, err := sdk.Connect(ctx, rc, device, params, fqcnQuery)
	if err != nil {
		return collection.Result{}, err
	}
	defer func() { _ = ssh.Close() }()
	s, err := open(ctx, ssh, conn, req.database)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcnQuery, err)
	}
	defer func() { _ = s.Close() }()

	warnings := transportWarnings(conn)
	privileged, err := privilegedRole(ctx, s)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: reading the login's privileges: %w", fqcnQuery, err)
	}
	if privileged != "" {
		if !req.allowPrivRol {
			return collection.Result{}, fmt.Errorf("%s: connection %s logs in as %s, which can act outside the database even in a read-only transaction (COPY TO PROGRAM, server files); give queries a role without it, or set %s", fqcnQuery, conn.Name, privileged, paramAllowPrivilegedRole)
		}
		warnings = append(warnings, fmt.Sprintf("the query ran as %s, which a read-only transaction does not confine (%s)", privileged, paramAllowPrivilegedRole))
	}

	if !req.returnsRows {
		n, err := s.Exec(ctx, req.sql, req.args, req.limits)
		if err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcnQuery, err)
		}
		if err := rc.SetStat(statRowsAffected, n); err != nil {
			return collection.Result{}, err
		}
		return collection.Result{Changed: n > 0}, recordWarnings(rc, warnings)
	}
	table, err := s.Query(ctx, pgclient.QueryOptions{
		SQL: req.sql, Args: req.args, ReadOnly: !req.write, Limits: req.limits,
		MaxRows: req.maxRows, MaxBytes: maxCSVBytes,
	})
	switch {
	case errors.Is(err, pgclient.ErrTooManyRows):
		return collection.Result{}, fmt.Errorf("%s: the query returned more than %s (%d) rows; nothing is returned past the bound, so narrow the query or add LIMIT", fqcnQuery, paramMaxRows, req.maxRows)
	case errors.Is(err, pgclient.ErrTooLarge):
		return collection.Result{}, fmt.Errorf("%s: the query's rows are over %d bytes of CSV; narrow the query or add LIMIT", fqcnQuery, maxCSVBytes)
	case err != nil:
		return collection.Result{}, fmt.Errorf("%s: %w", fqcnQuery, err)
	}
	columns := make([]any, len(table.Columns))
	for i, c := range table.Columns {
		columns[i] = c
	}
	for key, value := range map[string]any{statColumns: columns, statRowCount: len(table.Rows), statCSV: table.CSV()} {
		if err := rc.SetStat(key, value); err != nil {
			return collection.Result{}, err
		}
	}
	return collection.Result{Changed: req.write}, recordWarnings(rc, warnings)
}

// privilegedRole names what makes the session's login able to act outside
// the database from a read-only transaction: superuser, or membership of
// one of privilegedRoles; empty when nothing does.
func privilegedRole(ctx context.Context, s *pgclient.Session) (string, error) {
	var super bool
	var members string
	err := s.DB().QueryRowContext(ctx, `
SELECT r.rolsuper,
       coalesce((SELECT string_agg(b.rolname, ', ' ORDER BY b.rolname)
                 FROM pg_roles b
                 WHERE b.rolname = ANY (string_to_array($1, ','))
                   AND pg_has_role(current_user, b.oid, 'MEMBER')), '')
FROM pg_roles r WHERE r.rolname = current_user`, strings.Join(privilegedRoles, ",")).Scan(&super, &members)
	switch {
	case err != nil:
		return "", err
	case super:
		return "a superuser", nil
	case members != "":
		return "a member of " + members, nil
	}
	return "", nil
}

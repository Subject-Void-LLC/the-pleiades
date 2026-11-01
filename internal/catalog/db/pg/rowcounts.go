// db.pg.rowcounts: every table's exact row count, each database read as
// one moment, and two databases compared table by table.
package pg

import (
	"context"
	"database/sql"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/pgclient"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// rowcounts' own parameters and returns.
const (
	paramCompare   = "compare"
	paramSchemas   = "schemas"
	paramExclude   = "exclude"
	paramMaxTables = "max_tables"

	statTables      = "tables"
	statDifferences = "differences"
	statOnlyHere    = "only_here"
	statOnlyThere   = "only_there"
	// statCompareExists says whether compare's database exists.
	statCompareExists = "compare_exists"
)

// rowcounts' defaults and bounds.
const (
	defaultCountTimeout = 5 * time.Minute
	defaultMaxTables    = 1000
	maxMaxTables        = 10000
)

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: fqcnRowcounts,
		Manifest: collection.Manifest{
			SupportedTransports:  []string{"ssh"},
			RequiredCapabilities: []capability.Name{capability.NameSSHTransport},
			ExecutionContext:     collection.ExecutionContext{Site: collection.SiteTarget, Device: collection.DeviceRequired},
			EngineVersion:        ">=0.2.0",
			Status:               collection.StatusImplemented,
			NoBecomeReason:       noBecomeForward,
			SupportsCheck:        true,
			Reversibility: collection.Reversibility{
				ReadOnly: true,
				Notes:    "It counts rows in read-only transactions, and writes nothing.",
			},
			Doc: rowcountsDoc(),
		},
		Invoke: Rowcounts,
		// It only reads, so a check of it is the task itself.
		Check: Rowcounts,
	})
}

// countScope is which tables a side counts.
type countScope struct {
	schemas   map[string]bool
	exclude   []string
	maxTables int
	timeout   time.Duration
}

// includes reports whether the scope counts table (schema.name).
func (s countScope) includes(table string) bool {
	schema, _, _ := strings.Cut(table, ".")
	if len(s.schemas) > 0 && !s.schemas[schema] {
		return false
	}
	for _, pattern := range s.exclude {
		if pattern == table || (strings.HasSuffix(pattern, ".*") && schema+".*" == pattern) {
			return false
		}
	}
	return true
}

// Rowcounts implements db.pg.rowcounts.
func Rowcounts(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	fail := func(err error) (collection.Result, error) {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcnRowcounts, err)
	}
	database, err := databaseName(params, paramDatabase)
	if err != nil {
		return fail(err)
	}
	scope, err := parseScope(params)
	if err != nil {
		return fail(err)
	}
	conn, err := connectionFor(rc, params, paramConnection)
	if err != nil {
		return fail(err)
	}
	var other *wire.DatabaseConnection
	otherDatabase := ""
	if raw, set := params[paramCompare]; set && raw != nil {
		cmp, ok := raw.(map[string]any)
		if !ok {
			return fail(fmt.Errorf("%s is a mapping of database and, for another server, connection", paramCompare))
		}
		for _, key := range slices.Sorted(maps.Keys(cmp)) {
			if key != paramDatabase && key != paramConnection {
				// A misspelled connection would compare on this side's own.
				return fail(fmt.Errorf("%s: %q is not %s or %s", paramCompare, key, paramDatabase, paramConnection))
			}
		}
		if otherDatabase, err = databaseName(cmp, paramDatabase); err != nil {
			return fail(fmt.Errorf("%s: %w", paramCompare, err))
		}
		c := conn
		if _, named := cmp[paramConnection]; named {
			if c, err = connectionFor(rc, cmp, paramConnection); err != nil {
				return fail(fmt.Errorf("%s: %w", paramCompare, err))
			}
		}
		if c.Name == conn.Name && otherDatabase == database {
			return fail(fmt.Errorf("%s names the database being counted", paramCompare))
		}
		other = &c
	}
	ssh, err := sdk.Connect(ctx, rc, device, params, fqcnRowcounts)
	if err != nil {
		return collection.Result{}, err
	}
	defer func() { _ = ssh.Close() }()

	warnings := transportWarnings(conn)
	here, hereExists, err := countSide(ctx, ssh, conn, database, scope)
	if err != nil {
		return fail(err)
	}
	if err := rc.SetStat(statExists, hereExists); err != nil {
		return collection.Result{}, err
	}
	var there map[string]int64
	// absent are the databases compared that do not exist: with compare,
	// each is a difference, so two that do not exist never agree.
	var absent []string
	if other != nil {
		if other.Name != conn.Name {
			warnings = append(warnings, transportWarnings(*other)...)
		}
		var thereExists bool
		if there, thereExists, err = countSide(ctx, ssh, *other, otherDatabase, scope); err != nil {
			return fail(err)
		}
		if err := rc.SetStat(statCompareExists, thereExists); err != nil {
			return collection.Result{}, err
		}
		if !hereExists {
			absent = append(absent, database+" on connection "+conn.Name)
		}
		if !thereExists {
			absent = append(absent, otherDatabase+" on connection "+other.Name)
		}
	}

	names := make([]string, 0, len(here))
	for t := range here {
		names = append(names, t)
	}
	sort.Strings(names)
	tables := make([]any, 0, len(names))
	var diffs, onlyHere, onlyThere []string
	for _, t := range names {
		entry := map[string]any{"table": t, "rows": here[t]}
		if there != nil {
			n, ok := there[t]
			switch {
			case !ok:
				onlyHere = append(onlyHere, t)
			case n != here[t]:
				entry["compare_rows"] = n
				diffs = append(diffs, fmt.Sprintf("%s: %d here, %d in %s", t, here[t], n, otherDatabase))
			default:
				entry["compare_rows"] = n
			}
		}
		tables = append(tables, entry)
	}
	for t := range there {
		if _, ok := here[t]; !ok {
			onlyThere = append(onlyThere, t)
		}
	}
	sort.Strings(onlyThere)
	for key, value := range map[string]any{statTables: tables, statDifferences: len(diffs), statOnlyHere: strs(onlyHere), statOnlyThere: strs(onlyThere)} {
		if err := rc.SetStat(key, value); err != nil {
			return collection.Result{}, err
		}
	}
	if n := len(diffs) + len(onlyHere) + len(onlyThere) + len(absent); n > 0 {
		var lines []string
		for _, db := range absent {
			lines = append(lines, db+": does not exist")
		}
		lines = append(lines, diffs...)
		for _, t := range onlyHere {
			lines = append(lines, t+": only in "+database)
		}
		for _, t := range onlyThere {
			lines = append(lines, t+": only in "+otherDatabase)
		}
		return fail(fmt.Errorf("%s and %s differ in %d way(s)%s", database, otherDatabase, n, listed(lines)))
	}
	return collection.Result{Changed: false}, recordWarnings(rc, warnings)
}

// parseScope reads schemas, exclude, max_tables and timeout.
func parseScope(params map[string]any) (countScope, error) {
	scope := countScope{schemas: map[string]bool{}}
	schemas, _, err := sdk.StringSlice(params, paramSchemas)
	if err != nil {
		return scope, err
	}
	for _, s := range schemas {
		scope.schemas[s] = true
	}
	if scope.exclude, _, err = sdk.StringSlice(params, paramExclude); err != nil {
		return scope, err
	}
	for _, pattern := range scope.exclude {
		table := pattern
		if schema, wild := strings.CutSuffix(pattern, ".*"); wild {
			table = schema + ".x"
		}
		if _, _, err := qualifiedName(table); err != nil || !strings.Contains(pattern, ".") {
			return scope, fmt.Errorf("%s: %q is not schema.table or schema.*", paramExclude, pattern)
		}
	}
	if scope.maxTables, err = intParam(params, paramMaxTables, defaultMaxTables, maxMaxTables); err != nil {
		return scope, err
	}
	if scope.timeout, err = durationParam(params, paramTimeout, defaultCountTimeout, maxQueryTimeout); err != nil {
		return scope, err
	}
	return scope, nil
}

// countSide counts every table the scope includes in database, from one
// snapshot (REPEATABLE READ READ ONLY), so the counts are one moment.
// Partitions are counted through their parent, once. A database that does
// not exist holds no tables and reports exists false.
func countSide(ctx context.Context, ssh *remoteexec.Conn, conn wire.DatabaseConnection, database string, scope countScope) (counts map[string]int64, exists bool, err error) {
	counts = map[string]int64{}
	s, err := open(ctx, ssh, conn, database)
	if pgclient.ErrorCode(err) == pgclient.InvalidCatalogName {
		return counts, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = s.Close() }()
	err = s.Snapshot(ctx, pgclient.Limits{Statement: scope.timeout, Lock: defaultLockTimeout}, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `
SELECT n.nspname, c.relname FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE c.relkind IN ('r', 'p') AND NOT c.relispartition AND `+userSchema+` ORDER BY 1, 2`)
		if err != nil {
			return err
		}
		type table struct{ schema, name string }
		var tables []table
		for rows.Next() {
			var t table
			if err := rows.Scan(&t.schema, &t.name); err != nil {
				_ = rows.Close()
				return err
			}
			if scope.includes(t.schema + "." + t.name) {
				tables = append(tables, t)
			}
		}
		_ = rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		// A schema named but absent (a typo, or a case PostgreSQL folded)
		// would count nothing here and agree with nothing there.
		for _, schema := range slices.Sorted(maps.Keys(scope.schemas)) {
			var exists bool
			if err := tx.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM pg_namespace WHERE nspname = $1)", schema).Scan(&exists); err != nil {
				return err
			}
			if !exists {
				return fmt.Errorf("%s names %q, which %s does not have", paramSchemas, schema, database)
			}
		}
		if len(tables) > scope.maxTables {
			return fmt.Errorf("%s holds %d tables, over %s (%d); narrow it with %s or %s", database, len(tables), paramMaxTables, scope.maxTables, paramSchemas, paramExclude)
		}
		for _, t := range tables {
			var n int64
			if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM "+quoteIdent(t.schema)+"."+quoteIdent(t.name)).Scan(&n); err != nil {
				return fmt.Errorf("counting %s.%s in %s: %w", t.schema, t.name, database, err)
			}
			counts[t.schema+"."+t.name] = n
		}
		return nil
	})
	return counts, err == nil, err
}

// strs is a string list as a return value.
func strs(in []string) []any {
	out := make([]any, len(in))
	for i, s := range in {
		out[i] = s
	}
	return out
}

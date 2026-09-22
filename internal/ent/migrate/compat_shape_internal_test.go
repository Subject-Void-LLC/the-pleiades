// This file is the static half of the compatibility policy (compat.go): it
// migrates a real database of each dialect one migration at a time, reads the
// schema after each step, and classifies every step as EXPAND or CONTRACT by
// what actually changed. A contracting step that is not declared fails, and so
// does a declared contract that changed nothing a reader could notice (unless
// it is marked semantic).
//
// Its first run is also its own evidence: the classifier must flag exactly the
// six migrations already known to break the build before them, and nothing
// else in either dialect's history. A classifier that flagged more would make
// the policy impossible to follow; one that flagged fewer would miss the
// failures that shipped.
package migrate

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"testing"
)

// columnModel is what a reader of one column depends on.
type columnModel struct {
	typ        string
	notNull    bool
	hasDefault bool
}

// tableModel is one table's columns, foreign keys and unique column sets.
type tableModel struct {
	columns map[string]columnModel

	// foreignKeys maps "column->table(column)" to its ON DELETE action.
	foreignKeys map[string]string

	// uniques holds each unique index or constraint as its sorted,
	// comma-joined columns.
	uniques map[string]bool
}

// schemaModel is a whole schema as far as compatibility is concerned.
type schemaModel struct {
	tables map[string]tableModel

	// code holds every trigger, rule and function: anything that runs on
	// the old build's writes without the old build knowing it exists.
	code map[string]bool
}

// TestEveryMigrationIsClassifiedAsDeclared is described in this file's header.
func TestEveryMigrationIsClassifiedAsDeclared(t *testing.T) {
	forEachDialect(t, func(t *testing.T, dialectName string) {
		ctx := context.Background()
		db := freshDB(t, dialectName)
		src := migrationSources[dialectName]
		names := namesOf(t, dialectName)

		var previous schemaModel
		flagged := map[string][]string{}
		for i, name := range names {
			if _, err := applyPending(ctx, db, src, names, name, gateStrict); err != nil {
				t.Fatalf("applying through %s: %v", name, err)
			}
			current := readModel(t, dialectName, db)
			if i > 0 {
				if changes := contractingChanges(previous, current); len(changes) > 0 {
					flagged[name] = changes
				}
			}
			previous = current
		}

		declared := contracts[dialectName]
		for name, changes := range flagged {
			if _, ok := declared[name]; !ok {
				t.Errorf("%s %s contracts the schema but is not declared in compat.go:\n  %s", dialectName, name, strings.Join(changes, "\n  "))
			}
		}
		for name, c := range declared {
			if _, ok := flagged[name]; !ok && !c.semantic {
				t.Errorf("%s %s is declared a contract, but nothing a reader depends on changed; mark it semantic if its meaning changed, or remove the declaration", dialectName, name)
			}
		}
		for name, changes := range flagged {
			t.Logf("%s %s contracts: %s", dialectName, name, strings.Join(changes, "; "))
		}
	})
}

// contractingChanges lists every way next breaks a reader or writer built
// against previous.
func contractingChanges(previous, next schemaModel) []string {
	var changes []string
	for tableName, before := range previous.tables {
		after, ok := next.tables[tableName]
		if !ok {
			changes = append(changes, "drops table "+tableName)
			continue
		}
		for columnName, was := range before.columns {
			now, ok := after.columns[columnName]
			switch {
			case !ok:
				changes = append(changes, fmt.Sprintf("drops column %s.%s", tableName, columnName))
			case now.typ != was.typ:
				changes = append(changes, fmt.Sprintf("changes %s.%s from %s to %s", tableName, columnName, was.typ, now.typ))
			case now.notNull && !was.notNull:
				changes = append(changes, fmt.Sprintf("makes %s.%s NOT NULL", tableName, columnName))
			case !now.notNull && was.notNull:
				// A reader built for a required column scans NULL into a
				// value that cannot hold one.
				changes = append(changes, fmt.Sprintf("makes %s.%s nullable", tableName, columnName))
			}
		}
		for columnName, now := range after.columns {
			if _, existed := before.columns[columnName]; !existed && now.notNull && !now.hasDefault {
				// A writer built before it never supplies it.
				changes = append(changes, fmt.Sprintf("adds %s.%s as NOT NULL with no default", tableName, columnName))
			}
		}
		for key, action := range after.foreignKeys {
			if was, existed := before.foreignKeys[key]; existed && was != action {
				changes = append(changes, fmt.Sprintf("changes %s %s from ON DELETE %s to %s", tableName, key, was, action))
			}
		}
		for key := range after.foreignKeys {
			if _, existed := before.foreignKeys[key]; !existed && existedColumn(before, key) {
				changes = append(changes, fmt.Sprintf("adds a foreign key %s %s over an existing column", tableName, key))
			}
		}
		for columns := range after.uniques {
			if !before.uniques[columns] && allExisted(before, columns) {
				changes = append(changes, fmt.Sprintf("adds a unique constraint on existing %s(%s)", tableName, columns))
			}
		}
	}
	for code := range next.code {
		if !previous.code[code] {
			changes = append(changes, "adds "+code)
		}
	}
	sort.Strings(changes)
	return changes
}

// existedColumn reports whether a foreign key's own column already existed.
func existedColumn(table tableModel, key string) bool {
	column := key[:strings.Index(key, "->")]
	_, ok := table.columns[column]
	return ok
}

// allExisted reports whether every column of a unique set already existed.
func allExisted(table tableModel, columns string) bool {
	for _, c := range strings.Split(columns, ",") {
		if _, ok := table.columns[c]; !ok {
			return false
		}
	}
	return true
}

// readModel reads db's schema model in its dialect.
func readModel(t *testing.T, dialectName string, db *sql.DB) schemaModel {
	t.Helper()
	if dialectName == "postgres" {
		return readPostgresModel(t, db)
	}
	return readSQLiteModel(t, db)
}

// queryStrings runs a query whose every column is text and returns the rows.
func queryStrings(t *testing.T, db *sql.DB, query string, args ...any) [][]string {
	t.Helper()
	rows, err := db.Query(query, args...)
	if err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	var out [][]string
	for rows.Next() {
		values := make([]sql.NullString, len(cols))
		targets := make([]any, len(cols))
		for i := range values {
			targets[i] = &values[i]
		}
		if err := rows.Scan(targets...); err != nil {
			t.Fatal(err)
		}
		row := make([]string, len(cols))
		for i, v := range values {
			row[i] = v.String
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// newTable returns an empty table model.
func newTable() tableModel {
	return tableModel{columns: map[string]columnModel{}, foreignKeys: map[string]string{}, uniques: map[string]bool{}}
}

// readPostgresModel reads the model from PostgreSQL's catalogs, for the
// current schema only.
func readPostgresModel(t *testing.T, db *sql.DB) schemaModel {
	t.Helper()
	m := schemaModel{tables: map[string]tableModel{}, code: map[string]bool{}}
	for _, r := range queryStrings(t, db, `SELECT table_name FROM information_schema.tables
		WHERE table_schema = current_schema() AND table_type = 'BASE TABLE' AND table_name <> 'schema_migrations'`) {
		m.tables[r[0]] = newTable()
	}
	for _, r := range queryStrings(t, db, `SELECT table_name, column_name, data_type, is_nullable, coalesce(column_default, '')
		FROM information_schema.columns WHERE table_schema = current_schema()`) {
		table, ok := m.tables[r[0]]
		if !ok {
			continue
		}
		table.columns[r[1]] = columnModel{typ: r[2], notNull: r[3] == "NO", hasDefault: r[4] != ""}
	}
	for _, r := range queryStrings(t, db, `SELECT c.conrelid::regclass::text, a.attname, c.confrelid::regclass::text, fa.attname, c.confdeltype::text
		FROM pg_constraint c
		JOIN pg_attribute a ON a.attrelid = c.conrelid AND a.attnum = c.conkey[1]
		JOIN pg_attribute fa ON fa.attrelid = c.confrelid AND fa.attnum = c.confkey[1]
		WHERE c.contype = 'f' AND c.connamespace = current_schema()::regnamespace`) {
		if table, ok := m.tables[r[0]]; ok {
			table.foreignKeys[r[1]+"->"+r[2]+"("+r[3]+")"] = postgresDeleteAction(r[4])
		}
	}
	for _, r := range queryStrings(t, db, `SELECT c.relname, string_agg(a.attname, ',' ORDER BY a.attname)
		FROM pg_index i
		JOIN pg_class c ON c.oid = i.indrelid
		JOIN pg_attribute a ON a.attrelid = i.indrelid AND a.attnum = ANY (i.indkey)
		WHERE i.indisunique AND NOT i.indisprimary AND c.relnamespace = current_schema()::regnamespace
		GROUP BY i.indexrelid, c.relname`) {
		if table, ok := m.tables[r[0]]; ok {
			table.uniques[r[1]] = true
		}
	}
	for _, r := range queryStrings(t, db, `SELECT 'trigger ' || tgname FROM pg_trigger WHERE NOT tgisinternal
		UNION ALL SELECT 'rule ' || rulename FROM pg_rules WHERE schemaname = current_schema()
		UNION ALL SELECT 'function ' || proname FROM pg_proc WHERE pronamespace = current_schema()::regnamespace`) {
		m.code[r[0]] = true
	}
	return m
}

// postgresDeleteAction spells pg_constraint.confdeltype the way SQLite does.
func postgresDeleteAction(code string) string {
	switch code {
	case "c":
		return "CASCADE"
	case "n":
		return "SET NULL"
	case "d":
		return "SET DEFAULT"
	case "r":
		return "RESTRICT"
	default:
		return "NO ACTION"
	}
}

// readSQLiteModel reads the model through SQLite's pragma functions.
func readSQLiteModel(t *testing.T, db *sql.DB) schemaModel {
	t.Helper()
	m := schemaModel{tables: map[string]tableModel{}, code: map[string]bool{}}
	for _, r := range queryStrings(t, db, `SELECT name FROM sqlite_master WHERE type = 'table'
		AND name NOT LIKE 'sqlite_%' AND name <> 'schema_migrations'`) {
		m.tables[r[0]] = newTable()
	}
	for name, table := range m.tables {
		for _, r := range queryStrings(t, db, `SELECT name, upper(type), "notnull", coalesce(dflt_value, ''), pk FROM pragma_table_info(?)`, name) {
			// A primary key column is NOT NULL whatever the flag says.
			table.columns[r[0]] = columnModel{typ: r[1], notNull: r[2] == "1" || r[4] != "0", hasDefault: r[3] != ""}
		}
		for _, r := range queryStrings(t, db, `SELECT "from", "table", "to", on_delete FROM pragma_foreign_key_list(?)`, name) {
			table.foreignKeys[r[0]+"->"+r[1]+"("+r[2]+")"] = strings.ToUpper(r[3])
		}
		for _, idx := range queryStrings(t, db, `SELECT name FROM pragma_index_list(?) WHERE "unique" = 1 AND origin <> 'pk'`, name) {
			var columns []string
			for _, c := range queryStrings(t, db, `SELECT name FROM pragma_index_info(?)`, idx[0]) {
				columns = append(columns, c[0])
			}
			sort.Strings(columns)
			table.uniques[strings.Join(columns, ",")] = true
		}
	}
	for _, r := range queryStrings(t, db, `SELECT 'trigger ' || name FROM sqlite_master WHERE type = 'trigger'`) {
		m.code[r[0]] = true
	}
	return m
}

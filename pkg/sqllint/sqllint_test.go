// Tests for the lexer and the rules, on the shapes real migrations take.
package sqllint_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/sqllint"
)

// TestSplit_SplitsOnlyAtTopLevelSemicolons: a semicolon, a keyword or a
// comment marker inside a string of any form, a quoted identifier, a
// nested comment or a dollar-quoted body is not a boundary.
func TestSplit_SplitsOnlyAtTopLevelSemicolons(t *testing.T) {
	src := `-- a header; not a statement
CREATE TABLE "drop;me" (id int, note text DEFAULT 'a;b''c');
/* outer /* inner; */ still; comment */
INSERT INTO t VALUES (E'it\'s; here', U&'d\0061t;a', $$ ; DROP TABLE x; $$);
CREATE FUNCTION f() RETURNS int AS $body$ BEGIN; RETURN 1; END $body$ LANGUAGE plpgsql;
SELECT $1::int, 'x' -- trailing; comment
;
;;
UPDATE t SET note = 'y'`
	stmts, err := sqllint.Split(src)
	if err != nil {
		t.Fatal(err)
	}
	var first []string
	for _, s := range stmts {
		first = append(first, s.Words()[0])
	}
	if got := strings.Join(first, " "); got != "CREATE INSERT CREATE SELECT UPDATE" {
		t.Fatalf("statements begin %q", got)
	}
	if stmts[0].Line != 2 || stmts[1].Line != 4 || stmts[4].Line != 9 {
		t.Errorf("lines %d %d %d", stmts[0].Line, stmts[1].Line, stmts[4].Line)
	}
	if !strings.HasPrefix(stmts[2].Text, "CREATE FUNCTION") || !strings.HasSuffix(stmts[2].Text, "plpgsql") {
		t.Errorf("text %q", stmts[2].Text)
	}
}

// TestSplit_RefusesWhatItCannotReadToTheEnd names each unterminated form
// and a psql command, by line.
func TestSplit_RefusesWhatItCannotReadToTheEnd(t *testing.T) {
	for src, want := range map[string]string{
		"SELECT 'open":               "line 1: a string is never closed",
		"SELECT\n\"open":             "line 2: a quoted identifier is never closed",
		"SELECT 1;\n/* /* */":        "line 2: a /* comment is never closed",
		"SELECT $tag$ body $other$":  "a $tag$ quote is never closed",
		"SELECT 1;\n\\i other.sql\n": "line 2: a line starting with a backslash is a psql command",
		"SELECT E'\\'":               "a string is never closed",
		strings.Repeat("x", 8<<20+1): "over",
	} {
		if _, err := sqllint.Split(src); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Split(%.30q) = %v, want %q", src, err, want)
		}
	}
	// A backslash that is not at a line's start is an operator's or a
	// string's, not a command.
	if _, err := sqllint.Split(`SELECT 'a\b', 1 \ 2`); err != nil {
		t.Errorf("a mid-line backslash: %v", err)
	}
}

// TestDestructive names each statement that removes or rewrites
// something, and passes the additive ones, including the near misses.
func TestDestructive(t *testing.T) {
	for sql, want := range map[string]string{
		"DROP TABLE t":                                                         "it drops a table",
		"drop index concurrently if exists i":                                  "it drops a index",
		"TRUNCATE t":                                                           "it truncates a table",
		"DELETE FROM t WHERE id = 1":                                           "it deletes rows",
		"REVOKE SELECT ON t FROM r":                                            "it revokes a privilege",
		"WITH gone AS (DELETE FROM t RETURNING *) SELECT 1":                    "it deletes rows in a WITH clause",
		"ALTER TABLE t DROP COLUMN c":                                          "it drops a column",
		"ALTER TABLE t RENAME COLUMN a TO b":                                   "it renames something",
		"ALTER TABLE t RENAME TO u":                                            "it renames something",
		"ALTER TABLE t ALTER COLUMN c TYPE bigint":                             "it changes a column's type",
		`ALTER TABLE t ALTER "c" SET DATA TYPE bigint`:                         "it changes a column's type",
		"ALTER TABLE t ALTER COLUMN c DROP NOT NULL":                           "it drops a not",
		"CREATE TABLE t (id int)":                                              "",
		"ALTER TABLE t ADD COLUMN type text":                                   "",
		"ALTER TABLE t ADD COLUMN c int, ALTER COLUMN d SET DEFAULT 1":         "",
		"ALTER TYPE mood ADD VALUE 'ok'":                                       "",
		"UPDATE t SET c = 1":                                                   "",
		`INSERT INTO t (note) VALUES ('DROP TABLE t')`:                         "",
		`CREATE TABLE "drop" (id int)`:                                         "",
		"CREATE FUNCTION f() RETURNS void AS $$ DELETE FROM t $$ LANGUAGE sql": "",
	} {
		stmts, err := sqllint.Split(sql)
		if err != nil || len(stmts) != 1 {
			t.Fatalf("Split(%q) = %v, %v", sql, stmts, err)
		}
		if got := sqllint.Destructive(stmts[0]); got != want {
			t.Errorf("Destructive(%q) = %q, want %q", sql, got, want)
		}
	}
}

// TestTransactionRules: which statements end the transaction a file runs
// in, and which cannot run in one.
func TestTransactionRules(t *testing.T) {
	ends := map[string]bool{
		"BEGIN": true, "COMMIT": true, "END": true, "ABORT": true, "ROLLBACK": true,
		"START TRANSACTION": true, "PREPARE TRANSACTION 'x'": true,
		"ROLLBACK TO SAVEPOINT s": false, "SAVEPOINT s": false, "RELEASE s": false,
		"DO $$ BEGIN PERFORM 1; END $$": false, "PREPARE q AS SELECT 1": false,
	}
	outside := map[string]string{
		"CREATE INDEX CONCURRENTLY i ON t (c)": "CREATE ... CONCURRENTLY",
		"REINDEX INDEX CONCURRENTLY i":         "REINDEX ... CONCURRENTLY",
		"VACUUM t":                             "VACUUM",
		"CREATE DATABASE d":                    "CREATE DATABASE",
		"ALTER SYSTEM SET work_mem = '1MB'":    "ALTER SYSTEM",
		"CREATE INDEX i ON t (c)":              "",
	}
	for sql, want := range ends {
		stmts, _ := sqllint.Split(sql)
		if got := sqllint.EndsTransaction(stmts[0]); got != want {
			t.Errorf("EndsTransaction(%q) = %v", sql, got)
		}
	}
	for sql, want := range outside {
		stmts, _ := sqllint.Split(sql)
		if got := sqllint.OutsideTransaction(stmts[0]); got != want {
			t.Errorf("OutsideTransaction(%q) = %q, want %q", sql, got, want)
		}
	}
}

// TestParseDirectives reads directives only from the header's comments.
func TestParseDirectives(t *testing.T) {
	d, err := sqllint.ParseDirectives("/* licence */\n-- pleiades: no-transaction\n-- pleiades: destructive: the column was unused since 2026-01\n-- an ordinary comment\nCREATE INDEX CONCURRENTLY i ON t (c);\n")
	if err != nil || !d.NoTransaction || d.Destructive != "the column was unused since 2026-01" {
		t.Fatalf("ParseDirectives = %+v, %v", d, err)
	}
	if d, err := sqllint.ParseDirectives("SELECT '\n-- pleiades: no-transaction\n'"); err != nil || d.NoTransaction {
		t.Errorf("a directive inside a string = %+v, %v", d, err)
	}
	for src, want := range map[string]string{
		"-- pleiades: no-transation\nSELECT 1":       `"no-transation" is not a directive`,
		"-- pleiades: destructive:\nSELECT 1":        "destructive needs a reason",
		"-- pleiades: no-transaction: yes\nSELECT 1": "takes no value",
		"SELECT 1;\n-- pleiades: no-transaction\n":   "line 2: a -- pleiades: directive after the first statement",
	} {
		if _, err := sqllint.ParseDirectives(src); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("ParseDirectives(%q) = %v, want %q", src, err, want)
		}
	}
}

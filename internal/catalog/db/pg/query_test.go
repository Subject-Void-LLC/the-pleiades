// Tests for db.pg.query against a real PostgreSQL through a real tunnel.
package pg_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/db/pg"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// query runs db.pg.query with extra params, returning its stats.
func query(h *harness, extra map[string]any) (map[string]any, error) {
	rc := h.fresh()
	_, err := pg.Query(context.Background(), rc, h.device, params(extra))
	return rc.stats, err
}

// TestQuery_ReadsAsALeastPrivilegedRole: a role of its own reads rows as
// CSV through the tunnel, cannot write without write, and with write
// changes rows and says how many; parameters are values, never SQL.
func TestQuery_ReadsAsALeastPrivilegedRole(t *testing.T) {
	h := newHarness(t)
	h.sql(t, "app",
		"CREATE TABLE orders (id int PRIMARY KEY, note text)",
		"INSERT INTO orders VALUES (1, 'first'), (2, NULL), (3, 'it''s, \"quoted\"')",
		"CREATE ROLE reader LOGIN PASSWORD 'reader-pw'",
		"GRANT SELECT, INSERT ON orders TO reader")
	h.connect("reader", "reader", "reader-pw")

	stats, err := query(h, map[string]any{"connection": "reader", "database": "app", "sql": "SELECT id, note FROM orders WHERE id <= $1 ORDER BY id", "args": []any{3}})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := stats["csv"], "id,note\r\n1,first\r\n2,\r\n3,\"it's, \"\"quoted\"\"\"\r\n"; got != want {
		t.Errorf("csv = %q, want %q", got, want)
	}
	if stats["row_count"] != 3 || len(stats["columns"].([]any)) != 2 {
		t.Errorf("stats = %v", stats)
	}

	if _, err := query(h, map[string]any{"connection": "reader", "database": "app", "sql": "INSERT INTO orders VALUES (4, 'x')"}); err == nil || !strings.Contains(err.Error(), "read-only transaction") {
		t.Errorf("an insert without write = %v", err)
	}
	stats, err = query(h, map[string]any{"connection": "reader", "database": "app", "sql": "INSERT INTO orders VALUES ($1, $2)", "args": []any{4, "'; DROP TABLE orders; --"}, "write": true})
	if err != nil || stats["rows_affected"] != int64(1) {
		t.Fatalf("an insert with write = %v, %v", stats, err)
	}
	if got := h.value(t, "app", "SELECT note FROM orders WHERE id = 4"); got != "'; DROP TABLE orders; --" {
		t.Errorf("the argument was not stored as a value: %q", got)
	}
}

// TestQuery_RefusesWhatItMustNotRun: two statements, rows past the bound,
// a login a read-only transaction does not confine, a connection nobody
// defined, and a timeout.
func TestQuery_RefusesWhatItMustNotRun(t *testing.T) {
	h := newHarness(t)
	h.sql(t, "app", "CREATE ROLE reader LOGIN PASSWORD 'reader-pw'", "CREATE ROLE runner LOGIN PASSWORD 'runner-pw'", "GRANT pg_execute_server_program TO runner")
	h.connect("reader", "reader", "reader-pw")
	h.connect("runner", "runner", "runner-pw")
	base := map[string]any{"connection": "reader", "database": "app"}
	with := func(extra map[string]any) map[string]any {
		out := map[string]any{}
		for k, v := range base {
			out[k] = v
		}
		for k, v := range extra {
			out[k] = v
		}
		return out
	}
	for name, tc := range map[string]struct {
		params map[string]any
		want   string
	}{
		"two statements":        {with(map[string]any{"sql": "SELECT 1; SELECT 2"}), "holds 2 statements"},
		"past max_rows":         {with(map[string]any{"sql": "SELECT generate_series(1, 11)", "max_rows": 10}), "more than max_rows (10) rows"},
		"a superuser":           {with(map[string]any{"connection": "admin", "sql": "SELECT 1"}), "logs in as a superuser"},
		"a server program role": {with(map[string]any{"connection": "runner", "sql": "SELECT 1"}), "a member of pg_execute_server_program"},
		"no such connection":    {with(map[string]any{"connection": "nowhere", "sql": "SELECT 1"}), "pleiades add-database-connection nowhere"},
		"a timeout":             {with(map[string]any{"sql": "SELECT pg_sleep(5)", "timeout": "200ms"}), "statement timeout"},
		"a bad duration":        {with(map[string]any{"sql": "SELECT 1", "timeout": 30}), "is a duration"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := query(h, tc.params); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("query = %v, want %q", err, tc.want)
			}
		})
	}

	stats, err := query(h, with(map[string]any{"connection": "admin", "sql": "SELECT current_user", "allow_privileged_role": true}))
	if err != nil {
		t.Fatal(err)
	}
	if warnings, _ := stats["warnings"].([]string); len(warnings) != 1 || !strings.Contains(warnings[0], "a superuser") {
		t.Errorf("warnings = %v", stats["warnings"])
	}
}

// TestQuery_ACheckOfAWriteIsRefused: a read's check is the read; a
// write's is unchecked, decided from the parameters alone.
func TestQuery_ACheckOfAWriteIsRefused(t *testing.T) {
	d, ok := collection.Lookup("db.pg.query")
	if !ok || d.CheckCall == nil {
		t.Fatal("db.pg.query declares no CheckCall")
	}
	var cannot *collection.CannotCheckError
	if err := d.CheckCall(map[string]any{"write": true}); !errors.As(err, &cannot) {
		t.Errorf("CheckCall(write) = %v", err)
	}
	if err := d.CheckCall(map[string]any{}); err != nil {
		t.Errorf("CheckCall(read) = %v", err)
	}
	h := newHarness(t)
	h.sql(t, "app", "CREATE TABLE t (id int)", "CREATE ROLE w LOGIN PASSWORD 'w-pw'", "GRANT INSERT, SELECT ON t TO w")
	h.connect("w", "w", "w-pw")
	_, err := pg.CheckQuery(context.Background(), h.fresh(), h.device, params(map[string]any{"connection": "w", "database": "app", "sql": "INSERT INTO t VALUES (1)", "write": true}))
	if !errors.As(err, &cannot) {
		t.Fatalf("CheckQuery(write) = %v", err)
	}
	if got := h.value(t, "app", "SELECT count(*) FROM t"); got != "0" {
		t.Errorf("a check of a write wrote: %s rows", got)
	}
}

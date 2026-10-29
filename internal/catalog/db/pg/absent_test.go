// Tests that db.pg.grants and db.pg.rowcounts answer a role or a database
// that does not exist as data, in a run and in a check, against a real
// PostgreSQL (Phase 131, R4).
package pg_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// callMode runs the registered method fqcn in a run or, with check, its
// check, returning what it recorded.
func callMode(t *testing.T, h *harness, fqcn string, check bool, extra map[string]any) (map[string]any, error) {
	t.Helper()
	d, ok := collection.Lookup(fqcn)
	if !ok {
		t.Fatalf("%s is not registered", fqcn)
	}
	method := d.Invoke
	if check {
		method = d.Check
	}
	rc := h.fresh()
	_, err := method(context.Background(), rc, h.device, params(extra))
	return rc.stats, err
}

// TestPGReads_AnswerAbsentAsData: an absent role or database is exists
// false in both modes. db.pg.grants then lists what is missing, the role or
// database first, and fails as any difference does; db.pg.rowcounts counts
// an absent database as holding no tables and passes alone, while with
// compare an absent side fails, even when both are absent.
func TestPGReads_AnswerAbsentAsData(t *testing.T) {
	h := newHarness(t)
	h.sql(t, "app", "CREATE TABLE orders (id int)", "INSERT INTO orders VALUES (1)")
	for _, check := range []bool{false, true} {
		grants := func(database, role string, expect map[string]any) (map[string]any, error) {
			return callMode(t, h, "db.pg.grants", check, map[string]any{"connection": "admin", "database": database, "role": role, "expect": expect})
		}
		stats, err := grants("app", "nobody", map[string]any{"database": []any{"CONNECT"}})
		missing := toStrings(stats["missing"])
		if err == nil || stats["exists"] != false || !slices.Equal(missing, []string{"role nobody (it does not exist)", "CONNECT on database app"}) || len(toStrings(stats["extra"])) != 0 {
			t.Errorf("check %v, absent role: err %v, stats %v", check, err, stats)
		}
		stats, err = grants("nope", adminUser, map[string]any{})
		if err == nil || stats["exists"] != false || !slices.Equal(toStrings(stats["missing"]), []string{"database nope (it does not exist)"}) {
			t.Errorf("check %v, absent database: err %v, stats %v", check, err, stats)
		}

		rowcounts := func(extra map[string]any) (map[string]any, error) {
			extra["connection"] = "admin"
			return callMode(t, h, "db.pg.rowcounts", check, extra)
		}
		stats, err = rowcounts(map[string]any{"database": "nope"})
		if tables, _ := stats["tables"].([]any); err != nil || stats["exists"] != false || tables == nil || len(tables) != 0 {
			t.Errorf("check %v, an absent database counted alone: err %v, stats %v", check, err, stats)
		}
		stats, err = rowcounts(map[string]any{"database": "app"})
		if err != nil || stats["exists"] != true {
			t.Errorf("check %v, a database that exists: err %v, stats %v", check, err, stats)
		}
		stats, err = rowcounts(map[string]any{"database": "app", "compare": map[string]any{"database": "nope"}})
		if err == nil || !strings.Contains(err.Error(), "nope on connection admin: does not exist") ||
			stats["exists"] != true || stats["compare_exists"] != false || !slices.Equal(toStrings(stats["only_here"]), []string{"public.orders"}) {
			t.Errorf("check %v, compared with an absent database: err %v, stats %v", check, err, stats)
		}
		stats, err = rowcounts(map[string]any{"database": "nope", "compare": map[string]any{"database": "nada"}})
		if err == nil || stats["exists"] != false || stats["compare_exists"] != false {
			t.Errorf("check %v, two absent databases agreed: err %v, stats %v", check, err, stats)
		}
	}
}

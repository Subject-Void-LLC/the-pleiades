// Tests for db.pg.grants against a real PostgreSQL's catalogs.
package pg_test

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/db/pg"
)

// grants runs db.pg.grants for role against expect.
func grants(h *harness, role string, expect map[string]any) (map[string]any, error) {
	rc := h.fresh()
	_, err := pg.Grants(context.Background(), rc, h.device, params(map[string]any{"connection": "admin", "database": "app", "role": role, "expect": expect}))
	return rc.stats, err
}

// TestGrants_ComparesWhatTheRoleWasGranted: a role holding exactly what
// is declared passes, a pattern standing for every table in its schema;
// then an extra privilege, a grant option, a column grant, a membership
// and an owned table are each named extra, and a declared privilege on a
// new table is named missing.
func TestGrants_ComparesWhatTheRoleWasGranted(t *testing.T) {
	h := newHarness(t)
	h.sql(t, "app",
		"CREATE TABLE orders (id int, email text)",
		"CREATE TABLE items (id int)",
		"CREATE SEQUENCE ticket",
		"CREATE ROLE readers",
		"CREATE ROLE reporting LOGIN",
		"GRANT CONNECT ON DATABASE app TO reporting",
		"GRANT USAGE ON SCHEMA public TO reporting",
		"GRANT SELECT ON ALL TABLES IN SCHEMA public TO reporting",
		"GRANT USAGE ON SEQUENCE ticket TO reporting")
	expect := map[string]any{
		"database":  []any{"CONNECT"},
		"schemas":   map[string]any{"public": []any{"usage"}},
		"tables":    map[string]any{"public.*": []any{"SELECT"}},
		"sequences": map[string]any{"public.ticket": []any{"USAGE"}},
	}
	if _, err := grants(h, "reporting", expect); err != nil {
		t.Fatalf("exactly what is declared: %v", err)
	}

	h.sql(t, "app",
		"GRANT INSERT ON orders TO reporting",
		"GRANT SELECT ON items TO reporting WITH GRANT OPTION",
		"GRANT UPDATE (email) ON orders TO reporting",
		"GRANT readers TO reporting",
		"CREATE TABLE notes (id int)",
		"ALTER TABLE notes OWNER TO reporting",
		"CREATE TABLE invoices (id int)")
	stats, err := grants(h, "reporting", expect)
	if err == nil {
		t.Fatal("extra privileges passed")
	}
	got := strings.Join(append(toStrings(stats["extra"]), toStrings(stats["missing"])...), "\n")
	for _, want := range []string{
		"INSERT on table public.orders",
		"SELECT on table public.items WITH GRANT OPTION (the grant option is not declared)",
		"UPDATE on column public.orders.email",
		"member of readers",
		"owns public.notes",
		"SELECT on table public.invoices",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the comparison does not name %q:\n%s", want, got)
		}
	}
	if !strings.Contains(err.Error(), "missing: SELECT on table public.invoices") || !strings.Contains(err.Error(), "extra: member of readers") {
		t.Errorf("the error = %v", err)
	}

	expect["tables"] = map[string]any{"public.*": []any{"SELECT"}, "public.orders": []any{"INSERT"}, "public.items": []any{"SELECT WITH GRANT OPTION"}, "public.invoices": []any{}}
	expect["columns"] = map[string]any{"public.orders.email": []any{"UPDATE"}}
	expect["member_of"] = []any{"readers"}
	expect["owns"] = []any{"public.notes"}
	h.sql(t, "app", "GRANT SELECT ON invoices TO reporting")
	if _, err := grants(h, "reporting", expect); err != nil {
		t.Fatalf("everything declared: %v", err)
	}
}

// TestGrants_RefusesADeclarationItCannotRead names an unknown kind, an
// unknown privilege, a malformed pattern and an absent role.
func TestGrants_RefusesADeclarationItCannotRead(t *testing.T) {
	h := newHarness(t)
	for name, tc := range map[string]struct {
		role   string
		expect map[string]any
		want   string
	}{
		"an unknown kind":      {"admin", map[string]any{"views": map[string]any{}}, "is not a kind this compares"},
		"an unknown privilege": {"admin", map[string]any{"tables": map[string]any{"public.t": []any{"EXECUTE"}}}, `"EXECUTE" is not a privilege a table has`},
		"a schema pattern":     {"admin", map[string]any{"schemas": map[string]any{"pub*": []any{"USAGE"}}}, "is not a schema"},
		"a bare table":         {"admin", map[string]any{"tables": map[string]any{"orders": []any{"SELECT"}}}, "is not a table"},
		"an absent role":       {"nobody", map[string]any{}, "role nobody does not exist"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := grants(h, tc.role, tc.expect); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("grants = %v, want %q", err, tc.want)
			}
		})
	}
}

// TestGrants_AllMeansWhatTheServerHas: ALL on a table stands for the
// privileges this server's tables have, so before PostgreSQL 17, which
// added MAINTAIN, a role granted ALL matches a declared ALL; a MAINTAIN
// declared outright there is refused rather than reported missing on
// every run. The fixture's server is older than 17.
func TestGrants_AllMeansWhatTheServerHas(t *testing.T) {
	h := newHarness(t)
	if major, _ := strconv.Atoi(h.value(t, "app", "SELECT current_setting('server_version_num')::int / 10000")); major >= 17 {
		t.Fatalf("the fixture's server is %d, which has MAINTAIN; this test needs one older than 17", major)
	}
	h.sql(t, "app", "CREATE TABLE orders (id int)", "CREATE ROLE writer", "GRANT ALL ON TABLE orders TO writer")
	if _, err := grants(h, "writer", map[string]any{"tables": map[string]any{"public.orders": []any{"ALL"}}}); err != nil {
		t.Fatalf("ALL granted, ALL declared: %v", err)
	}
	_, err := grants(h, "writer", map[string]any{"tables": map[string]any{"public.orders": []any{"ALL", "MAINTAIN"}}})
	if err == nil || !strings.Contains(err.Error(), "MAINTAIN exists from PostgreSQL 17") {
		t.Errorf("MAINTAIN declared on an older server = %v", err)
	}
}

// toStrings is a list return as strings.
func toStrings(v any) []string {
	list, _ := v.([]any)
	out := make([]string, len(list))
	for i, item := range list {
		out[i], _ = item.(string)
	}
	return out
}

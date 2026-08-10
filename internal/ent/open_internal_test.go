// This file tests resolveDSN directly, from inside package ent, because
// which dialect a DSN selects is the decision OpenDatabase exists to make
// and it is not observable from outside without a real server of each
// kind standing by.
package ent

import (
	"strings"
	"testing"

	"entgo.io/ent/dialect"
)

// TestResolveDSN_SelectsTheDialectTheSchemeNames is the table of every
// DSN shape OpenDatabase documents as accepted, plus the shapes it
// documents as rejected.
func TestResolveDSN_SelectsTheDialectTheSchemeNames(t *testing.T) {
	tests := []struct {
		name       string
		dsn        string
		wantDriver string
		wantErr    bool
	}{
		{
			name:       "postgres url",
			dsn:        "postgres://user:pass@localhost:5432/pleiades?sslmode=disable",
			wantDriver: dialect.Postgres,
		},
		{
			name:       "postgresql url, the longer spelling libpq also accepts",
			dsn:        "postgresql://user:pass@localhost:5432/pleiades",
			wantDriver: dialect.Postgres,
		},
		{
			name:       "sqlite url with a relative path",
			dsn:        "sqlite://controller.db",
			wantDriver: dialect.SQLite,
		},
		{
			name:       "sqlite url with an absolute path",
			dsn:        "sqlite:///var/lib/pleiades/controller.db",
			wantDriver: dialect.SQLite,
		},
		{
			name:       "bare relative path, the DB_PATH compatibility form",
			dsn:        "controller.db",
			wantDriver: dialect.SQLite,
		},
		{
			name:       "bare absolute path",
			dsn:        "/var/lib/pleiades/controller.db",
			wantDriver: dialect.SQLite,
		},
		{
			name:       "bare nested path",
			dsn:        "state/controller.db",
			wantDriver: dialect.SQLite,
		},
		{
			name:       "empty dsn resolves to sqlite rather than erroring",
			dsn:        "",
			wantDriver: dialect.SQLite,
		},
		{
			name:    "mysql is rejected rather than guessed at",
			dsn:     "mysql://user:pass@localhost:3306/pleiades",
			wantErr: true,
		},
		{
			name:    "an unknown scheme is rejected",
			dsn:     "cockroach://localhost:26257/pleiades",
			wantErr: true,
		},
		{
			name:    "a file url is rejected, since the sqlite scheme is the supported spelling",
			dsn:     "file://controller.db",
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resolved, err := resolveDSN(tc.dsn)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("resolveDSN(%q) succeeded with driver %q, want an error", tc.dsn, resolved.driver)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveDSN(%q) error = %v", tc.dsn, err)
			}
			if resolved.driver != tc.wantDriver {
				t.Fatalf("resolveDSN(%q) driver = %q, want %q", tc.dsn, resolved.driver, tc.wantDriver)
			}
		})
	}
}

// TestResolveDSN_PostgresDSNIsPassedThroughVerbatim proves the Postgres
// adapter does not rewrite the DSN.
//
// This matters because libpq accepts connection parameters this project
// has no opinion about (sslmode, connect_timeout, application_name, a
// non-default search_path), and silently dropping or reordering them
// would change how a deployment connects in ways nothing here could
// justify.
func TestResolveDSN_PostgresDSNIsPassedThroughVerbatim(t *testing.T) {
	const dsn = "postgres://u:p@db.internal:5432/pleiades?sslmode=verify-full&connect_timeout=10&application_name=controller"

	resolved, err := resolveDSN(dsn)
	if err != nil {
		t.Fatalf("resolveDSN error = %v", err)
	}
	if resolved.dsn != dsn {
		t.Fatalf("postgres DSN was rewritten:\n got %q\nwant %q", resolved.dsn, dsn)
	}
}

// TestResolveDSN_PostgresDescriptionRedactsThePassword proves the string
// OpenDatabase puts into its error messages does not carry the password
// from the DSN.
//
// OpenDatabase's errors reach composition roots that log them, and a DSN
// is the one value on this path that routinely carries a credential
// inline, so this is a real leak boundary rather than a cosmetic one.
func TestResolveDSN_PostgresDescriptionRedactsThePassword(t *testing.T) {
	const secret = "hunter2-do-not-log-me"
	dsn := "postgres://pleiades:" + secret + "@db.internal:5432/pleiades?sslmode=disable"

	resolved, err := resolveDSN(dsn)
	if err != nil {
		t.Fatalf("resolveDSN error = %v", err)
	}
	if strings.Contains(resolved.describe, secret) {
		t.Fatalf("the DSN description carries the password, so any logged error would leak it: %q", resolved.describe)
	}
	// It must still be useful for debugging, so the host and database
	// have to survive redaction.
	for _, want := range []string{"db.internal:5432", "pleiades"} {
		if !strings.Contains(resolved.describe, want) {
			t.Fatalf("the DSN description %q dropped %q, leaving nothing to debug with", resolved.describe, want)
		}
	}
}

// TestResolveDSN_SQLitePathSurvivesTheScheme proves the sqlite:// scheme
// is stripped and the remainder is treated as a literal filesystem path,
// so "sqlite:///a/b.db" names /a/b.db rather than a host called "a".
func TestResolveDSN_SQLitePathSurvivesTheScheme(t *testing.T) {
	tests := []struct {
		dsn      string
		wantPath string
	}{
		{dsn: "sqlite://controller.db", wantPath: "controller.db"},
		{dsn: "sqlite:///var/lib/pleiades/controller.db", wantPath: "/var/lib/pleiades/controller.db"},
		{dsn: "state/controller.db", wantPath: "state/controller.db"},
	}

	for _, tc := range tests {
		t.Run(tc.dsn, func(t *testing.T) {
			resolved, err := resolveDSN(tc.dsn)
			if err != nil {
				t.Fatalf("resolveDSN(%q) error = %v", tc.dsn, err)
			}
			// The driver DSN is a file: URI wrapping the path, so the
			// path has to appear in it literally.
			if !strings.Contains(resolved.dsn, tc.wantPath) {
				t.Fatalf("resolveDSN(%q) driver DSN = %q, which does not name the path %q", tc.dsn, resolved.dsn, tc.wantPath)
			}
		})
	}
}

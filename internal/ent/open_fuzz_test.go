// This file fuzzes resolveDSN, the connection-string boundary Phase 18
// introduced. It is an internal test because resolveDSN is where the
// decision being fuzzed actually happens; going through OpenDatabase
// would need a live server of each dialect and would measure the drivers
// rather than this project's own parsing.
package ent

import (
	"strings"
	"testing"

	"entgo.io/ent/dialect"
)

// FuzzResolveDSN fuzzes the DSN parser behind OpenDatabase.
//
// A DSN is semi-trusted input: it arrives from deployment configuration
// (an environment variable, a compose file, a Helm value) rather than
// from an end user, but it is still a string this process parses and
// then hands to a database driver, which is exactly the shape Part VIII's
// hardening categories care about.
//
// Three properties are held, and each one is a real failure this parser
// could have:
//
//  1. It never panics, whatever it is handed.
//  2. It never selects a dialect the DSN did not name. A string carrying
//     an unsupported scheme must be an error, never a silent fallback to
//     SQLite that would create a stray file on disk instead of connecting
//     to the server the operator asked for.
//  3. A resolved Postgres DSN reaches the driver byte for byte, so no
//     connection parameter is dropped, added, or reordered on the way.
func FuzzResolveDSN(f *testing.F) {
	// Valid shapes first, then adversarial ones.
	f.Add("postgres://user:pass@localhost:5432/pleiades?sslmode=disable")
	f.Add("postgresql://localhost/pleiades")
	f.Add("sqlite://controller.db")
	f.Add("sqlite:///var/lib/pleiades/controller.db")
	f.Add("controller.db")
	f.Add("")
	f.Add("mysql://user@host/db")
	f.Add("://")
	f.Add("postgres://")
	f.Add("sqlite://")
	// A scheme hidden behind a path separator, which must not be read as
	// a scheme.
	f.Add("./postgres://not-a-scheme")
	// Traversal in the SQLite path. The Walk-tier precedent is that a
	// path names exactly what its operator typed, so this must resolve
	// rather than error; the property under test is that it stays SQLite
	// and stays literal.
	f.Add("../../etc/passwd")
	// Connection-parameter smuggling attempts against the Postgres form.
	f.Add("postgres://h/db?search_path=evil")
	f.Add("postgres://h/db?sslmode=disable&sslmode=require")
	// Control characters and separators that a naive splitter would trip
	// on.
	f.Add("postgres://h/db\x00truncated")
	f.Add("sqlite://a\nb.db")
	f.Add("sqlite://a?b#c.db")
	f.Add(strings.Repeat("a", 4096))
	f.Add("SQLITE://UPPERCASE.db")

	f.Fuzz(func(t *testing.T, dsn string) {
		resolved, err := resolveDSN(dsn)
		if err != nil {
			// A rejection is always an acceptable outcome. What is not
			// acceptable is returning a usable driver alongside it.
			if resolved.driver != "" {
				t.Fatalf("resolveDSN(%q) returned both an error and the driver %q", dsn, resolved.driver)
			}
			return
		}

		switch resolved.driver {
		case dialect.Postgres:
			// Property 2: only a Postgres scheme may select Postgres.
			if !strings.HasPrefix(dsn, postgresURLScheme) && !strings.HasPrefix(dsn, postgresqlURLScheme) {
				t.Fatalf("resolveDSN(%q) selected Postgres, but the DSN names no Postgres scheme", dsn)
			}
			// Property 3: the driver gets exactly what the operator wrote.
			if resolved.dsn != dsn {
				t.Fatalf("resolveDSN(%q) rewrote the Postgres DSN to %q", dsn, resolved.dsn)
			}

		case dialect.SQLite:
			// Property 2 in the other direction: SQLite is only reachable
			// through its own scheme or through a string carrying no
			// scheme at all. Anything else would mean an unsupported
			// scheme silently became a file on disk.
			if !strings.HasPrefix(dsn, sqliteURLScheme) && strings.Contains(dsn, "://") {
				t.Fatalf("resolveDSN(%q) selected SQLite for a DSN carrying a foreign scheme", dsn)
			}
			// The SQLite adapter always produces a file: URI, never a
			// bare path, so go-sqlite3 parses it as a URI filename.
			if !strings.HasPrefix(resolved.dsn, "file:") {
				t.Fatalf("resolveDSN(%q) produced the SQLite DSN %q, which is not a file: URI", dsn, resolved.dsn)
			}

		default:
			t.Fatalf("resolveDSN(%q) selected the unknown driver %q", dsn, resolved.driver)
		}
	})
}

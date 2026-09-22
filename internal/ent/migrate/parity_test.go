// This file guards the one failure mode a multi-dialect migration set
// invites: regenerating one dialect after a schema edit and forgetting
// the other. It needs no database and no Docker, so it runs on every
// build, which is the point. The stronger claim, that each dialect's
// committed migrations really do bring a database all the way to the
// schema ent currently desires, needs a real server of each dialect and
// lives behind the integration build tag in parity_integration_test.go.
package migrate

import (
	"io/fs"
	"strconv"
	"strings"
	"testing"
)

// TestEveryDialectHasMigrations proves no dialect is registered in
// migrationSources with an unreadable or empty migration directory.
//
// A dialect that resolves but has nothing to apply would let a binary
// start against a completely empty database and then fail at the first
// query, which is the opposite of the fail-closed behavior Apply exists
// to provide.
func TestEveryDialectHasMigrations(t *testing.T) {
	for dialectName, src := range migrationSources {
		t.Run(dialectName, func(t *testing.T) {
			names, err := migrationNames(src)
			if err != nil {
				t.Fatalf("reading migrations for dialect %q: %v", dialectName, err)
			}
			if len(names) == 0 {
				t.Fatalf("dialect %q has no migration files, so Apply would bring a database to no schema at all", dialectName)
			}
		})
	}
}

// TestMigrationsAreContiguouslyNumbered proves each dialect's files are
// numbered from 0001 with no gaps and no duplicates.
//
// Apply orders migrations lexicographically and its own gate refuses a
// database whose recorded history has a hole in it, so a mis-numbered
// file on disk would either apply in the wrong order or wedge a database
// that had already applied its neighbors.
//
// Note that the dialects deliberately do NOT have to agree on a file
// count. SQLite accumulated four migrations before PostgreSQL existed
// here at all, and PostgreSQL starts from a single squashed initial
// migration, because ent can only ever diff against the schema it desires
// today and no PostgreSQL database has ever been migrated by this
// project. What must hold is that each set is internally well formed, and
// that both converge on the same ent schema, which is the integration
// test's claim.
func TestMigrationsAreContiguouslyNumbered(t *testing.T) {
	for dialectName, src := range migrationSources {
		t.Run(dialectName, func(t *testing.T) {
			names, err := migrationNames(src)
			if err != nil {
				t.Fatalf("reading migrations for dialect %q: %v", dialectName, err)
			}
			for i, name := range names {
				match := versionPattern.FindStringSubmatch(name)
				if match == nil {
					t.Fatalf("migration %q does not match the required NNNN_name.sql convention", name)
				}
				version, err := strconv.Atoi(match[1])
				if err != nil {
					t.Fatalf("parsing version from %q: %v", name, err)
				}
				if want := i + 1; version != want {
					t.Fatalf("migration %q is numbered %d but is at position %d; versions must run contiguously from 0001", name, version, want)
				}
			}
		})
	}
}

// TestMigrationDirectoriesHoldOnlySQL proves nothing but .sql files sits
// in a migration directory.
//
// Apply reads every non-directory entry it finds and executes it as SQL,
// so a stray README or editor backup file would be handed to the database
// as a statement.
func TestMigrationDirectoriesHoldOnlySQL(t *testing.T) {
	for dialectName, src := range migrationSources {
		t.Run(dialectName, func(t *testing.T) {
			entries, err := fs.ReadDir(src.fsys, src.dir)
			if err != nil {
				t.Fatalf("reading %q: %v", src.dir, err)
			}
			for _, e := range entries {
				if e.IsDir() {
					t.Fatalf("unexpected subdirectory %q in %q", e.Name(), src.dir)
				}
				if !versionPattern.MatchString(e.Name()) {
					t.Fatalf("unexpected non-migration file %q in %q; Apply would execute it as SQL", e.Name(), src.dir)
				}
			}
		})
	}
}

// TestEveryDialectRecordsVersionsParameterized proves each dialect
// carries its own version-record statement and that the statement is
// parameterized rather than built by interpolation.
//
// This exists because the placeholder spelling is genuinely
// dialect-specific and getting it wrong is close to invisible: lib/pq
// rejects "?" inside the same transaction as the migration's own DDL, so
// the DDL rolls back too and the failure reads as broken DDL rather than
// as a wrong placeholder.
func TestEveryDialectRecordsVersionsParameterized(t *testing.T) {
	// wantPlaceholders maps a dialect to the placeholder spelling its
	// driver accepts.
	wantPlaceholders := map[string]string{
		"sqlite3":  "VALUES (?, ?, ?)",
		"postgres": "VALUES ($1, $2, $3)",
	}

	for dialectName, src := range migrationSources {
		t.Run(dialectName, func(t *testing.T) {
			if src.insertVersion == "" {
				t.Fatalf("dialect %q records no version-insert statement", dialectName)
			}
			want, ok := wantPlaceholders[dialectName]
			if !ok {
				t.Fatalf("dialect %q has no expected placeholder spelling recorded in this test; add one", dialectName)
			}
			if got := src.insertVersion; !strings.Contains(got, want) {
				t.Fatalf("dialect %q records versions with %q, which does not use the expected parameterized form %q", dialectName, got, want)
			}
		})
	}

	// Every registered dialect must be covered above, so adding a dialect
	// without deciding its placeholder spelling fails here rather than at
	// the first migration against a real server.
	if len(wantPlaceholders) != len(migrationSources) {
		t.Fatalf("this test covers %d dialects but migrationSources has %d; every dialect needs an expected placeholder spelling", len(wantPlaceholders), len(migrationSources))
	}
}

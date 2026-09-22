// Structural proof that concurrent migration is settled by the database's own
// record of the work, and by nothing else.
package archtest

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// advisoryLockCall matches a call to any of PostgreSQL's advisory lock
// functions, and not a comment that merely names one.
var advisoryLockCall = regexp.MustCompile(`pg_(try_)?advisory_(xact_)?lock(_shared)?\s*\(`)

// This is Phase 84's Adversarial Pattern Justification made executable. The
// claim is that any number of controllers can migrate one database at once
// because each migration records its version FIRST, in its own transaction,
// and the version is the history table's primary key: the loser waits on the
// winner's record and then reads the history again (internal/ent/migrate's
// package comment). That is internal/tlscert's lose-then-reload reasoning,
// consumed rather than re-invented, with the transaction standing in for the
// rename.
//
// The tempting second mechanism is a lock: a PostgreSQL advisory lock, a NATS
// lease through internal/lock, or leader election. Each has a failure the
// design above does not (FAILURE_PATTERNS.md #134: one slow or dead holder
// stops everyone), none exists on SQLite, and the admin commands that also
// migrate have no NATS connection at all. These tests fail if one appears.

// TestMigrationsTakeNoLock fails if the migration runner imports a lock, an
// elector or a broker client, directly or through anything it imports.
func TestMigrationsTakeNoLock(t *testing.T) {
	pkgs := goList(t, false, modulePath+"/internal/ent/migrate")
	if len(pkgs) != 1 {
		t.Fatalf("go list found %d packages for internal/ent/migrate; the pattern is misaimed", len(pkgs))
	}
	forbidden := map[string]string{
		modulePath + "/internal/lock":     "a lock is a second mechanism beside the version key",
		modulePath + "/internal/election": "a leader is a lock with a longer name",
		"github.com/nats-io/nats.go":      "the admin commands that migrate have no broker connection",
	}
	// A control: the dependency list must hold something the package really
	// uses, or an empty list would pass every check below.
	sawSQL := false
	for _, dep := range pkgs[0].Deps {
		if dep == "database/sql" {
			sawSQL = true
		}
		if why, ok := forbidden[dep]; ok {
			t.Errorf("internal/ent/migrate depends on %s: %s", dep, why)
		}
	}
	if !sawSQL {
		t.Fatal("internal/ent/migrate's dependencies do not include database/sql; the list is not what this test thinks it is")
	}
}

// TestNothingTakesAnAdvisoryLock fails if any Go file outside the one
// benchmark that measures it calls a PostgreSQL advisory lock function.
func TestNothingTakesAnAdvisoryLock(t *testing.T) {
	root := moduleRoot(t)
	allowed := filepath.Join("internal", "lock", "pg_advisory_bench_test.go")
	found := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if path != root && (strings.HasPrefix(name, ".") || name == "testdata" || name == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		// This file names the function it forbids.
		if strings.HasSuffix(path, filepath.Join("internal", "archtest", "migrate_test.go")) {
			return nil
		}
		src, err := os.ReadFile(path) // #nosec G304 -- a file inside this module
		if err != nil {
			return err
		}
		if !advisoryLockCall.Match(src) {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		if rel == allowed {
			found++
			return nil
		}
		t.Errorf("%s calls a PostgreSQL advisory lock; migrations are serialized by the version key, not a lock", rel)
		return nil
	})
	if err != nil {
		t.Fatalf("walking the module: %v", err)
	}
	// A control: the one known use must be found, or the walk is misaimed.
	if found != 1 {
		t.Fatalf("found the advisory-lock benchmark %d times; want exactly once, or this test is not looking where it thinks", found)
	}
}

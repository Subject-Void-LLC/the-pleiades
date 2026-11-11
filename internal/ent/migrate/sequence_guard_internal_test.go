// This file guards the SQLite migrations against resetting an AUTOINCREMENT
// sequence.
//
// The generator writes a SQLite column change as a table rebuild: create
// new_<table>, copy the rows with their ids, drop <table>, rename. Dropping
// the table drops its sqlite_sequence row, so the rebuilt table's next id is
// one past the highest id it still holds, not past the highest it ever
// handed out: the id of the most recently deleted row is given out again. A
// reference with no foreign key to that table (a role binding's scope_id)
// then attaches to the new row. 0046 was written as one before this guard.
package migrate

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// firstGuardedSQLiteMigration is the first SQLite migration held to the
// guard. The rebuilds before it (0003, 0007, 0022 among them) shipped before
// the defect was found; pre-1.0 nothing is deployed that they could still
// reach, and a migration once written is never edited.
const firstGuardedSQLiteMigration = 46

// rebuildPattern finds the rename that ends a table rebuild.
var rebuildPattern = regexp.MustCompile("(?i)ALTER TABLE `?new_[a-z_]+`? RENAME TO")

// TestSQLiteMigrations_ARebuildKeepsItsSequence refuses a guarded SQLite
// migration that rebuilds a table and does not write sqlite_sequence: such a
// migration must carry the dropped table's sequence across, or be written as
// an ALTER TABLE that keeps the table.
func TestSQLiteMigrations_ARebuildKeepsItsSequence(t *testing.T) {
	dir := filepath.Join("migrations", "sqlite")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}
	checked := 0
	for _, e := range entries {
		number, err := strconv.Atoi(strings.SplitN(e.Name(), "_", 2)[0])
		if err != nil || number < firstGuardedSQLiteMigration || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		checked++
		body, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("reading %s: %v", e.Name(), err)
		}
		if rebuildPattern.Match(body) && !strings.Contains(string(body), "sqlite_sequence") {
			t.Errorf("%s rebuilds a table without carrying its sqlite_sequence across: "+
				"the most recently deleted id would be reused; write it as ALTER TABLE, or save and restore the sequence",
				e.Name())
		}
	}
	if checked == 0 {
		t.Fatal("no guarded SQLite migration was found, so the guard checked nothing")
	}
}

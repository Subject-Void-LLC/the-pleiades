// This file holds the type lock (type_inputs.go) to what it is for on
// SQLite, through internal/credstore/locktest, whose own test runs the same
// two checks on PostgreSQL. Kept here, container-free, so the fast test
// tiers run them.
package credstore_test

import (
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credstore/locktest"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credstore/resolve"
)

// TestTypeEditsRacingCredentialWritesNeverShowASecret_SQLite races them.
func TestTypeEditsRacingCredentialWritesNeverShowASecret_SQLite(t *testing.T) {
	store, orgID := locktest.HookedStore(t, locktest.OpenSQLite(t))
	locktest.RaceTypeEditsAgainstWrites(t, store, orgID, 3*time.Second)
}

// TestACredentialWriteHoldsTypeEditsOff_SQLite lands one inside the other.
func TestACredentialWriteHoldsTypeEditsOff_SQLite(t *testing.T) {
	store, orgID := locktest.HookedStore(t, locktest.OpenSQLite(t))
	locktest.HoldTypeEditsOff(t, store, orgID)
}

// TestReadsTakeOneSnapshot_SQLite lands a type edit between a reader's two
// reads. SQLite in WAL mode gives each statement outside a transaction its
// own snapshot, so the gap is real here too.
func TestReadsTakeOneSnapshot_SQLite(t *testing.T) {
	client := locktest.OpenSQLite(t)
	locktest.InterceptTypeReads(client)
	store, orgID := locktest.HookedStore(t, client)
	locktest.ReadsTakeOneSnapshot(t, store, orgID, locktest.Readers(client, store, orgID, resolve.NewEntResolver(client)))
}

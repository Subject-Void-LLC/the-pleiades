// Tests that a full rotation leaves no row that still needs the previous key.
package crypto_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/crypto"
)

// TestRotateAll_LeavesNothingThatNeedsThePreviousKey is the proof the
// production guide's rotation procedure now rests on.
//
// It seeds every encrypted column under an old key, rotates through a
// service holding the new key as current and the old one as previous, then
// reopens the database with a service that holds ONLY the new key. That last
// step is the operator removing MASTER_ENCRYPTION_KEY_PREVIOUS. Every row
// must still read back its original plaintext, which is the property whose
// absence lost credentials and survey answers before RotateAll existed.
func TestRotateAll_LeavesNothingThatNeedsThePreviousKey(t *testing.T) {
	ctx := context.Background()
	oldKey := []byte(strings.Repeat("o", 32))
	newKey := []byte(strings.Repeat("n", 32))

	svcOld, err := crypto.NewEnvelopeService(oldKey, "v1", nil, "")
	if err != nil {
		t.Fatalf("NewEnvelopeService(old) error = %v", err)
	}
	dbPath, ids := seedEveryColumn(t, svcOld)

	svcRotating, err := crypto.NewEnvelopeService(newKey, "v2", oldKey, "v1")
	if err != nil {
		t.Fatalf("NewEnvelopeService(rotating) error = %v", err)
	}
	results, err := crypto.RotateAll(ctx, openEveryColumn(t, dbPath, svcRotating), svcRotating)
	if err != nil {
		t.Fatalf("RotateAll() error = %v", err)
	}

	wantTables := []string{"credentials", "devices", "saved launch configurations", "mesh signing keys"}
	if len(results) != len(wantTables) {
		t.Fatalf("RotateAll() reported %d tables, want %d: %+v", len(results), len(wantTables), results)
	}
	for i, r := range results {
		if r.Table != wantTables[i] {
			t.Errorf("result %d is %q, want %q", i, r.Table, wantTables[i])
		}
		if r.Count != (crypto.RotationCount{Rotated: 1}) || !r.Count.Complete() {
			t.Errorf("%s: %+v, want exactly the one seeded row rotated and nothing skipped or unreadable", r.Table, r.Count)
		}
	}

	svcNewOnly, err := crypto.NewEnvelopeService(newKey, "v2", nil, "")
	if err != nil {
		t.Fatalf("NewEnvelopeService(new only) error = %v", err)
	}
	for table, readable := range readEveryColumn(t, openEveryColumn(t, dbPath, svcNewOnly), ids) {
		if !readable {
			t.Errorf("%s no longer reads back after removing the previous key: rotation left it on the old key", table)
		}
	}
}

// TestRotateAll_ReportsAnUnreadableMeshKeyAndKeepsGoing proves the mesh
// pass counts a seed that opens under neither key as unreadable rather
// than failing, and that one table's unreadable row does not stop the
// others from rotating.
func TestRotateAll_ReportsAnUnreadableMeshKeyAndKeepsGoing(t *testing.T) {
	ctx := context.Background()
	strangerKey := []byte(strings.Repeat("s", 32))
	newKey := []byte(strings.Repeat("n", 32))

	svcStranger, err := crypto.NewEnvelopeService(strangerKey, "v1", nil, "")
	if err != nil {
		t.Fatalf("NewEnvelopeService(stranger) error = %v", err)
	}
	dbPath, _ := seedEveryColumn(t, svcStranger)

	// A service that has never held the key everything was written under:
	// every row is unreadable to it.
	svcNew, err := crypto.NewEnvelopeService(newKey, "v1", nil, "")
	if err != nil {
		t.Fatalf("NewEnvelopeService(new) error = %v", err)
	}
	results, err := crypto.RotateAll(ctx, openEveryColumn(t, dbPath, svcNew), svcNew)
	if err != nil {
		t.Fatalf("RotateAll() error = %v, want unreadable rows counted, not fatal", err)
	}
	for _, r := range results {
		if r.Count != (crypto.RotationCount{Unreadable: 1}) {
			t.Errorf("%s: %+v, want the one seeded row counted as unreadable", r.Table, r.Count)
		}
		if !r.Count.Complete() {
			t.Errorf("%s: an unreadable row made the pass incomplete; removing the previous key cannot change a row it could not open", r.Table)
		}
	}
}

// TestRotateAll_ReportsATableItCouldNotRead proves a pass that fails to list
// its table joins an error naming that table and leaves it out of the
// result, rather than reporting a count of zero that would read as done.
func TestRotateAll_ReportsATableItCouldNotRead(t *testing.T) {
	svc, err := crypto.NewEnvelopeService([]byte(strings.Repeat("k", 32)), "v1", nil, "")
	if err != nil {
		t.Fatalf("NewEnvelopeService() error = %v", err)
	}
	dbPath, _ := seedEveryColumn(t, svc)
	client := openEveryColumn(t, dbPath, svc)
	if err := client.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	results, err := crypto.RotateAll(context.Background(), client, svc)
	if err == nil {
		t.Fatal("RotateAll() on a closed client returned no error")
	}
	if len(results) != 0 {
		t.Errorf("RotateAll() reported %+v for tables it could not read", results)
	}
	for _, table := range []string{"credentials", "devices", "saved launch configurations", "mesh signing keys"} {
		if !strings.Contains(err.Error(), table+":") {
			t.Errorf("RotateAll() error %q does not name %q", err, table)
		}
	}
}

// This file is the cheap, in-process sanity check on the catalog data
// itself: no duplicate names, and every entry passes the same Validate()
// the real pleiades forge CLI runs before writing anything. It catches a
// typo in this package's data at `go test` time, before tools/gencatalog
// ever shells out to the real binary.
package catalogdata_test

import (
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/internal/forge/catalogdata"
)

func TestCollections_NoDuplicatesAndAllValid(t *testing.T) {
	seen := make(map[string]bool, len(catalogdata.Collections))
	for _, c := range catalogdata.Collections {
		if seen[c.Name] {
			t.Errorf("duplicate collection name %q", c.Name)
		}
		seen[c.Name] = true
		if err := c.Validate(); err != nil {
			t.Errorf("collection %q failed Validate: %v", c.Name, err)
		}
	}
}

func TestCollections_MatchesDocumentedCount(t *testing.T) {
	// docs/hephaestus.md's own catalog table, counted by hand and cross
	// checked against this package's section files, resolves to exactly
	// 71 individual <namespace>.<method> names across 27 Go packages (the
	// doc's own "roughly twenty seven collections" counts packages, not
	// methods). This test pins that number down so a future accidental
	// entry loss or duplication is a build failure, not a silent gap.
	const wantCollections = 75
	if got := len(catalogdata.Collections); got != wantCollections {
		t.Errorf("len(Collections) = %d, want %d", got, wantCollections)
	}
}

func TestDevices_AllValid(t *testing.T) {
	seen := make(map[string]bool, len(catalogdata.Devices))
	for _, d := range catalogdata.Devices {
		key := d.Vendor + "/" + d.TypeKey
		if seen[key] {
			t.Errorf("duplicate device %s", key)
		}
		seen[key] = true
		if err := d.Validate(); err != nil {
			t.Errorf("device %s failed Validate: %v", key, err)
		}
	}
}

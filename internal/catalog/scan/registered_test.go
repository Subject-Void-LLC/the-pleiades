// Tests for what the scan methods declare.
package scan_test

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// TestRegistered declares what the design note promises: vuln and secrets
// only read, so their check is the task; sbom writes, and its undo is the
// removal of the directory it made.
func TestRegistered(t *testing.T) {
	for name, want := range map[string]struct{ readOnly, reversible bool }{
		"scan.vuln": {true, false}, "scan.sbom": {false, true}, "scan.secrets": {true, false},
	} {
		d, ok := collection.Lookup(name)
		if !ok {
			t.Fatalf("%s is not registered", name)
		}
		r := d.Manifest.Reversibility
		if r.ReadOnly != want.readOnly || r.Reversible != want.reversible || !d.Manifest.SupportsCheck || d.Check == nil {
			t.Errorf("%s declares %+v", name, r)
		}
	}
	d, _ := collection.Lookup("scan.sbom")
	if inv := d.Manifest.Reversibility.Inverses; len(inv) != 1 || inv[0].FQCN != "file.remove" {
		t.Errorf("scan.sbom's undo = %+v", inv)
	}
}

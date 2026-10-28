// Phase 131's R3 for the edge.* methods: every stat a read records is one
// its Returns declares, so a register can be written from pleiades doc.
package edge_test

import (
	"context"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// TestEdge_EveryStatIsDeclared runs each method against the fixture
// account and holds every stat it set to its Returns, and reads the access
// application's name, type and session duration back.
func TestEdge_EveryStatIsDeclared(t *testing.T) {
	_, dev := fixture(t)
	for fqcn, params := range map[string]map[string]any{
		"edge.tunnel": {"tunnel": "prod"},
		"edge.access": {"app": app},
		"edge.dns":    {"zone": "example.com", "name": "www.example.com", "type": "A"},
	} {
		d, ok := collection.Lookup(fqcn)
		if !ok {
			t.Fatalf("%s is not registered", fqcn)
		}
		rc := newCtx()
		if _, err := d.Invoke(context.Background(), rc, dev, params); err != nil {
			t.Fatalf("%s: %v", fqcn, err)
		}
		declared := map[string]bool{}
		for _, r := range d.Manifest.Doc.Returns {
			declared[r.Name] = true
		}
		for k := range rc.stats {
			if !declared[k] {
				t.Errorf("%s records %s, which its Returns does not declare", fqcn, k)
			}
		}
		switch fqcn {
		case "edge.access":
			if rc.stats["name"] != "Admin" || rc.stats["type"] != "self_hosted" || rc.stats["session_duration"] != "24h" {
				t.Errorf("access stats = %v", rc.stats)
			}
		case "edge.dns":
			if rc.stats["zone_id"] != zone || rc.stats["type"] != "A" || rc.stats["name"] != "www.example.com" {
				t.Errorf("dns stats = %v", rc.stats)
			}
		}
	}
}

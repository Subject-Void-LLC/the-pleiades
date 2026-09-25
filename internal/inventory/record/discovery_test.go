// Tests that the reserved discovery property is written by RecordDiscovery
// alone, with a revision, and never through AddInfo or RemoveInfo.
package record_test

import (
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// TestDiscoveredProperty_OnlyRecordDiscoveryWritesIt refuses the property
// through the ordinary mutators, and records it with a revision through
// the one that exists for it.
func TestDiscoveredProperty_OnlyRecordDiscoveryWritesIt(t *testing.T) {
	b := record.NewBase(record.Record{ID: "d1", Name: "d1", Type: "t"}, nil)
	if err := b.AddInfo(inventory.DiscoveredProperty, map[string]any{"capabilities": []any{"AptCapable"}}, true); err == nil {
		t.Error("AddInfo wrote the discovery")
	}
	if err := b.RemoveInfo(inventory.DiscoveredProperty); err == nil {
		t.Error("RemoveInfo removed the discovery")
	}
	if b.Version() != 0 {
		t.Fatalf("a refused write bumped the version to %d", b.Version())
	}

	d := inventory.Discovery{Protocol: "ssh", Capabilities: []capability.Name{capability.NameApt}, ProbedAt: time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)}
	b.RecordDiscovery(d)
	got, ok, err := inventory.DiscoveryFrom(b.Properties())
	if err != nil || !ok || got.Protocol != "ssh" || len(got.Capabilities) != 1 || !got.ProbedAt.Equal(d.ProbedAt) {
		t.Fatalf("read back %+v, %v, %v", got, ok, err)
	}
	if h := b.History(); b.Version() != 1 || len(h) != 1 || h[0].Field != inventory.DiscoveredProperty {
		t.Errorf("version %d, history %+v", b.Version(), h)
	}
}

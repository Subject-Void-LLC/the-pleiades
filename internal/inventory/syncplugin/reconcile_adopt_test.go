// Package syncplugin_test: a hand-added device is a conflict a sync leaves
// alone unless Config.Adopt names it, and an adoption records its new
// owner even when nothing else about the device moved.
package syncplugin_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	inv "github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/syncplugin"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// handAdded is a store holding web1 as add-host writes it: no source.
func handAdded(t *testing.T) inv.Repository {
	t.Helper()
	path := filepath.Join(t.TempDir(), "inventory.yaml")
	if err := inv.WriteHosts(path, []inv.HostSpec{{ID: "hand-web1", Name: "web1", Type: "linux_server", Properties: map[string]interface{}{"host": "10.0.0.1"}}}); err != nil {
		t.Fatal(err)
	}
	return inv.NewFileRepository(path, inv.NewItemFactory())
}

func TestReconcile_AdoptsAHandAddedDeviceOnlyByName(t *testing.T) {
	ctx := context.Background()
	repo := handAdded(t)
	// The upstream reports exactly what is stored: only the owner can move.
	p := &scriptedPlugin{records: []record.Record{hostRecord("web1", map[string]inventory.PropertyValue{"host": "10.0.0.1"})}}

	report := reconcile(t, p, repo)
	if len(report.Results) != 1 || report.Results[0].Outcome != syncplugin.OutcomeConflict || !strings.Contains(report.Results[0].Reason, "--adopt web1") {
		t.Fatalf("a hand-added device: %+v", report.Results)
	}
	// Another name in Adopt is not this one.
	other, err := syncplugin.Reconcile(ctx, p, syncplugin.Config{Name: "scripted", Adopt: []string{"web2", "WEB1"}}, repo, inv.NewItemFactory())
	if err != nil || other.Count(syncplugin.OutcomeConflict) != 1 {
		t.Fatalf("adopting other names: %+v, %v", other.Results, err)
	}
	if item, _ := repo.GetByName(ctx, "web1"); item.Source().Plugin != "" {
		t.Fatalf("the device was taken over without being named: %q", item.Source().Plugin)
	}

	adopted, err := syncplugin.Reconcile(ctx, p, syncplugin.Config{Name: "scripted", Adopt: []string{"web1"}}, repo, inv.NewItemFactory())
	if err != nil || adopted.Count(syncplugin.OutcomeUpdated) != 1 {
		t.Fatalf("adopting web1: %+v, %v", adopted.Results, err)
	}
	item, err := repo.GetByName(ctx, "web1")
	if err != nil {
		t.Fatal(err)
	}
	if item.Source().Plugin != "scripted" {
		t.Errorf("the adopted device is owned by %q, want scripted, though nothing else moved", item.Source().Plugin)
	}
	// Owned now, it syncs as any device the plugin added, with no adoption.
	if got := reconcile(t, p, repo).Count(syncplugin.OutcomeUnchanged); got != 1 {
		t.Errorf("a sync after the adoption: %d unchanged, want 1", got)
	}
	// A read-only inventory reports the adoption as a would-update and
	// writes nothing.
	ro := handAdded(t)
	report, err = syncplugin.Reconcile(ctx, p, syncplugin.Config{Name: "scripted", Adopt: []string{"web1"}}, inv.NewReadOnlyRepository(ro), inv.NewItemFactory())
	if err != nil || report.Count(syncplugin.OutcomeWouldUpdate) != 1 {
		t.Errorf("a read-only adoption: %+v, %v", report.Results, err)
	}
	if item, _ := ro.GetByName(ctx, "web1"); item.Source().Plugin != "" {
		t.Errorf("a read-only adoption wrote the owner %q", item.Source().Plugin)
	}
}

func TestConfig_Adopts(t *testing.T) {
	c := syncplugin.Config{Adopt: []string{"a", "b"}}
	if !c.Adopts("b") || c.Adopts("c") || c.Adopts("") || (syncplugin.Config{}).Adopts("a") {
		t.Error("Adopts answers wrong")
	}
}

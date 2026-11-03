// Tests of the built-in catalog a binary that registers no Collection
// installs (catalog.go).
package collection_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

func TestInstallCatalog_AnswersWhatTheRegistryDoesNot(t *testing.T) {
	t.Cleanup(collection.SnapshotForTest())
	t.Cleanup(collection.CatalogForTest())
	if err := collection.InstallCatalog([]byte("not json")); err == nil {
		t.Error("a catalog that does not decode was installed")
	}
	if err := collection.InstallCatalog([]byte("{}")); err == nil {
		t.Error("an empty catalog was installed")
	}
	if _, ok := collection.BuiltinManifest("catalogtest.only"); ok {
		t.Fatal("a method was found before any catalog was installed")
	}
	raw, err := json.Marshal(map[string]collection.Manifest{
		"catalogtest.only":     {Status: collection.StatusImplemented, Reversibility: collection.Reversibility{ReadOnly: true, Notes: "from the catalog"}},
		"catalogtest.external": {Status: collection.StatusImplemented},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := collection.InstallCatalog(raw); err != nil {
		t.Fatal(err)
	}
	if err := collection.InstallCatalog(raw); !errors.Is(err, collection.ErrCatalogInstalled) {
		t.Errorf("a second install: %v", err)
	}
	if m, ok := collection.BuiltinManifest("catalogtest.only"); !ok || m.Reversibility.Notes != "from the catalog" {
		t.Errorf("BuiltinManifest from the catalog = %+v, %v", m, ok)
	}
	if m, ok := collection.ManifestOf("catalogtest.only"); !ok || !m.Reversibility.ReadOnly {
		t.Errorf("ManifestOf from the catalog = %+v, %v", m, ok)
	}
	if _, ok := collection.ManifestOf("catalogtest.absent"); ok {
		t.Error("ManifestOf found a method nothing knows")
	}

	// The registry answers first: a registered external Collection's method
	// is known but never built-in, whatever the catalog says.
	invoke := func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (collection.Result, error) {
		return collection.Result{}, nil
	}
	if err := collection.Register(collection.Descriptor{Name: "catalogtest.external", Invoke: invoke, Provider: &collection.Provider{Program: "prog", Digest: "d"},
		Manifest: collection.Manifest{Status: collection.StatusImplemented, Reversibility: collection.Reversibility{Notes: "external"}}}); err != nil {
		t.Fatal(err)
	}
	if _, ok := collection.BuiltinManifest("catalogtest.external"); ok {
		t.Error("an external Collection's method read as built-in")
	}
	if m, ok := collection.ManifestOf("catalogtest.external"); !ok || m.Reversibility.Notes != "external" {
		t.Errorf("ManifestOf of a registered method = %+v, %v; want the registry's", m, ok)
	}
}

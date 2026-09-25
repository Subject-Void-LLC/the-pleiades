// Tests that the file inventory keeps a discovery in its generated state
// file, refuses one written into hosts.yaml by hand, and starts a generic
// device discovered.
package inventory

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/generic"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// TestFileRepository_RefusesAHandWrittenDiscovery: a hosts.yaml host that
// names the discovery property does not load, and neither does the read-
// only path, and each refusal names the property.
func TestFileRepository_RefusesAHandWrittenDiscovery(t *testing.T) {
	path := filepath.Join(t.TempDir(), DefaultInventoryFilename)
	body := "hosts:\n  - id: h1\n    name: edge1\n    type: generic_ssh\n    properties:\n      host: 10.0.0.1\n      discovered:\n        capabilities: [AptCapable]\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	repo := NewFileRepository(path, NewItemFactory())
	if _, err := repo.GetByName(context.Background(), "edge1"); err == nil || !strings.Contains(err.Error(), inventory.DiscoveredProperty) {
		t.Fatalf("err %v, want a refusal naming %s", err, inventory.DiscoveredProperty)
	}
	hosts, err := ParseHosts([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := HydrateHosts(NewItemFactory(), hosts); err == nil || !strings.Contains(err.Error(), inventory.DiscoveredProperty) {
		t.Fatalf("read-only path: err %v", err)
	}
}

// TestFileRepository_GenericDeviceStartsDiscovered: a generic host with no
// saved state is discovered, which runs nothing; a vendor host is active.
func TestFileRepository_GenericDeviceStartsDiscovered(t *testing.T) {
	path := filepath.Join(t.TempDir(), DefaultInventoryFilename)
	body := "hosts:\n  - id: h1\n    name: edge1\n    type: generic_ssh\n    properties: {host: 10.0.0.1}\n  - id: h2\n    name: web1\n    type: linux_server\n    properties: {host: 10.0.0.2}\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	repo := NewFileRepository(path, NewItemFactory())
	for name, want := range map[string]inventory.LifecycleState{"edge1": inventory.StateDiscovered, "web1": inventory.StateActive} {
		item, err := repo.GetByName(context.Background(), name)
		if err != nil {
			t.Fatal(err)
		}
		if item.State() != want {
			t.Errorf("%s is %s, want %s", name, item.State(), want)
		}
	}
}

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

// granter is the one write path a grant has (record.Base.RecordGrant).
type granter interface {
	RecordGrant(inventory.Grant) error
}

// TestFileRepository_KeepsAGrantInTheStateFile: a grant is saved to the
// generated state file, never to hosts.yaml, survives a reload, and one
// written into hosts.yaml by hand is refused, naming the property.
func TestFileRepository_KeepsAGrantInTheStateFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, DefaultInventoryFilename)
	if err := os.WriteFile(path, []byte("hosts:\n  - id: h2\n    name: web1\n    type: linux_server\n    properties: {host: 10.0.0.2}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	repo := NewFileRepository(path, NewItemFactory())
	item, err := repo.GetByName(context.Background(), "web1")
	if err != nil {
		t.Fatal(err)
	}
	g, ok := item.(granter)
	if !ok {
		t.Fatalf("%T has no RecordGrant", item)
	}
	if err := g.RecordGrant(inventory.Grant{Capability: "acme.widget_api", Source: inventory.GrantAsserted, By: "ops", Reason: "lab", Properties: map[string]any{"endpoint": "https://w"}}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Save(context.Background(), item); err != nil {
		t.Fatalf("Save: %v", err)
	}
	hosts, _ := os.ReadFile(path) // #nosec G304 -- a path under this test's own directory
	if strings.Contains(string(hosts), inventory.GrantedProperty) {
		t.Errorf("hosts.yaml holds the grant:\n%s", hosts)
	}
	reloaded, err := NewFileRepository(path, NewItemFactory()).GetByName(context.Background(), "web1")
	if err != nil {
		t.Fatal(err)
	}
	grants, _, err := inventory.GrantsFrom(reloaded.Properties())
	if err != nil || len(grants) != 1 || grants[0].Capability != "acme.widget_api" || grants[0].Reason != "lab" {
		t.Fatalf("after a reload: grants %+v, %v", grants, err)
	}

	byHand := filepath.Join(t.TempDir(), DefaultInventoryFilename)
	if err := os.WriteFile(byHand, []byte("hosts:\n  - id: h3\n    name: web2\n    type: linux_server\n    properties:\n      host: 10.0.0.3\n      granted:\n        acme.widget_api: {source: assertion}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewFileRepository(byHand, NewItemFactory()).GetByName(context.Background(), "web2"); err == nil || !strings.Contains(err.Error(), inventory.GrantedProperty) {
		t.Fatalf("a hand-written grant: err %v, want a refusal naming %s", err, inventory.GrantedProperty)
	}
}

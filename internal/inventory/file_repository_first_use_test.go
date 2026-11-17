// Tests that the file inventory keeps a pin recorded on first use in its
// generated state file, never in hosts.yaml; that a person's confirmation
// writes the pin into hosts.yaml and keeps the device's discovery; and that a
// hosts.yaml copied without its state file trusts no first-use pin at all.
package inventory

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/generic"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// firstUser is the writers of a first-use pin a stored item has
// (record.Base).
type firstUser interface {
	RecordFirstUsePin(string)
	ConfirmFirstUsePin(pin string) error
	RecordDiscovery(inventory.Discovery)
	DiscoveryBinding() string
}

// TestFileRepository_KeepsAFirstUsePinInTheStateFile follows a pin from
// onboarding's record of it to a person's confirmation, through real saves
// and reloads of a generic_http device.
func TestFileRepository_KeepsAFirstUsePinInTheStateFile(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, DefaultInventoryFilename)
	if err := os.WriteFile(path, []byte("hosts:\n  - id: h1\n    name: api1\n    type: generic_http\n    properties: {base_url: \"https://10.0.0.9\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	repo := NewFileRepository(path, NewItemFactory())
	item, err := repo.GetByName(ctx, "api1")
	if err != nil {
		t.Fatal(err)
	}
	pin := "sha256:" + strings.Repeat("ab", 32)
	w := item.(firstUser)
	w.RecordFirstUsePin(pin)
	w.RecordDiscovery(inventory.Discovery{Protocol: "http", Capabilities: []capability.Name{capability.NameHTTPAPI}, ProbedAt: time.Now(), Binding: w.DiscoveryBinding()})
	if err := repo.Save(ctx, item); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if hosts, _ := os.ReadFile(path); strings.Contains(string(hosts), inventory.TLSFirstUseProperty) { // #nosec G304 -- this test's own file
		t.Errorf("hosts.yaml holds the first-use pin:\n%s", hosts)
	}

	reloaded, err := NewFileRepository(path, NewItemFactory()).GetByName(ctx, "api1")
	if err != nil {
		t.Fatal(err)
	}
	if got := devicetls.For(reloaded).FirstUsePin(); got != pin || !reloaded.HasCapability(capability.NameHTTPAPI) {
		t.Fatalf("after a reload the device trusts first-use pin %q and holds HTTPAPI %v", got, reloaded.HasCapability(capability.NameHTTPAPI))
	}

	// A hosts.yaml carried to another project without its state file: no
	// pin at all, so the device is refused, never trusted as if checked.
	copied := filepath.Join(t.TempDir(), DefaultInventoryFilename)
	body, _ := os.ReadFile(path) // #nosec G304 -- this test's own file
	if err := os.WriteFile(copied, body, 0o600); err != nil {
		t.Fatal(err)
	}
	elsewhere, err := NewFileRepository(copied, NewItemFactory()).GetByName(ctx, "api1")
	if err != nil {
		t.Fatal(err)
	}
	if s := devicetls.For(elsewhere); s.Pins() != nil || s.FirstUsePin() != "" {
		t.Errorf("a copied hosts.yaml trusts %q", s.Pins())
	}

	if err := reloaded.(firstUser).ConfirmFirstUsePin(pin); err != nil {
		t.Fatal(err)
	}
	if err := NewFileRepository(path, NewItemFactory()).Save(ctx, reloaded); err != nil {
		t.Fatalf("Save after confirming: %v", err)
	}
	hosts, _ := os.ReadFile(path) // #nosec G304 -- this test's own file
	if !strings.Contains(string(hosts), "tls_fingerprint: "+pin) {
		t.Errorf("the confirmed pin is not in hosts.yaml:\n%s", hosts)
	}
	confirmed, err := NewFileRepository(path, NewItemFactory()).GetByName(ctx, "api1")
	if err != nil {
		t.Fatal(err)
	}
	s := devicetls.For(confirmed)
	if !slices.Equal(s.Pins(), []string{pin}) || s.FirstUsePin() != "" || !confirmed.HasCapability(capability.NameHTTPAPI) {
		t.Errorf("confirmed: pins %q, first use %q, HTTPAPI %v; want the pin a person's, and the discovery kept", s.Pins(), s.FirstUsePin(), confirmed.HasCapability(capability.NameHTTPAPI))
	}
	if _, present := confirmed.Properties().Raw()[inventory.TLSFirstUseProperty]; present {
		t.Error("the first-use pin outlived its confirmation")
	}
}

// TestFileRepository_RefusesAHandWrittenFirstUsePin: a hosts.yaml host that
// names the first-use pin does not load, and the refusal names it, so no
// person writes a pin that a run reads as onboarding's.
func TestFileRepository_RefusesAHandWrittenFirstUsePin(t *testing.T) {
	path := filepath.Join(t.TempDir(), DefaultInventoryFilename)
	body := "hosts:\n  - id: h1\n    name: api1\n    type: " + generic.TypeHTTP + "\n    properties:\n      base_url: \"https://10.0.0.9\"\n      " + inventory.TLSFirstUseProperty + ": sha256:" + strings.Repeat("ab", 32) + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewFileRepository(path, NewItemFactory()).GetByName(context.Background(), "api1"); err == nil || !strings.Contains(err.Error(), inventory.TLSFirstUseProperty) {
		t.Fatalf("err %v, want a refusal naming %s", err, inventory.TLSFirstUseProperty)
	}
}

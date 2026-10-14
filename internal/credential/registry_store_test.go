// Tests for the registry accounts kept in the credential file's own
// registries: section, and the lookup a run makes by registry host.
package credential

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
)

// TestRegistryStore_RoundTripsAndStaysApartFromDevices: an account saved for
// a registry is found by that registry's host in any spelling, is encrypted
// on disk, and is not a device's credential even when a device bears the
// same name, nor does a device's credential answer for a registry.
func TestRegistryStore_RoundTripsAndStaysApartFromDevices(t *testing.T) {
	dir := t.TempDir()
	key := bytes.Repeat([]byte{7}, 32)
	if err := SaveRegistryCredential(dir, key, "https://GHCR.io/", "deployer", "registry-secret"); err != nil {
		t.Fatal(err)
	}
	if err := SaveFileStore(dir, key, "ghcr.io", Credential{Username: "root", Password: "device-secret"}); err != nil {
		t.Fatal(err)
	}
	store, err := NewFileStore(dir, key)
	if err != nil {
		t.Fatal(err)
	}
	rs := store.(RegistryStore)
	user, password, ok, err := rs.LookupRegistry(context.Background(), "ghcr.io")
	if err != nil || !ok || user != "deployer" || password != "registry-secret" {
		t.Fatalf("LookupRegistry = %q, %q, %v, %v", user, password, ok, err)
	}
	dev, err := store.Lookup(context.Background(), "ghcr.io")
	if err != nil || dev.Password != "device-secret" {
		t.Errorf("the device named ghcr.io got %+v, %v; want its own credential", dev, err)
	}
	if _, _, ok, _ := rs.LookupRegistry(context.Background(), "quay.io"); ok {
		t.Error("an unstored registry answered")
	}
	raw, err := os.ReadFile(credentialsPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "registry-secret") {
		t.Error("the registry password is in the credential file in the clear")
	}
}

// TestLazyStore_LookupRegistry resolves the key on first use, as Lookup
// does.
func TestLazyStore_LookupRegistry(t *testing.T) {
	dir := t.TempDir()
	key, err := ResolveMasterKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := SaveRegistryCredential(dir, key, "docker.io", "deployer", "hub-token"); err != nil {
		t.Fatal(err)
	}
	rs := NewLazyFileStore(dir).(RegistryStore)
	if _, password, ok, err := rs.LookupRegistry(context.Background(), "index.docker.io"); err != nil || !ok || password != "hub-token" {
		t.Errorf("LookupRegistry(Docker Hub alias) = %q, %v, %v", password, ok, err)
	}
}

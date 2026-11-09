// Package native: that a dispatch's login seed material never reaches a
// method on a Runner (seeds.go).
package native

import (
	"context"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// addressedStub is a stub that reports an address of its own, as a device
// declaring NetworkAddressableCapable does.
type addressedStub struct {
	*inventorytest.Stub
}

// IPAddress returns the stub's address.
func (addressedStub) IPAddress() string { return "192.0.2.10" }

// seedFinder finds the seeder test's devices by name.
type seedFinder map[string]inventory.InventoryItem

// GetByName returns the device named name.
func (f seedFinder) GetByName(_ context.Context, name string) (inventory.InventoryItem, error) {
	return f[name], nil
}

// seedKeysTheEngineWrites returns every key the engine's own login seeder
// (engine.NewCredentialLoginSeeder, the one writer of seed material) puts in
// a seed, over each form it writes: a machine reached by an SSH key, one
// reached over WinRM by a password, and a Cisco IOS device with an address
// and an enable password. A seed key added to any of them is in this set,
// whatever it is named.
func seedKeysTheEngineWrites(t *testing.T) []string {
	t.Helper()
	cred, err := credential.Generate("admin", "seed-test")
	if err != nil {
		t.Fatal(err)
	}
	flat := credential.Flatten(cred)
	flat[wire.SecretEnablePassword] = "enable-it"
	stub := func(name string, caps ...capability.Name) *inventorytest.Stub {
		return &inventorytest.Stub{StubID: inventory.DeviceID(name), StubName: name, StubState: inventory.StateActive, Caps: caps}
	}
	devices := seedFinder{
		"ssh-box": stub("ssh-box", capability.NameSSHTransport),
		"win-box": stub("win-box", capability.NameWinRM),
		"ios-box": addressedStub{stub("ios-box", capability.NameCiscoIOS, capability.NameNetworkAddressable)},
	}
	seeder := engine.NewCredentialLoginSeeder(credential.NewStaticStore(flat), devices)
	keys := map[string]bool{}
	for name := range devices {
		seed, err := seeder(context.Background(), name, engine.SeedForms{Password: true, IOSSecret: true})
		if err != nil {
			t.Fatalf("seeding %s: %v", name, err)
		}
		for key := range seed {
			keys[key] = true
		}
	}
	// The forms above reach every branch: a key, a password hash, a
	// password, the IOS secrets and the address.
	if len(keys) < 7 {
		t.Fatalf("the seeder wrote only %v, so a form was not reached", slices.Sorted(maps.Keys(keys)))
	}
	return slices.Sorted(maps.Keys(keys))
}

// TestAdapter_Execute_DropsDeliveredSeeds runs a dispatch through the
// adapter's real Execute whose secrets carry every key the engine's seeder
// writes beside a password: the method receives the password and none of
// them, since only the engine derives a seed, for a method whose manifest
// asks for one, and on a Runner it derives none. Every such key carries the
// prefix the Runner drops, so a seed key added without it fails here. The
// method is an external program's, which runs in this process and reads the
// payload's secrets directly (a built-in's child is handed the same
// payload). The caller's map is left as it was.
func TestAdapter_Execute_DropsDeliveredSeeds(t *testing.T) {
	seedKeys := seedKeysTheEngineWrites(t)
	t.Cleanup(collection.SnapshotForTest())
	var seen map[string]string
	method := func(_ context.Context, rc sdk.RunbookContext, _ inventory.InventoryItem, _ map[string]any) (collection.Result, error) {
		seen = rc.InjectSecrets()
		return collection.Result{}, nil
	}
	if err := collection.Register(collection.Descriptor{
		Name: "nativeexternal.seed.read",
		Manifest: collection.Manifest{Status: collection.StatusImplemented,
			Reversibility: collection.Reversibility{ReadOnly: true, Notes: "reads"},
			Doc:           collection.Doc{Params: []collection.Param{{Name: "device", Type: "string"}}}},
		Invoke:   method,
		Provider: &collection.Provider{Program: "/opt/collections/seed", Digest: "sha256:00"},
	}); err != nil {
		t.Fatal(err)
	}
	runbooks := writeRunbook(t, "seed", "id: seed\ntasks:\n  - name: read\n    nativeexternal.seed.read: {}\n")
	adapter, err := NewAdapter(&mockBus{}, runbooks, nil)
	if err != nil {
		t.Fatal(err)
	}
	secrets := map[string]string{wire.SecretPassword: "the-login"}
	for _, key := range seedKeys {
		if !strings.HasPrefix(key, seedSecretPrefix) {
			t.Errorf("the engine writes the seed key %q, which does not carry %q, so it would reach a method", key, seedSecretPrefix)
		}
		secrets[key] = "delivered-" + key
	}
	if _, err := adapter.Execute(context.Background(), wire.DispatchPayload{
		JobID: "job-1", RunbookID: "seed", DeviceName: "router1", DeviceHost: "10.0.0.1", Secrets: secrets,
	}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if seen == nil || seen[wire.SecretPassword] != "the-login" {
		t.Fatalf("the method saw %v, want the payload's password", seen)
	}
	for _, key := range seedKeys {
		if _, ok := seen[key]; ok {
			t.Errorf("the method received %s", key)
		}
	}
	if len(secrets) != len(seedKeys)+1 {
		t.Errorf("the caller's secrets were changed: %d keys left", len(secrets))
	}
}

// TestSeedPrefixNamesOnlySeeds: no key a device's own credential flattens to
// (credential.Flatten, the machine credential's writer) carries the prefix
// the Runner drops, so dropping it takes nothing else.
func TestSeedPrefixNamesOnlySeeds(t *testing.T) {
	cred, err := credential.Generate("admin", "seed-test")
	if err != nil {
		t.Fatal(err)
	}
	cred.Passphrase, cred.CertificatePEM, cred.PFXBase64, cred.Token, cred.EnablePassword = "p", []byte("c"), "x", "t", "e"
	flat := credential.Flatten(cred)
	if len(flat) < 8 {
		t.Fatalf("Flatten wrote only %v, so a field was not reached", slices.Sorted(maps.Keys(flat)))
	}
	for key := range flat {
		if strings.HasPrefix(key, seedSecretPrefix) {
			t.Errorf("%q is not a seed and carries %q", key, seedSecretPrefix)
		}
	}
	payload := wire.DispatchPayload{Secrets: flat}
	if got := withoutDeliveredSeeds(nil, payload); len(got.Secrets) != len(flat) {
		t.Errorf("a payload with no seed lost keys: %v", slices.Sorted(maps.Keys(got.Secrets)))
	}
}

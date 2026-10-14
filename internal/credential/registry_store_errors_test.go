// The registry store's refusals and failures: each is reported, never a
// silent empty account.
package credential

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSaveRegistryCredential_Refusals: no host, a key that is not a key,
// and a credential file that does not parse are each refused, and the
// refused save writes nothing.
func TestSaveRegistryCredential_Refusals(t *testing.T) {
	key := bytes.Repeat([]byte{7}, 32)
	dir := t.TempDir()
	if err := SaveRegistryCredential(dir, key, "  ", "u", "p"); err == nil || !strings.Contains(err.Error(), "host") {
		t.Errorf("no host: %v", err)
	}
	if err := SaveRegistryCredential(dir, []byte("short"), "ghcr.io", "u", "p"); err == nil {
		t.Error("a key that is not a key was used")
	}
	if _, err := os.Stat(credentialsPath(dir)); !os.IsNotExist(err) {
		t.Errorf("a refused save wrote the credential file: %v", err)
	}

	corrupt := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(credentialsPath(corrupt)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(credentialsPath(corrupt), []byte("registries: [not, a, map"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SaveRegistryCredential(corrupt, key, "ghcr.io", "u", "p"); err == nil {
		t.Error("a save over a credential file that does not parse succeeded")
	}

	// A directory that cannot be made: its parent is a file.
	blocked := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocked, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SaveFileStore(blocked, key, "d", Credential{Password: "p"}); err == nil {
		t.Error("a save under a file succeeded")
	}
}

// TestLookupRegistry_Failures: a credential file that does not parse, a
// password that does not decrypt, and a master key that cannot be
// resolved are errors, not an absent account.
func TestLookupRegistry_Failures(t *testing.T) {
	key := bytes.Repeat([]byte{7}, 32)
	dir := t.TempDir()
	if err := SaveRegistryCredential(dir, key, "ghcr.io", "deployer", "registry-secret"); err != nil {
		t.Fatal(err)
	}

	other, err := NewFileStore(dir, bytes.Repeat([]byte{9}, 32))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := other.(RegistryStore).LookupRegistry(context.Background(), "ghcr.io"); err == nil || !strings.Contains(err.Error(), "decrypt") {
		t.Errorf("a password under another key: %v, want a decrypt failure", err)
	}

	if err := os.WriteFile(credentialsPath(dir), []byte("registries: [not, a, map"), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := NewFileStore(dir, key)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, ok, err := store.(RegistryStore).LookupRegistry(context.Background(), "ghcr.io"); err == nil || ok {
		t.Errorf("a credential file that does not parse: ok %v, %v", ok, err)
	}

	t.Setenv(masterKeyEnvVar, "not a base64 key!")
	lazy := NewLazyFileStore(t.TempDir())
	if _, _, ok, err := lazy.(RegistryStore).LookupRegistry(context.Background(), "ghcr.io"); err == nil || ok {
		t.Errorf("an unresolvable master key: ok %v, %v", ok, err)
	}
}

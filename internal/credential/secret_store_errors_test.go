// Tests for named secrets when the credential file or its key is wrong.
package credential_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
)

// TestSecrets_ABrokenStoreIsReported: an unreadable credential file, a key
// that is not a key, and a master key that changed under a stored secret are
// each an error, never a missing secret.
func TestSecrets_ABrokenStoreIsReported(t *testing.T) {
	// The developer's own PLEIADES_MASTER_KEY, if set, would give both
	// projects one key: the test starts with none, restored when it ends.
	t.Setenv("PLEIADES_MASTER_KEY", "")
	if err := os.Unsetenv("PLEIADES_MASTER_KEY"); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	key, err := credential.ResolveMasterKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := credential.SaveSecretField(dir, []byte("short"), "dmvpn", "psk", "Long-Enough-1"); err == nil {
		t.Error("a key that is not 32 bytes was taken")
	}
	if err := credential.SaveSecretField(dir, key, "dmvpn", "psk", "Long-Enough-1"); err != nil {
		t.Fatal(err)
	}

	// Another master key: the stored ciphertext no longer opens.
	other := t.TempDir()
	if err := os.MkdirAll(filepath.Join(other, ".pleiades"), 0o700); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, ".pleiades", "credentials.yaml")) // #nosec G304 -- this test's own directory
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(other, ".pleiades", "credentials.yaml"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := credential.NewLazyFileStore(other).(credential.SecretStore).LookupSecret(context.Background(), "dmvpn", "psk"); err == nil {
		t.Error("a secret opened under another master key")
	}
	if _, err := credential.SecretValues(other); err == nil {
		t.Error("SecretValues opened a secret under another master key")
	}

	// A master key that cannot be read.
	t.Setenv("PLEIADES_MASTER_KEY", "not a key")
	if _, _, err := credential.NewLazyFileStore(dir).(credential.SecretStore).LookupSecret(context.Background(), "dmvpn", "psk"); err == nil {
		t.Error("a lookup with an unreadable master key did not fail")
	}
	if _, err := credential.SecretValues(dir); err == nil {
		t.Error("SecretValues with an unreadable master key did not fail")
	}
	// t.Setenv restores the variable when the test ends; it must be absent,
	// not empty, for the file's own key to be read again.
	if err := os.Unsetenv("PLEIADES_MASTER_KEY"); err != nil {
		t.Fatal(err)
	}

	// A credential file that is not one.
	broken := t.TempDir()
	if err := os.MkdirAll(filepath.Join(broken, ".pleiades"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(broken, ".pleiades", "credentials.yaml"), []byte("secrets: [not, a, map]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := credential.ListSecrets(broken); err == nil {
		t.Error("ListSecrets read a broken file")
	}
	if _, err := credential.SecretValues(broken); err == nil {
		t.Error("SecretValues read a broken file")
	}
	brokenKey, err := credential.ResolveMasterKey(broken)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := credential.NewLazyFileStore(broken).(credential.SecretStore).LookupSecret(context.Background(), "dmvpn", "psk"); err == nil {
		t.Error("a lookup read a broken file")
	}
	_ = brokenKey
}

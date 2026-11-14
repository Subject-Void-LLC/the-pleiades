// Tests that a credential file or seal key another account could read or
// change is refused before any secret in it is read.
package credential_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/crypto"
)

// TestCredentialFile_ALooseFileIsRefused: credentials.yaml and seal.key at
// 0644 are each refused by every reader naming chmod 600, and read again
// once put back to 0600.
func TestCredentialFile_ALooseFileIsRefused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX modes do not apply on Windows, where the check checks nothing by design")
	}
	dir := t.TempDir()
	key, err := credential.ResolveMasterKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := credential.SaveFileStore(dir, key, "web01", credential.Credential{Username: "ops", Password: "p"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := credential.SealKey(dir, key); err != nil {
		t.Fatal(err)
	}
	creds := filepath.Join(dir, ".pleiades", "credentials.yaml")
	seal := filepath.Join(dir, ".pleiades", "seal.key")
	for _, path := range []string{creds, seal} {
		if err := os.Chmod(path, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	refused := func(what string, err error, path string) {
		t.Helper()
		if !errors.Is(err, crypto.ErrLooseSecretFile) || !strings.Contains(err.Error(), "chmod 600 "+path) {
			t.Errorf("%s = %v, want a refusal naming chmod 600 %s", what, err, path)
		}
	}
	_, err = credential.NewLazyFileStore(dir).Lookup(context.Background(), "web01")
	refused("Lookup", err, creds)
	_, err = credential.ListCredentials(dir)
	refused("ListCredentials", err, creds)
	refused("SaveFileStore", credential.SaveFileStore(dir, key, "web02", credential.Credential{Username: "x", Password: "y"}), creds)
	_, err = credential.LoadSealKey(dir, key)
	refused("LoadSealKey", err, seal)

	for _, path := range []string{creds, seal} {
		if err := os.Chmod(path, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if cred, err := credential.NewLazyFileStore(dir).Lookup(context.Background(), "web01"); err != nil || cred.Username != "ops" {
		t.Errorf("after chmod 600: %+v, %v", cred, err)
	}
	if _, err := credential.LoadSealKey(dir, key); err != nil {
		t.Errorf("the seal key after chmod 600: %v", err)
	}
}

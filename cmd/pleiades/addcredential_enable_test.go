// Tests of add-credential's enable secret, through the real binary.
package main_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
)

// writeTestKey writes a new ed25519 private key in OpenSSH PEM form and
// returns its path.
func writeTestKey(t *testing.T) string {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "id_ed25519")
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// storedCredential reads device's credential back from dir's store.
func storedCredential(t *testing.T, dir, device string) credential.Credential {
	t.Helper()
	key, err := credential.ResolveMasterKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	store, err := credential.NewFileStore(dir, key)
	if err != nil {
		t.Fatal(err)
	}
	cred, err := store.Lookup(context.Background(), device)
	if err != nil {
		t.Fatal(err)
	}
	return cred
}

// TestCLI_AddCredentialStoresAnEnableSecret: with --password-stdin the
// enable secret is the second line, alone it is the only one, and it never
// goes with a token or a generated credential or is read two ways.
func TestCLI_AddCredentialStoresAnEnableSecret(t *testing.T) {
	dir := t.TempDir()
	if out, err := runPleiades(t, dir, "init"); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	if out, err := runPleiadesWithStdin(t, dir, "cisco\nen4ble\n", "add-credential", "r1", "--username", "cisco", "--password-stdin", "--enable-password-stdin"); err != nil {
		t.Fatalf("add-credential: %v\n%s", err, out)
	}
	if c := storedCredential(t, dir, "r1"); c.Password != "cisco" || c.EnablePassword != "en4ble" {
		t.Errorf("stored password set %v, enable %q; want both lines kept apart", c.Password == "cisco", c.EnablePassword)
	}

	keyPath := writeTestKey(t)
	if out, err := runPleiadesWithStdin(t, dir, "en4ble\n", "add-credential", "r2", "--username", "cisco", "--key", keyPath, "--enable-password-stdin"); err != nil {
		t.Fatalf("add-credential with a key: %v\n%s", err, out)
	}
	if c := storedCredential(t, dir, "r2"); len(c.PrivateKeyPEM) == 0 || c.EnablePassword != "en4ble" {
		t.Errorf("a key and an enable secret: key %d bytes, enable %q", len(c.PrivateKeyPEM), c.EnablePassword)
	}

	for name, args := range map[string][]string{
		"with a token":    {"add-credential", "r3", "--token-stdin", "--enable-password-stdin"},
		"with --generate": {"add-credential", "r3", "--username", "u", "--generate", "--enable-password-stdin"},
		"read two ways":   {"add-credential", "r3", "--username", "u", "--password-stdin", "--enable-password", "--enable-password-stdin"},
	} {
		if out, err := runPleiadesWithStdin(t, dir, "x\ny\n", args...); err == nil || !strings.Contains(out, "enable") {
			t.Errorf("%s: %v\n%s; want refused naming the enable secret", name, err, out)
		}
	}
}

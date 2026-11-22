// Tests of add-credential --generate --json through the real binary: one
// document on standard output that fits its published schema, describes the
// key the vault holds, names the key a --replace discarded, and holds no
// secret.
package main_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/Subject-Void-LLC/the-pleiades/internal/clireport"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
)

// generatedJSON runs add-credential --generate --json with args and decodes
// its standard output, which must be exactly one JSON document fitting the
// published schema.
func generatedJSON(t *testing.T, dir string, args ...string) (clireport.GeneratedCredential, string) {
	t.Helper()
	out := stdoutOf(t, dir, append([]string{"add-credential"}, args...)...)
	fitsSchema(t, clireport.GeneratedCredentialSchema, "add-credential --generate --json", out)
	dec := json.NewDecoder(bytes.NewReader(out))
	var doc clireport.GeneratedCredential
	if err := dec.Decode(&doc); err != nil {
		t.Fatalf("standard output is not JSON: %v\n%s", err, out)
	}
	if err := dec.Decode(new(any)); !errors.Is(err, io.EOF) {
		t.Fatalf("standard output holds more than one document:\n%s", out)
	}
	return doc, string(out)
}

// TestCLI_AddCredentialGenerateJSON: the document names the device, the
// account and the key the vault now holds (its type, size and fingerprint,
// the same fingerprint list-credentials shows, and a public key line of that
// key), says whether a password was generated, names the key --replace
// discarded, and never holds the private key or the password.
func TestCLI_AddCredentialGenerateJSON(t *testing.T) {
	dir := credentialProject(t)
	first, out := generatedJSON(t, dir, "web01", "--username", "root", "--generate", "--key-type", "ecdsa-p256", "--json")
	stored := storedCredential(t, dir, "web01")
	if first.SchemaVersion != clireport.GeneratedCredentialVersion || first.Device != "web01" || first.Username != "root" ||
		first.KeyType != "ecdsa-p256" || first.KeyBits != 256 || !first.PasswordGenerated || first.Unbound || first.ReplacedKeyFingerprint != "" {
		t.Errorf("the document is %+v", first)
	}
	info, err := credential.DescribeKey(stored)
	if err != nil || first.KeyFingerprint != info.Fingerprint {
		t.Errorf("the document's fingerprint %q is not the stored key's %+v: %v", first.KeyFingerprint, info, err)
	}
	if listed := listedCredentials(t, dir)["web01"]; listed.KeyFingerprint != first.KeyFingerprint {
		t.Errorf("list-credentials shows %q, the document %q", listed.KeyFingerprint, first.KeyFingerprint)
	}
	pub, comment, _, _, err := ssh.ParseAuthorizedKey([]byte(first.PublicKey))
	if err != nil || comment != "root@web01" || ssh.FingerprintSHA256(pub) != first.KeyFingerprint {
		t.Errorf("the public key line %q is not the key's: %v", first.PublicKey, err)
	}
	if strings.Contains(out, stored.Password) || strings.Contains(out, "PRIVATE KEY") {
		t.Errorf("the document holds a secret:\n%s", out)
	}

	second, out := generatedJSON(t, dir, "web01", "--username", "root", "--generate", "--replace", "--no-password", "--json")
	if second.ReplacedKeyFingerprint != first.KeyFingerprint || second.PasswordGenerated || second.KeyType != "ed25519" || second.KeyFingerprint == first.KeyFingerprint {
		t.Errorf("after --replace --no-password the document is %+v", second)
	}
	if replaced := storedCredential(t, dir, "web01"); replaced.Password != "" || strings.Contains(out, "PRIVATE KEY") {
		t.Errorf("--no-password stored a password, or the document holds the key:\n%s", out)
	}
}

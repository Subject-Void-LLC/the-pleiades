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

// secretsIn names each secret value of cred that text holds: the password,
// the enable secret, the passphrase, and the private key's PEM body whole.
// Lines of that body are not searched one by one, since a short last line,
// or one holding only the public half the file carries, matches public text.
func secretsIn(text string, cred credential.Credential) []string {
	var found []string
	body := ""
	for _, line := range strings.Split(string(cred.PrivateKeyPEM), "\n") {
		if !strings.HasPrefix(line, "-----") {
			body += line
		}
	}
	for what, value := range map[string]string{"password": cred.Password, "enable secret": cred.EnablePassword, "passphrase": cred.Passphrase, "private key": body} {
		if value != "" && strings.Contains(strings.ReplaceAll(text, "\n", ""), value) {
			found = append(found, what)
		}
	}
	return found
}

// generatedJSON runs add-credential --generate --json with args and decodes
// its standard output, which must be exactly one JSON document fitting the
// published schema and holding no field the model does not declare.
func generatedJSON(t *testing.T, dir string, args ...string) (clireport.GeneratedCredential, string) {
	t.Helper()
	out := stdoutOf(t, dir, append([]string{"add-credential"}, args...)...)
	fitsSchema(t, clireport.GeneratedCredentialSchema, "add-credential --generate --json", out)
	// Strict: a field the model does not declare, a secret's among them,
	// fails the decode.
	dec := json.NewDecoder(bytes.NewReader(out))
	dec.DisallowUnknownFields()
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
	// Every string the strict document holds is a public value checked
	// exactly, so none of them can carry a secret.
	if line, err := credential.PublicKey(stored, "root@web01"); err != nil || first.PublicKey != line {
		t.Errorf("public_key is not exactly the stored key's line: %v", err)
	}
	if found := secretsIn(out, stored); len(found) > 0 {
		t.Errorf("the document holds the %s:\n%s", strings.Join(found, " and "), out)
	}
	// The control: the search finds the password and the key where they are.
	if found := secretsIn(stored.Password+"\n"+string(stored.PrivateKeyPEM), stored); len(found) != 2 {
		t.Errorf("the search found %v in text holding the password and the key", found)
	}

	second, out := generatedJSON(t, dir, "web01", "--username", "root", "--generate", "--replace", "--no-password", "--json")
	if second.ReplacedKeyFingerprint != first.KeyFingerprint || second.PasswordGenerated || second.KeyType != "ed25519" || second.KeyFingerprint == first.KeyFingerprint {
		t.Errorf("after --replace --no-password the document is %+v", second)
	}
	replaced := storedCredential(t, dir, "web01")
	if replaced.Password != "" {
		t.Error("--no-password stored a password")
	}
	if found := secretsIn(out, replaced); len(found) > 0 {
		t.Errorf("the document holds the %s:\n%s", strings.Join(found, " and "), out)
	}
}

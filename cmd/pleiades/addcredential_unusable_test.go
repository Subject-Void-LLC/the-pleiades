// Tests of add-credential --generate over a stored credential the read
// refuses, through the real binary: --replace replaces it, as the refusal
// says to, and a fault in the family the save keeps is named instead.
package main_test

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/crypto"
)

// editStoredEntry rewrites device's entry in dir's credential file through
// change, as a hand edit or an older build's save would leave it.
func editStoredEntry(t *testing.T, dir, device string, change func(entry map[string]any)) {
	t.Helper()
	path := filepath.Join(dir, ".pleiades", "credentials.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	devices, _ := doc["devices"].(map[string]any)
	entry, _ := devices[device].(map[string]any)
	if entry == nil {
		t.Fatalf("no entry for %s in %s", device, path)
	}
	change(entry)
	out, err := yaml.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, out, 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestCLI_AddCredential_GenerateReplacesACredentialTheReadRefuses: a stored
// login whose certificate holds a key, which the read refuses and tells the
// operator to replace, is replaced by --generate --replace, and refused
// without --replace; a fault in the SNMP fields is named rather than left
// behind a new login, and --replace with SNMP flags replaces it too. Before,
// every one stopped with "failed to read the credential store".
func TestCLI_AddCredential_GenerateReplacesACredentialTheReadRefuses(t *testing.T) {
	dir := credentialProject(t)
	if out, err := runPleiadesWithStdin(t, dir, "login-pass-1\nauth-pass-phrase\n", "add-credential", "web01", "--username", "admin", "--password-stdin",
		"--snmp-user", "monitor", "--snmp-auth", "sha", "--snmp-passphrase-stdin"); err != nil {
		t.Fatalf("add-credential: %v\n%s", err, out)
	}
	key, err := credential.ResolveMasterKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	svc, err := crypto.NewAESService(key)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := svc.Encrypt([]byte("-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----\n-----BEGIN EC PRIVATE KEY-----\nMHcC\n-----END EC PRIVATE KEY-----\n"))
	if err != nil {
		t.Fatal(err)
	}
	editStoredEntry(t, dir, "web01", func(e map[string]any) { e["certificate_encrypted"] = base64.StdEncoding.EncodeToString(sealed) })

	out, err := runPleiades(t, dir, "add-credential", "web01", "--generate", "--username", "admin")
	if err == nil || !strings.Contains(out, "cannot be used") || !strings.Contains(out, "--replace discards it") || strings.Contains(out, "failed to read") {
		t.Fatalf("--generate without --replace over a refused login: %v\n%s", err, out)
	}
	if out, err := runPleiades(t, dir, "add-credential", "web01", "--generate", "--replace", "--username", "admin"); err != nil || !strings.Contains(out, "public key: ssh-ed25519 ") {
		t.Fatalf("--generate --replace over a refused login: %v\n%s", err, out)
	}
	c := storedCredential(t, dir, "web01")
	if c.Password == "login-pass-1" || len(c.CertificatePEM) != 0 || len(c.PrivateKeyPEM) == 0 || c.SNMPUser != "monitor" {
		t.Errorf("after --generate --replace the credential is %v; want a new login and the SNMP user kept", c)
	}

	// The fault now in the SNMP fields, which a new login alone keeps.
	editStoredEntry(t, dir, "web01", func(e map[string]any) { e["snmp_auth_protocol"] = "rot13" })
	out, err = runPleiades(t, dir, "add-credential", "web01", "--generate", "--replace", "--username", "admin")
	if err == nil || !strings.Contains(out, "replaces only its login fields, so store its SNMP fields again first") {
		t.Fatalf("--generate --replace over refused SNMP fields: %v\n%s", err, out)
	}
	if out, err := runPleiadesWithStdin(t, dir, "c0mmunity-s3cret\n", "add-credential", "web01", "--generate", "--replace", "--username", "admin", "--snmp-community-stdin"); err != nil {
		t.Fatalf("--generate --replace with SNMP flags over refused SNMP fields: %v\n%s", err, out)
	}
	if c := storedCredential(t, dir, "web01"); c.SNMPCommunity != "c0mmunity-s3cret" || c.SNMPUser != "" || len(c.PrivateKeyPEM) == 0 {
		t.Errorf("after replacing both families the credential is %v", c)
	}
}

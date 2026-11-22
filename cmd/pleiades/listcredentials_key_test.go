// Tests of a stored key's type and fingerprint through the real binary:
// add-credential records them, list-credentials shows them with the master
// key gone, an older entry reads "not recorded", and a run refuses a key its
// record does not describe without writing either fingerprint anywhere.
package main_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
	"golang.org/x/crypto/ssh"
)

// writeKeyFile writes a new ed25519 private key in OpenSSH's form to a file
// of its own and returns the path and the key's SHA256 fingerprint.
func writeKeyFile(t *testing.T) (string, string) {
	t.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKey(key, "")
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "id_ed25519")
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}
	return path, ssh.FingerprintSHA256(signer.PublicKey())
}

// printedFingerprint is the fingerprint of the public key line
// add-credential --generate printed last.
func printedFingerprint(t *testing.T, out string) string {
	t.Helper()
	i := strings.LastIndex(out, "public key: ")
	if i < 0 {
		t.Fatalf("no public key line:\n%s", out)
	}
	pub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(strings.TrimSpace(out[i+len("public key: "):])))
	if err != nil {
		t.Fatalf("the public key line does not parse: %v\n%s", err, out)
	}
	return ssh.FingerprintSHA256(pub)
}

// editCredentialEntry rewrites device's entry in dir's credential file
// through change, as a person with a text editor would: no key, just YAML.
func editCredentialEntry(t *testing.T, dir, device string, change func(entry map[string]any)) {
	t.Helper()
	path := filepath.Join(dir, ".pleiades", "credentials.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]map[string]map[string]any
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	change(doc["devices"][device])
	out, err := yaml.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, out, 0o600); err != nil {
		t.Fatal(err)
	}
}

// replaceInCredentialFile edits dir's credential file as a person would,
// replacing old with new.
func replaceInCredentialFile(t *testing.T, dir, old, new string) {
	t.Helper()
	path := filepath.Join(dir, ".pleiades", "credentials.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), old) {
		t.Fatalf("the credential file does not hold %q", old)
	}
	if err := os.WriteFile(path, []byte(strings.Replace(string(data), old, new, 1)), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestCLI_ListCredentials_ShowsEachKeysRecordWithNoMasterKey: --key and
// --generate each record their key's type and fingerprint, list-credentials
// prints them in text and --json with the master key moved away, a password
// carries none, and an entry whose record is gone reads "not recorded".
func TestCLI_ListCredentials_ShowsEachKeysRecordWithNoMasterKey(t *testing.T) {
	dir := credentialProject(t)
	keyPath, keyFingerprint := writeKeyFile(t)
	oldPath, _ := writeKeyFile(t)
	for _, args := range [][]string{
		{"add-credential", "web01", "--username", "root", "--key", keyPath},
		{"add-credential", "old1", "--username", "root", "--key", oldPath, "--no-device"},
	} {
		if out, err := runPleiades(t, dir, args...); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	if out, err := runPleiadesWithStdin(t, dir, "not-a-real-pw\n", "add-credential", "sw1", "--username", "admin", "--password-stdin", "--no-device"); err != nil {
		t.Fatalf("add-credential sw1: %v\n%s", err, out)
	}
	generated, err := runPleiades(t, dir, "add-credential", "db01", "--username", "root", "--generate", "--no-device")
	if err != nil {
		t.Fatalf("add-credential --generate: %v\n%s", err, generated)
	}
	generatedFingerprint := printedFingerprint(t, generated)
	// old1 as a file written before records were kept holds it.
	editCredentialEntry(t, dir, "old1", func(e map[string]any) { delete(e, "key_type"); delete(e, "key_fingerprint") })

	// The listing decrypts nothing, so it needs no master key.
	masterKey := filepath.Join(dir, ".pleiades", "master.key")
	if err := os.Rename(masterKey, masterKey+".away"); err != nil {
		t.Fatal(err)
	}
	listed := listedCredentials(t, dir)
	for name, want := range map[string]struct {
		keyType, fingerprint string
		notRecorded          bool
	}{
		"web01": {"ed25519", keyFingerprint, false},
		"sw1":   {"", "", false},
		"old1":  {"", "", true},
	} {
		got := listed[name]
		if string(got.KeyType) != want.keyType || got.KeyFingerprint != want.fingerprint || got.KeyNotRecorded != want.notRecorded {
			t.Errorf("%s is listed with %q %q not recorded %v, want %+v", name, got.KeyType, got.KeyFingerprint, got.KeyNotRecorded, want)
		}
	}
	if got := listed["db01"]; got.KeyFingerprint != generatedFingerprint {
		t.Errorf("the generated key is listed with %q, and its printed line has %s", got.KeyFingerprint, generatedFingerprint)
	}
	text, err := runPleiades(t, dir, "list-credentials")
	if err != nil {
		t.Fatalf("list-credentials: %v\n%s", err, text)
	}
	for _, want := range []string{"web01  root  private_key  key ed25519 " + keyFingerprint, "old1  root  private_key  key fingerprint not recorded"} {
		if !strings.Contains(text, want) {
			t.Errorf("list-credentials does not print %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "sw1  admin  password  key") {
		t.Errorf("a password is listed with a key:\n%s", text)
	}
	if err := os.Rename(masterKey+".away", masterKey); err != nil {
		t.Fatal(err)
	}
}

// TestCLI_ARunRefusesAKeyItsRecordDoesNotDescribe: web01's recorded
// fingerprint is edited to another key's. A run against it fails before any
// connection, naming the mismatch and the command that replaces it, and no
// file the run wrote (its journal, its records) holds either fingerprint.
func TestCLI_ARunRefusesAKeyItsRecordDoesNotDescribe(t *testing.T) {
	dir := credentialProject(t)
	keyPath, keyFingerprint := writeKeyFile(t)
	_, otherFingerprint := writeKeyFile(t)
	if out, err := runPleiades(t, dir, "add-credential", "web01", "--username", "root", "--key", keyPath); err != nil {
		t.Fatalf("add-credential: %v\n%s", err, out)
	}
	replaceInCredentialFile(t, dir, keyFingerprint, otherFingerprint)

	out, err := runPleiades(t, dir, "adhoc", "web01", "exec.command", "cmd=true")
	if err == nil {
		t.Fatalf("a run with a mismatched key record passed:\n%s", out)
	}
	for _, want := range []string{"does not match the key type and fingerprint recorded beside it", "replace it with pleiades add-credential web01"} {
		if !strings.Contains(out, want) {
			t.Errorf("the run does not say %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "dial") {
		t.Errorf("the run dialed the device with a refused credential:\n%s", out)
	}
	// The credential file is the one place a fingerprint is written.
	err = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Base(path) == "credentials.yaml" {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, fingerprint := range []string{keyFingerprint, otherFingerprint} {
			if strings.Contains(string(data), strings.TrimPrefix(fingerprint, "SHA256:")) {
				t.Errorf("%s holds a key fingerprint", path)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, strings.TrimPrefix(otherFingerprint, "SHA256:")) || strings.Contains(out, strings.TrimPrefix(keyFingerprint, "SHA256:")) {
		t.Errorf("the run's output holds a key fingerprint:\n%s", out)
	}
}

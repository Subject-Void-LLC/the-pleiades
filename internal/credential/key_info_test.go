// Tests of a stored key's record: every save of a login writes the key's
// type and fingerprint as plaintext, a listing shows them with no master key,
// and a read refuses a key the record does not describe.
package credential_test

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
	"golang.org/x/crypto/ssh"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
)

// openSSHKey returns key in OpenSSH's private key form, sealed by passphrase
// when it is not empty, and the SHA256 fingerprint ssh-keygen -l would print.
func openSSHKey(t *testing.T, key any, passphrase string) ([]byte, string) {
	t.Helper()
	var block *pem.Block
	var err error
	if passphrase != "" {
		block, err = ssh.MarshalPrivateKeyWithPassphrase(key, "", []byte(passphrase))
	} else {
		block, err = ssh.MarshalPrivateKey(key, "")
	}
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(block), ssh.FingerprintSHA256(signer.PublicKey())
}

// newEd25519 returns a fresh ed25519 private key.
func newEd25519(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

// editEntry rewrites device's entry in dir's credential file through change,
// as a person with a text editor would: no key, no lock, just the YAML.
func editEntry(t *testing.T, dir, device string, change func(entry map[string]any)) {
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

// TestDescribeKey_NamesEachKeyByTypeAndSize: every key type ssh reads is
// named by its algorithm and size and given the fingerprint ssh-keygen
// prints, a sealed key read with its passphrase, and anything else refused.
func TestDescribeKey_NamesEachKeyByTypeAndSize(t *testing.T) {
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	p256, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	p384, _ := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	p521, _ := ecdsa.GenerateKey(elliptic.P521(), rand.Reader)
	for _, tc := range []struct {
		key  any
		want credential.KeyType
		bits int
	}{
		{newEd25519(t), "ed25519", 256},
		{rsaKey, "rsa-2048", 2048},
		{p256, "ecdsa-p256", 256},
		{p384, "ecdsa-p384", 384},
		{p521, "ecdsa-p521", 521},
	} {
		pemBytes, fingerprint := openSSHKey(t, tc.key, "")
		info, err := credential.DescribeKey(credential.Credential{PrivateKeyPEM: pemBytes})
		if err != nil || info.Type != tc.want || info.Bits != tc.bits || info.Fingerprint != fingerprint {
			t.Errorf("%s: described as %+v, %v; want %s, %d bits, %s", tc.want, info, err, tc.want, tc.bits, fingerprint)
		}
	}

	sealed, fingerprint := openSSHKey(t, newEd25519(t), "correct-horse")
	if info, err := credential.DescribeKey(credential.Credential{PrivateKeyPEM: sealed, Passphrase: "correct-horse"}); err != nil || info.Fingerprint != fingerprint {
		t.Errorf("a sealed key with its passphrase: %+v, %v", info, err)
	}
	for name, cred := range map[string]credential.Credential{
		"no key":           {Password: "p"},
		"not a key":        {PrivateKeyPEM: []byte("key-bytes")},
		"wrong passphrase": {PrivateKeyPEM: sealed, Passphrase: "wrong"},
	} {
		if _, err := credential.DescribeKey(cred); err == nil {
			t.Errorf("%s was described", name)
		}
	}
}

// TestSaveFileStore_RecordsTheKeyAListingShows: a saved key's type and
// fingerprint are written as plaintext beside its ciphertext and listed with
// no master key; a login with no key records none, and a key that cannot be
// read is listed as not recorded rather than guessed at.
func TestSaveFileStore_RecordsTheKeyAListingShows(t *testing.T) {
	dir := t.TempDir()
	key := testKey('k')
	pemBytes, fingerprint := openSSHKey(t, newEd25519(t), "")
	sealed, sealedFingerprint := openSSHKey(t, newEd25519(t), "correct-horse")
	for name, cred := range map[string]credential.Credential{
		"keyed":    {Username: "root", PrivateKeyPEM: pemBytes},
		"sealed":   {Username: "root", PrivateKeyPEM: sealed, Passphrase: "correct-horse"},
		"password": {Username: "admin", Password: "s3cret-pw"},
		"unread":   {Username: "root", PrivateKeyPEM: []byte("key-bytes")},
	} {
		if err := credential.SaveFileStore(dir, key, name, cred); err != nil {
			t.Fatal(err)
		}
	}
	if entry := storedEntry(t, dir, "keyed"); entry["key_type"] != "ed25519" || entry["key_fingerprint"] != fingerprint {
		t.Errorf("the file records %v and %v, want ed25519 and %s", entry["key_type"], entry["key_fingerprint"], fingerprint)
	}

	// Listing takes no key: what it shows is the plaintext record.
	listed, err := credential.ListCredentials(dir)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]credential.CredentialInfo{}
	for _, c := range listed {
		byName[c.Name] = c
	}
	for name, want := range map[string]credential.CredentialInfo{
		"keyed":    {KeyType: "ed25519", KeyFingerprint: fingerprint},
		"sealed":   {KeyType: "ed25519", KeyFingerprint: sealedFingerprint},
		"password": {},
		"unread":   {KeyNotRecorded: true},
	} {
		got := byName[name]
		if got.KeyType != want.KeyType || got.KeyFingerprint != want.KeyFingerprint || got.KeyNotRecorded != want.KeyNotRecorded {
			t.Errorf("%s is listed with %q %q not recorded %v, want %+v", name, got.KeyType, got.KeyFingerprint, got.KeyNotRecorded, want)
		}
	}
	for _, name := range []string{"keyed", "sealed", "password", "unread"} {
		if _, err := lookup(t, dir, key, name); err != nil {
			t.Errorf("%s does not read back: %v", name, err)
		}
	}
}

// TestLookup_RefusesAKeyItsRecordDoesNotDescribe: the record is plaintext, so
// a person can edit it. Another key's fingerprint, another type, or a record
// beside no key each fails the credential closed with ErrKeyMismatch, naming
// the family to replace and neither fingerprint; a record removed whole reads
// as an entry stored before records were kept.
func TestLookup_RefusesAKeyItsRecordDoesNotDescribe(t *testing.T) {
	key := testKey('m')
	pemBytes, fingerprint := openSSHKey(t, newEd25519(t), "")
	_, other := openSSHKey(t, newEd25519(t), "")
	for name, tc := range map[string]struct {
		cred   credential.Credential
		change func(map[string]any)
		want   string
	}{
		"another key's fingerprint": {credential.Credential{Username: "root", PrivateKeyPEM: pemBytes},
			func(e map[string]any) { e["key_fingerprint"] = other }, "its fingerprint is not the one recorded"},
		"another type": {credential.Credential{Username: "root", PrivateKeyPEM: pemBytes},
			func(e map[string]any) { e["key_type"] = "rsa-4096" }, "its type is not the one recorded"},
		"the fingerprint alone removed": {credential.Credential{Username: "root", PrivateKeyPEM: pemBytes},
			func(e map[string]any) { delete(e, "key_fingerprint") }, "its fingerprint is not the one recorded"},
		"a record beside no key": {credential.Credential{Username: "admin", Password: "s3cret-pw"},
			func(e map[string]any) { e["key_type"], e["key_fingerprint"] = "ed25519", fingerprint }, "no private key is stored"},
	} {
		dir := t.TempDir()
		if err := credential.SaveFileStore(dir, key, "web01", tc.cred); err != nil {
			t.Fatal(err)
		}
		editEntry(t, dir, "web01", tc.change)
		_, err := lookup(t, dir, key, "web01")
		var unusable *credential.UnusableError
		if !errors.Is(err, credential.ErrKeyMismatch) || !errors.As(err, &unusable) || unusable.Family != credential.LoginFamily {
			t.Errorf("%s: Lookup = %v, want ErrKeyMismatch in the login family", name, err)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "pleiades add-credential web01") {
			t.Errorf("%s: %q does not say %q and how to replace it", name, err, tc.want)
		}
		if strings.Contains(err.Error(), fingerprint) || strings.Contains(err.Error(), other) {
			t.Errorf("%s: the refusal names a fingerprint: %v", name, err)
		}
	}

	// Removing both is what a file from before records were kept looks like:
	// read as it always was, and listed as not recorded.
	dir := t.TempDir()
	if err := credential.SaveFileStore(dir, key, "web01", credential.Credential{Username: "root", PrivateKeyPEM: pemBytes}); err != nil {
		t.Fatal(err)
	}
	editEntry(t, dir, "web01", func(e map[string]any) { delete(e, "key_type"); delete(e, "key_fingerprint") })
	if _, err := lookup(t, dir, key, "web01"); err != nil {
		t.Errorf("an entry with no record: %v", err)
	}
	if listed, _ := credential.ListCredentials(dir); len(listed) != 1 || !listed[0].KeyNotRecorded {
		t.Errorf("an entry with no record is listed as %+v", listed)
	}
}

// TestLookup_KeepsTheFingerprintOffTheCredential: the fingerprint is the
// file's and the listing's alone. The credential a run gets carries no field
// holding it, so neither its printed, JSON or flattened form (what a
// dispatch carries) can put it in a fact, a stat or a journal.
func TestLookup_KeepsTheFingerprintOffTheCredential(t *testing.T) {
	dir := t.TempDir()
	key := testKey('n')
	pemBytes, fingerprint := openSSHKey(t, newEd25519(t), "")
	if err := credential.SaveFileStore(dir, key, "web01", credential.Credential{Username: "root", PrivateKeyPEM: pemBytes}); err != nil {
		t.Fatal(err)
	}
	cred, err := lookup(t, dir, key, "web01")
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(cred)
	flat, _ := json.Marshal(credential.Flatten(cred))
	for form, text := range map[string]string{"printed": fmt.Sprintf("%v %#v", cred, cred), "JSON": string(encoded), "flattened": string(flat)} {
		if strings.Contains(text, fingerprint) || strings.Contains(text, strings.TrimPrefix(fingerprint, "SHA256:")) {
			t.Errorf("the %s credential holds its fingerprint", form)
		}
	}
}

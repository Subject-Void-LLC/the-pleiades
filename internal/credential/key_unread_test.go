// Tests that a recorded key the record check cannot read or name is left to
// the login's own parse: the check refuses only a definite difference.
package credential_test

import (
	// Deprecated, and used for that reason: a DSA key is the one kind ssh
	// reads that the record cannot name.
	"crypto/dsa"
	"crypto/rand"
	"encoding/asn1"
	"encoding/pem"
	"math/big"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
)

// dsaKeyPEM returns a new DSA private key in the PEM form ssh reads.
func dsaKeyPEM(t *testing.T) []byte {
	t.Helper()
	var key dsa.PrivateKey
	if err := dsa.GenerateParameters(&key.Parameters, rand.Reader, dsa.L1024N160); err != nil {
		t.Fatal(err)
	}
	if err := dsa.GenerateKey(&key, rand.Reader); err != nil {
		t.Fatal(err)
	}
	der, err := asn1.Marshal(struct {
		Version            int
		P, Q, G, Pub, Priv *big.Int
	}{0, key.P, key.Q, key.G, key.Y, key.X})
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "DSA PRIVATE KEY", Bytes: der})
}

// TestLookup_LeavesAKeyItCannotReadOrNameToTheLogin: bytes ssh cannot parse,
// and a DSA key it parses but the record cannot name, are stored with no
// record. A record written beside either by hand is not checked against a
// key the check cannot describe, so Lookup returns the credential and the
// login's own parse decides.
func TestLookup_LeavesAKeyItCannotReadOrNameToTheLogin(t *testing.T) {
	dsaPEM := dsaKeyPEM(t)
	if _, err := ssh.ParsePrivateKey(dsaPEM); err != nil {
		t.Fatalf("the control: ssh does not read the DSA key: %v", err)
	}
	_, fingerprint := openSSHKey(t, newEd25519(t), "")
	key := testKey('u')
	for name, pemBytes := range map[string][]byte{"unreadable": []byte("key-bytes"), "DSA": dsaPEM} {
		dir := t.TempDir()
		if err := credential.SaveFileStore(dir, key, "web01", credential.Credential{Username: "root", PrivateKeyPEM: pemBytes}); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if listed, _ := credential.ListCredentials(dir); len(listed) != 1 || !listed[0].KeyNotRecorded {
			t.Errorf("%s: listed as %+v, want not recorded", name, listed)
		}
		editEntry(t, dir, "web01", func(e map[string]any) { e["key_type"], e["key_fingerprint"] = "ed25519", fingerprint })
		if _, err := lookup(t, dir, key, "web01"); err != nil {
			t.Errorf("%s: a key the check cannot describe was refused: %v", name, err)
		}
	}
}

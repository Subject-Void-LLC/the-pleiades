// Tests of a passphrase-sealed OpenSSH key file whose header names another
// public key than the one it seals: x/crypto reads either half without
// comparing them, so the save refuses such a file and a stored one is refused
// when its record is checked.
package credential

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/Subject-Void-LLC/the-pleiades/internal/crypto"
)

// sealedOpenSSHKey is the body of an OpenSSH private key file after its
// magic, as the format lays it out.
type sealedOpenSSHKey struct {
	CipherName   string
	KdfName      string
	KdfOpts      string
	NumKeys      uint32
	PubKey       []byte
	PrivKeyBlock []byte
}

// openSSHMagic opens every OpenSSH private key file's body.
const openSSHMagic = "openssh-key-v1\x00"

// headerLies returns a passphrase-sealed OpenSSH file sealing a new key B
// whose header carries key A's public half instead, and both public keys.
func headerLies(t *testing.T, passphrase string) ([]byte, ssh.PublicKey, ssh.PublicKey) {
	t.Helper()
	keyA, keyB := newKey(t), newKey(t)
	block, err := ssh.MarshalPrivateKeyWithPassphrase(keyB, "", []byte(passphrase))
	if err != nil {
		t.Fatal(err)
	}
	var body sealedOpenSSHKey
	if err := ssh.Unmarshal(block.Bytes[len(openSSHMagic):], &body); err != nil {
		t.Fatal(err)
	}
	pubA, pubB := publicOf(t, keyA), publicOf(t, keyB)
	body.PubKey = pubA.Marshal()
	block.Bytes = append([]byte(openSSHMagic), ssh.Marshal(&body)...)
	return pem.EncodeToMemory(block), pubA, pubB
}

// newKey returns a new ed25519 private key.
func newKey(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

// publicOf returns key's SSH public key.
func publicOf(t *testing.T, key ed25519.PrivateKey) ssh.PublicKey {
	t.Helper()
	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return signer.PublicKey()
}

// TestReadPublicKey_RefusesAHeaderNamingAnotherKey is the control and the
// rule: x/crypto reads key A from the header without the passphrase and key B
// with it, comparing neither, and readPublicKey refuses the file.
func TestReadPublicKey_RefusesAHeaderNamingAnotherKey(t *testing.T) {
	file, pubA, pubB := headerLies(t, "correct-horse")
	_, err := ssh.ParsePrivateKey(file)
	var missing *ssh.PassphraseMissingError
	if !errors.As(err, &missing) || missing.PublicKey == nil || ssh.FingerprintSHA256(missing.PublicKey) != ssh.FingerprintSHA256(pubA) {
		t.Fatalf("the header does not read as key A: %v", err)
	}
	signer, err := ssh.ParsePrivateKeyWithPassphrase(file, []byte("correct-horse"))
	if err != nil || ssh.FingerprintSHA256(signer.PublicKey()) != ssh.FingerprintSHA256(pubB) {
		t.Fatalf("the sealed part does not read as key B: %v", err)
	}
	if _, err := readPublicKey(Credential{PrivateKeyPEM: file, Passphrase: "correct-horse"}); !errors.Is(err, ErrKeyHeaderMismatch) {
		t.Errorf("readPublicKey = %v, want ErrKeyHeaderMismatch", err)
	}
}

// TestSaveAndLookup_RefuseAHeaderNamingAnotherKey: the save refuses the file
// and stores nothing. One already stored, recorded as a reader of the header
// would record it (key A), is refused by Lookup, which reads key B; before
// this check, Lookup compared the header with that record and passed it.
func TestSaveAndLookup_RefuseAHeaderNamingAnotherKey(t *testing.T) {
	dir := t.TempDir()
	key := lockTestKey
	file, pubA, _ := headerLies(t, "correct-horse")
	cred := Credential{Username: "root", PrivateKeyPEM: file, Passphrase: "correct-horse"}
	if err := SaveFileStore(dir, key, "web01", cred); !errors.Is(err, ErrKeyHeaderMismatch) {
		t.Fatalf("SaveFileStore = %v, want ErrKeyHeaderMismatch", err)
	}
	if listed, err := ListCredentials(dir); err != nil || len(listed) != 0 {
		t.Fatalf("a refused save stored %d credentials: %v", len(listed), err)
	}

	// Stored as the earlier code did: the entry written straight, its record
	// read from the header.
	svc, err := crypto.NewAESService(key)
	if err != nil {
		t.Fatal(err)
	}
	err = updateCredentialsDocument(context.Background(), dir, func(doc *credentialsDocument) error {
		login, err := buildLoginEntry(svc, cred)
		if err != nil {
			return err
		}
		info, err := describePublicKey(pubA)
		if err != nil {
			return err
		}
		login.KeyType, login.KeyFingerprint = info.Type, info.Fingerprint
		doc.Devices = map[string]credentialEntry{"web01": {loginEntry: login}}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewFileStore(dir, key)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.Lookup(context.Background(), "web01")
	var unusable *UnusableError
	if !errors.Is(err, ErrKeyHeaderMismatch) || !errors.As(err, &unusable) || unusable.Family != LoginFamily {
		t.Errorf("Lookup = %v, want ErrKeyHeaderMismatch in the login family", err)
	}
}

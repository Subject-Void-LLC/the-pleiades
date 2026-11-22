// Package credential: what a stored private key is (its type and SHA256
// fingerprint), recorded beside it when it is saved so a listing shows it
// with no master key, and held to the key itself each time it is read.
//
// Decided 2026-11-22: the type and fingerprint are readable metadata, never a
// secret, but also never a fact, a stat or a journal entry, since the key
// Pleiades logs in with describes Pleiades, not the device. A file can be
// edited by hand, so Lookup compares what is recorded with the key it
// decrypts and refuses the credential when they differ.
package credential

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"errors"
	"fmt"

	"golang.org/x/crypto/ssh"
)

// KeyType names a private key's algorithm and, where it varies, its size,
// as one token: ed25519, rsa-3072, ecdsa-p256. A stored key of any size is
// named this way (rsa-2048 for one stored with --key); GenerateKeyTypes are
// the ones Pleiades makes.
type KeyType string

// KeyInfo is what a private key is, read from its public half: never a
// secret.
type KeyInfo struct {
	// Type is the key's algorithm and size.
	Type KeyType
	// Bits is the key's size: the modulus for RSA, the curve for ECDSA, and
	// 256 for ed25519.
	Bits int
	// Fingerprint is the SHA256 fingerprint of the public half in the form
	// ssh-keygen -l prints it: SHA256: and unpadded base64.
	Fingerprint string
}

// ErrKeyMismatch is Lookup's refusal of a stored private key that is not the
// key whose type and fingerprint are recorded beside it: the readable record
// was edited, or the key moved under it. It never names either fingerprint,
// since the error may reach a run's output and its journal.
var ErrKeyMismatch = errors.New("its private key does not match the key type and fingerprint recorded beside it")

// DescribeKey returns the type, size and fingerprint of cred's private key,
// reading the key with cred's passphrase when it has one.
func DescribeKey(cred Credential) (KeyInfo, error) {
	signer, err := signerOf(cred)
	if err != nil {
		return KeyInfo{}, err
	}
	return describePublicKey(signer.PublicKey())
}

// describePublicKey names pub's algorithm and size and computes its
// fingerprint. A key of an algorithm it does not know is an error rather
// than a guess.
func describePublicKey(pub ssh.PublicKey) (KeyInfo, error) {
	// Every key type ssh parses from a private key exposes its Go key.
	typed, ok := pub.(ssh.CryptoPublicKey)
	if !ok {
		return KeyInfo{}, fmt.Errorf("credential: a %s key cannot be described", pub.Type())
	}
	info := KeyInfo{Fingerprint: ssh.FingerprintSHA256(pub)}
	switch k := typed.CryptoPublicKey().(type) {
	case ed25519.PublicKey:
		info.Type, info.Bits = "ed25519", 256
	case *rsa.PublicKey:
		info.Bits = k.N.BitLen()
		info.Type = KeyType(fmt.Sprintf("rsa-%d", info.Bits))
	case *ecdsa.PublicKey:
		info.Bits = k.Curve.Params().BitSize
		info.Type = KeyType(fmt.Sprintf("ecdsa-p%d", info.Bits))
	default:
		// DSA among them: OpenSSH 10 dropped it, and a DSA key stored with
		// --key is listed as not recorded rather than named here.
		return KeyInfo{}, fmt.Errorf("credential: a %s key cannot be described", pub.Type())
	}
	return info, nil
}

// signerOf parses cred's private key, with its passphrase when it has one.
// It is the one parse PublicKey and DescribeKey share, so the line a machine
// is given and the fingerprint a listing shows are of the same key.
func signerOf(cred Credential) (ssh.Signer, error) {
	if len(cred.PrivateKeyPEM) == 0 {
		return nil, fmt.Errorf("credential: holds no private key")
	}
	var signer ssh.Signer
	var err error
	if cred.Passphrase != "" {
		signer, err = ssh.ParsePrivateKeyWithPassphrase(cred.PrivateKeyPEM, []byte(cred.Passphrase))
	} else {
		signer, err = ssh.ParsePrivateKey(cred.PrivateKeyPEM)
	}
	if err != nil {
		return nil, fmt.Errorf("credential: reading the private key: %w", err)
	}
	return signer, nil
}

// storedPublicKey returns the public half of the private key cred holds as
// the file stores it, for checking the record. A key sealed by a passphrase
// in OpenSSH's form carries its public half beside the sealed part, and that
// is read without the passphrase or its deliberately slow key derivation, so
// a Lookup does not pay that again before the login pays it once. That
// public half sits inside the same master-key ciphertext as the rest of the
// key, so nobody without the master key can change it. Any other key is
// parsed whole.
func storedPublicKey(cred Credential) (ssh.PublicKey, error) {
	if cred.Passphrase != "" {
		_, err := ssh.ParsePrivateKey(cred.PrivateKeyPEM)
		var missing *ssh.PassphraseMissingError
		if errors.As(err, &missing) && missing.PublicKey != nil {
			return missing.PublicKey, nil
		}
	}
	signer, err := signerOf(cred)
	if err != nil {
		return nil, err
	}
	return signer.PublicKey(), nil
}

// recordKey writes the type and fingerprint of cred's private key onto entry,
// the login family being saved. A key that cannot be read or named records
// nothing, which a listing shows as not recorded: nothing is guessed.
func recordKey(entry *loginEntry, cred Credential) {
	if len(cred.PrivateKeyPEM) == 0 {
		return
	}
	pub, err := storedPublicKey(cred)
	if err != nil {
		return
	}
	info, err := describePublicKey(pub)
	if err != nil {
		return
	}
	entry.KeyType, entry.KeyFingerprint = info.Type, info.Fingerprint
}

// checkKeyRecord holds cred, as Lookup decrypted it, to the type and
// fingerprint entry records. An entry recording neither is one stored before
// they were recorded, or whose key could not be read then, and is not
// checked. Any other difference is ErrKeyMismatch, naming which part
// differs and never a value.
func checkKeyRecord(cred Credential, entry loginEntry) error {
	if entry.KeyType == "" && entry.KeyFingerprint == "" {
		return nil
	}
	if len(cred.PrivateKeyPEM) == 0 {
		return fmt.Errorf("%w: a key is recorded and no private key is stored", ErrKeyMismatch)
	}
	pub, err := storedPublicKey(cred)
	if err != nil {
		return fmt.Errorf("%w: the stored private key cannot be read to compare", ErrKeyMismatch)
	}
	info, err := describePublicKey(pub)
	if err != nil {
		return fmt.Errorf("%w: the stored private key cannot be named to compare", ErrKeyMismatch)
	}
	if info.Fingerprint != entry.KeyFingerprint {
		return fmt.Errorf("%w: its fingerprint is not the one recorded", ErrKeyMismatch)
	}
	if info.Type != entry.KeyType {
		return fmt.Errorf("%w: its type is not the one recorded", ErrKeyMismatch)
	}
	return nil
}

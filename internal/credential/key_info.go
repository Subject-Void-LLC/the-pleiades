// Package credential: what a stored private key is (its type and SHA256
// fingerprint), recorded beside it when it is saved so a listing shows it
// with no master key, and held to the key itself each time it is read.
//
// Decided 2026-11-22: the type and fingerprint are readable metadata, never a
// secret, but also never a fact, a stat or a journal entry, since the key
// Pleiades logs in with describes Pleiades, not the device. A file can be
// edited by hand, so Lookup compares what is recorded with the key it
// decrypts and refuses the credential when they differ. That proves the
// record and the key agree, not that the key is the one first stored under
// the name: the vault seals each field with no associated data
// (internal/crypto's AES-GCM), so a whole sealed key moved from another entry
// with its record passes, and the vault's format is left as it is.
package credential

import (
	"bytes"
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

// ErrKeyHeaderMismatch is the refusal of a private key file in OpenSSH's form,
// sealed by a passphrase, whose header carries another public key than the
// one the passphrase seals. Such a file is never stored, and one already
// stored is refused when its record is checked.
var ErrKeyHeaderMismatch = errors.New("its private key file names one public key in its header and seals another")

// DescribeKey returns the type, size and fingerprint of cred's private key,
// reading the key with cred's passphrase when it has one (readPublicKey).
func DescribeKey(cred Credential) (KeyInfo, error) {
	pub, err := readPublicKey(cred)
	if err != nil {
		return KeyInfo{}, err
	}
	return describePublicKey(pub)
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

// readPublicKey returns the public half of the key cred's private key file
// seals, read with cred's passphrase when it has one, so what is described is
// always the key a login signs with. A file in OpenSSH's form sealed by a
// passphrase also carries a public key in its header, readable without the
// passphrase, which golang.org/x/crypto/ssh never compares with the key it
// seals; when the two differ the file is refused with ErrKeyHeaderMismatch,
// since a tool that trusted the header would name one key while the login
// used another.
func readPublicKey(cred Credential) (ssh.PublicKey, error) {
	signer, err := signerOf(cred)
	if err != nil {
		return nil, err
	}
	sealed := signer.PublicKey()
	if cred.Passphrase == "" {
		return sealed, nil
	}
	// Without the passphrase the parse stops at the header, which costs none
	// of the passphrase's slow key derivation.
	_, err = ssh.ParsePrivateKey(cred.PrivateKeyPEM)
	var missing *ssh.PassphraseMissingError
	if errors.As(err, &missing) && missing.PublicKey != nil && !bytes.Equal(missing.PublicKey.Marshal(), sealed.Marshal()) {
		return nil, ErrKeyHeaderMismatch
	}
	return sealed, nil
}

// recordKey writes the type and fingerprint of cred's private key onto entry,
// the login family being saved. A key that cannot be read or named records
// nothing, which a listing shows as not recorded: nothing is guessed.
func recordKey(entry *loginEntry, cred Credential) {
	if len(cred.PrivateKeyPEM) == 0 {
		return
	}
	info, err := DescribeKey(cred)
	if err != nil {
		return
	}
	entry.KeyType, entry.KeyFingerprint = info.Type, info.Fingerprint
}

// checkKeyRecord holds cred, as Lookup decrypted it, to the type and
// fingerprint entry records, reading the key the passphrase seals, never the
// header beside it. An entry recording neither is one stored before they were
// recorded, or whose key could not be read then, and is not checked. A key
// this cannot read or name is not checked either: the login's own parse of it
// decides, and refusing it here would only put a vaguer error in front of
// that one. What is refused is a definite difference: a record beside no key,
// a key whose fingerprint or type is not the one recorded
// (ErrKeyMismatch), and a file whose header names another key
// (ErrKeyHeaderMismatch), each naming which part differs and never a value.
//
// What this proves is that the record and the key agree, not that either is
// the one first stored there: the vault seals each field with no associated
// data, so a whole sealed key moved from another entry with its record passes.
func checkKeyRecord(cred Credential, entry loginEntry) error {
	if entry.KeyType == "" && entry.KeyFingerprint == "" {
		return nil
	}
	if len(cred.PrivateKeyPEM) == 0 {
		return fmt.Errorf("%w: a key is recorded and no private key is stored", ErrKeyMismatch)
	}
	pub, err := readPublicKey(cred)
	if errors.Is(err, ErrKeyHeaderMismatch) {
		return err
	}
	if err != nil {
		return nil
	}
	info, err := describePublicKey(pub)
	if err != nil {
		return nil
	}
	if info.Fingerprint != entry.KeyFingerprint {
		return fmt.Errorf("%w: its fingerprint is not the one recorded", ErrKeyMismatch)
	}
	if info.Type != entry.KeyType {
		return fmt.Errorf("%w: its type is not the one recorded", ErrKeyMismatch)
	}
	return nil
}

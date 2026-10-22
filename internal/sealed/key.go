// Sealing to a project's public key with HPKE.
package sealed

import (
	"crypto/hpke"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strings"
)

// publicKeyPrefix opens every public key's text form, so a value pasted into
// the wrong place is recognised as a seal key, and one of another format or
// version is refused by name.
const publicKeyPrefix = "pleiades-seal1:"

// kem is the one key encapsulation this version seals with: ML-KEM-768
// combined with X25519, so a file stays closed unless both are broken,
// including by a future quantum computer recording it today.
func kem() hpke.KEM { return hpke.MLKEM768X25519() }

// PrivateKey opens files sealed to its public key.
type PrivateKey struct{ key hpke.PrivateKey }

// PublicKey seals files that only its private key opens. It is not a
// secret.
type PublicKey struct{ key hpke.PublicKey }

// GenerateKey returns a new key pair.
func GenerateKey() (*PrivateKey, error) {
	k, err := kem().GenerateKey()
	if err != nil {
		return nil, fmt.Errorf("failed to generate a seal key: %w", err)
	}
	return &PrivateKey{key: k}, nil
}

// ParsePrivateKey reads a private key Bytes wrote.
func ParsePrivateKey(b []byte) (*PrivateKey, error) {
	k, err := kem().NewPrivateKey(b)
	if err != nil {
		return nil, fmt.Errorf("not a seal private key: %w", err)
	}
	return &PrivateKey{key: k}, nil
}

// Bytes returns the private key's serialized form, to be stored encrypted.
func (k *PrivateKey) Bytes() ([]byte, error) {
	return k.key.Bytes()
}

// Public returns the key's public half.
func (k *PrivateKey) Public() PublicKey {
	return PublicKey{key: k.key.PublicKey()}
}

// ParsePublicKey reads a public key's text form. Whitespace is ignored, so a
// key that wrapped when it was pasted still reads.
func ParsePublicKey(s string) (PublicKey, error) {
	s = strings.Join(strings.Fields(s), "")
	if !strings.HasPrefix(s, publicKeyPrefix) {
		return PublicKey{}, fmt.Errorf("not a pleiades seal key: it should start %q, as `pleiades seal-key` prints it", publicKeyPrefix)
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(s, publicKeyPrefix))
	if err != nil {
		return PublicKey{}, fmt.Errorf("not a pleiades seal key: %w", err)
	}
	k, err := kem().NewPublicKey(raw)
	if err != nil {
		return PublicKey{}, fmt.Errorf("not a pleiades seal key: %w", err)
	}
	return PublicKey{key: k}, nil
}

// String returns the key's text form, which ParsePublicKey reads.
func (p PublicKey) String() string {
	return publicKeyPrefix + base64.RawURLEncoding.EncodeToString(p.key.Bytes())
}

// Fingerprint names the key in a few characters, as SSH names one: a file
// records the fingerprint of the key it was sealed to, so a file brought to
// the wrong project says whose it is.
func (p PublicKey) Fingerprint() string {
	sum := sha256.Sum256(p.key.Bytes())
	return "SHA256:" + base64.RawStdEncoding.EncodeToString(sum[:])
}

// SealToKey seals secret, for purpose, so that only pub's private key opens
// it.
func SealToKey(pub PublicKey, purpose string, secret []byte) ([]byte, error) {
	if err := checkSecret(purpose, secret); err != nil {
		return nil, err
	}
	headers := map[string]string{
		headerVersion:   Version,
		headerMethod:    MethodKey,
		headerPurpose:   purpose,
		headerRecipient: pub.Fingerprint(),
	}
	body, err := hpke.Seal(pub.key, hpke.HKDFSHA256(), hpke.AES256GCM(), binding(headers), secret)
	if err != nil {
		return nil, fmt.Errorf("failed to seal: %w", err)
	}
	return encode(headers, body), nil
}

// OpenWithKey opens a file sealed to priv's public key.
func (f *File) OpenWithKey(priv *PrivateKey) ([]byte, error) {
	if f.Method != MethodKey {
		return nil, fmt.Errorf("this file is sealed with %s, not to a key", f.Method)
	}
	if mine := priv.Public().Fingerprint(); f.Recipient != mine {
		return nil, fmt.Errorf("%w: the file is sealed to %s, and this key is %s", ErrWrongKey, f.Recipient, mine)
	}
	secret, err := hpke.Open(priv.key, hpke.HKDFSHA256(), hpke.AES256GCM(), binding(f.headers), f.body)
	if err != nil {
		return nil, fmt.Errorf("%w: it was changed after it was sealed", ErrCannotOpen)
	}
	return secret, nil
}

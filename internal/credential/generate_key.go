// Package credential: the key types add-credential --generate makes, each
// stored in OpenSSH's own form, and the options a generated login takes.
//
// Decided 2026-11-22: five types, ed25519 the default on every build (a FIPS
// build included, since the build does not say what the target accepts),
// RSA no smaller than 3072 bits, and a password unless the operator says
// otherwise. The type is never stored as a field: it is read from the key.
package credential

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rsa"
	"encoding/pem"
	"fmt"
	"strings"

	"golang.org/x/crypto/ssh"
)

// The key types GenerateKey makes.
const (
	// KeyTypeEd25519 has one signature algorithm, so nothing to downgrade
	// to, and OpenSSH has taken it since 6.5. The default.
	KeyTypeEd25519 KeyType = "ed25519"
	// KeyTypeRSA3072 is for a device that takes RSA user keys only, such as
	// an older Cisco IOS switch.
	KeyTypeRSA3072 KeyType = "rsa-3072"
	// KeyTypeRSA4096 is the same, larger and slower to make.
	KeyTypeRSA4096 KeyType = "rsa-4096"
	// KeyTypeECDSAP256 is for a host in FIPS mode that refuses ed25519.
	KeyTypeECDSAP256 KeyType = "ecdsa-p256"
	// KeyTypeECDSAP384 is the same on the larger curve.
	KeyTypeECDSAP384 KeyType = "ecdsa-p384"
)

// DefaultKeyType is what a generated login holds when no type is asked for.
const DefaultKeyType = KeyTypeEd25519

// GenerateKeyTypes are every type GenerateKey makes, the default first.
var GenerateKeyTypes = []KeyType{KeyTypeEd25519, KeyTypeRSA3072, KeyTypeRSA4096, KeyTypeECDSAP256, KeyTypeECDSAP384}

// IsRSA reports whether t names an RSA key, whose authorized_keys line starts
// with ssh-rsa whatever algorithm it signs with.
func (t KeyType) IsRSA() bool { return strings.HasPrefix(string(t), "rsa-") }

// ParseKeyType returns the generated key type s names. It matches exactly,
// in lower case, and refuses anything else by listing the five names; a
// 2048-bit RSA key is refused with the type to use instead and the way to
// store one made elsewhere.
func ParseKeyType(s string) (KeyType, error) {
	for _, t := range GenerateKeyTypes {
		if s == string(t) {
			return t, nil
		}
	}
	names := make([]string, len(GenerateKeyTypes))
	for i, t := range GenerateKeyTypes {
		names[i] = string(t)
	}
	if strings.EqualFold(s, "rsa-2048") {
		return "", fmt.Errorf("a generated RSA key is at least 3072 bits, so use rsa-3072; a 2048-bit key made elsewhere can still be stored with --key")
	}
	return "", fmt.Errorf("unknown key type %q: the types are %s", s, strings.Join(names, ", "))
}

// GenerateKey returns a new private key of type t in OpenSSH's own PEM
// form, labeled comment, which every type shares so the key is read by one
// parse (ssh.ParsePrivateKey) wherever it is used.
//
// An ed25519 key draws from entropy. RSA and ECDSA keys do not: since Go
// 1.26 their generators ignore the reader they are given and draw from the
// system's secure source (unless GODEBUG=cryptocustomrand=1), so a failing
// entropy source cannot reach them, and nothing here pretends it can.
func GenerateKey(t KeyType, comment string) ([]byte, error) {
	var private crypto.PrivateKey
	var err error
	switch t {
	case KeyTypeEd25519:
		_, private, err = ed25519.GenerateKey(entropy)
	case KeyTypeRSA3072:
		private, err = rsa.GenerateKey(entropy, 3072)
	case KeyTypeRSA4096:
		private, err = rsa.GenerateKey(entropy, 4096)
	case KeyTypeECDSAP256:
		private, err = ecdsa.GenerateKey(elliptic.P256(), entropy)
	case KeyTypeECDSAP384:
		private, err = ecdsa.GenerateKey(elliptic.P384(), entropy)
	default:
		return nil, fmt.Errorf("credential: %q is not a key type this generates", t)
	}
	if err != nil {
		return nil, fmt.Errorf("credential: generating a key: %w", err)
	}
	block, err := ssh.MarshalPrivateKey(private, comment)
	if err != nil {
		return nil, fmt.Errorf("credential: encoding the key: %w", err)
	}
	return pem.EncodeToMemory(block), nil
}

// GenerateOptions are the choices a generated login takes.
type GenerateOptions struct {
	// KeyType is the key to make; empty means DefaultKeyType.
	KeyType KeyType
	// NoPassword stores the key alone. Without a password a device that
	// refuses the key fails the login, rather than the login falling back
	// to a password and hiding the refusal.
	NoPassword bool
}

// GenerateWith returns a credential for username holding a new private key
// of opts' type, labeled comment, and, unless opts says not to, a new
// random password. Neither is ever printed: PublicKey and DescribeKey give
// the parts that may be.
func GenerateWith(username, comment string, opts GenerateOptions) (Credential, error) {
	if username == "" {
		return Credential{}, fmt.Errorf("credential: a generated credential needs a username")
	}
	keyType := opts.KeyType
	if keyType == "" {
		keyType = DefaultKeyType
	}
	key, err := GenerateKey(keyType, comment)
	if err != nil {
		return Credential{}, err
	}
	cred := Credential{Username: username, PrivateKeyPEM: key}
	if !opts.NoPassword {
		if cred.Password, err = randomPassword(GeneratedPasswordLength); err != nil {
			return Credential{}, err
		}
	}
	return cred, nil
}

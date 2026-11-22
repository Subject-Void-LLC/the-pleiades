// Package clireport: the document `pleiades add-credential --generate
// --json` prints.
package clireport

import (
	"reflect"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
)

// GeneratedCredentialVersion is GeneratedCredential's schema_version.
const GeneratedCredentialVersion = 1

// GeneratedCredential is a login add-credential --generate stored: which
// device and account it is for, what key it holds, and the public key line
// to give the machine. It carries no secret: no field holds the private key,
// the password or the enable secret, only whether each was stored.
type GeneratedCredential struct {
	// SchemaVersion is this document's major version.
	SchemaVersion int `json:"schema_version"`
	// Device is the name the credential is stored under.
	Device string `json:"device"`
	// Username is the account it logs in as.
	Username string `json:"username"`
	// KeyType is the key's algorithm and size: ed25519, rsa-3072, rsa-4096,
	// ecdsa-p256 or ecdsa-p384.
	KeyType string `json:"key_type"`
	// KeyBits is the key's size: the modulus for RSA, the curve for ECDSA,
	// and 256 for ed25519.
	KeyBits int `json:"key_bits"`
	// KeyFingerprint is the key's SHA256 fingerprint as ssh-keygen -l prints
	// it, the same value list-credentials shows.
	KeyFingerprint string `json:"key_fingerprint"`
	// PublicKey is the authorized_keys line the machine is given, labeled
	// user@device. An RSA key's line starts with ssh-rsa, which names the
	// key's format, not the algorithm it signs with.
	PublicKey string `json:"public_key"`
	// PasswordGenerated says a random password was stored beside the key;
	// false under --no-password.
	PasswordGenerated bool `json:"password_generated"`
	// EnablePasswordStored says an enable secret given with
	// --enable-password or --enable-password-stdin was stored too.
	EnablePasswordStored bool `json:"enable_password_stored"`
	// Unbound says no inventory device has the name, so the entry is marked
	// unbound.
	Unbound bool `json:"unbound"`
	// ReplacedKeyFingerprint is, with --replace, the fingerprint of the key
	// the new one replaced, so its line can be removed from the machine that
	// held it; absent when there was none or it could not be read.
	ReplacedKeyFingerprint string `json:"replaced_key_fingerprint,omitempty"`
}

// GeneratedCredentialSchema is GeneratedCredential's.
var GeneratedCredentialSchema = Schema{
	File: "credential-generated.json", Command: "pleiades add-credential --generate --json", Title: "Pleiades generated credential",
	Model: reflect.TypeFor[GeneratedCredential](),
	Known: map[string][]string{
		"GeneratedCredential.KeyType": keyTypeNames(),
	},
	ModelFiles: []string{"internal/clireport/credential.go"},
}

// keyTypeNames are the key types add-credential --generate makes, by name.
func keyTypeNames() []string {
	names := make([]string, len(credential.GenerateKeyTypes))
	for i, t := range credential.GenerateKeyTypes {
		names[i] = string(t)
	}
	return names
}

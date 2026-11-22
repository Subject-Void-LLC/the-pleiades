// Tests that the generated credential document fits its published schema.
package clireport

import (
	"slices"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
)

// TestGeneratedCredentialSchema_FitsAFullDocument: a document with every
// field given a value fits the published schema, each property of it filled,
// and its known key types are the ones add-credential makes.
func TestGeneratedCredentialSchema_FitsAFullDocument(t *testing.T) {
	doc := GeneratedCredential{
		SchemaVersion: GeneratedCredentialVersion, Device: "ub1", Username: "root",
		KeyType: "ed25519", KeyBits: 256, KeyFingerprint: "SHA256:Xq3", PublicKey: "ssh-ed25519 AAAA root@ub1",
		PasswordGenerated: true, EnablePasswordStored: true, Unbound: true, ReplacedKeyFingerprint: "SHA256:old",
	}
	fitsStrictly(t, GeneratedCredentialSchema, doc, true)
	known := GeneratedCredentialSchema.Known["GeneratedCredential.KeyType"]
	if len(known) != len(credential.GenerateKeyTypes) || !slices.Contains(known, string(credential.DefaultKeyType)) {
		t.Errorf("the schema knows key types %v", known)
	}
}

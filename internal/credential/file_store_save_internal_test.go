package credential

import (
	"errors"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/crypto"
)

// failingEncryptService is a crypto.Service whose Encrypt always fails.
// crypto.NewAESService's real GCM implementation essentially never fails
// in practice (its only failure mode is crypto/rand exhaustion), so
// buildCredentialEntry's and encryptField's error-wrapping branches
// cannot be reached through the public API with a real key. This fakes
// crypto.Service, the interface those functions actually depend on, per
// AGENTS.md's "mock interfaces, not concrete types," rather than
// contriving a way to break the concrete AES implementation.
//
// This file is a white-box (package credential, not credential_test)
// test file specifically so it can call the unexported
// buildCredentialEntry directly; every other test in this package stays
// in credential_test and exercises the same behavior through the public
// Store/SaveFileStore API.
type failingEncryptService struct{}

// Encrypt always returns an error, simulating an encryption backend
// failure that a real AES-GCM service essentially never produces.
func (failingEncryptService) Encrypt(plaintext []byte) ([]byte, error) {
	return nil, errors.New("simulated encryption failure")
}

// Decrypt is not exercised by these tests but is required to satisfy
// crypto.Service.
func (failingEncryptService) Decrypt(ciphertext []byte) ([]byte, error) {
	return nil, errors.New("simulated decryption failure")
}

// Compile-time assertion that failingEncryptService satisfies
// crypto.Service.
var _ crypto.Service = failingEncryptService{}

// TestBuildCredentialEntry_PasswordEncryptError covers buildCredentialEntry's
// password error-wrapping branch.
func TestBuildCredentialEntry_PasswordEncryptError(t *testing.T) {
	_, err := buildCredentialEntry(failingEncryptService{}, Credential{Password: "x"})
	if err == nil {
		t.Fatal("expected an error when the password fails to encrypt")
	}
}

// TestBuildCredentialEntry_PrivateKeyEncryptError covers
// buildCredentialEntry's private key error-wrapping branch.
func TestBuildCredentialEntry_PrivateKeyEncryptError(t *testing.T) {
	_, err := buildCredentialEntry(failingEncryptService{}, Credential{PrivateKeyPEM: []byte("x")})
	if err == nil {
		t.Fatal("expected an error when the private key fails to encrypt")
	}
}

// TestBuildCredentialEntry_PassphraseEncryptError covers
// buildCredentialEntry's passphrase error-wrapping branch.
func TestBuildCredentialEntry_PassphraseEncryptError(t *testing.T) {
	_, err := buildCredentialEntry(failingEncryptService{}, Credential{Passphrase: "x"})
	if err == nil {
		t.Fatal("expected an error when the passphrase fails to encrypt")
	}
}

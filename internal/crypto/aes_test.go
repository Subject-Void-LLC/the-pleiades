package crypto_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/internal/crypto"
)

func TestAESEncryptionDecryption(t *testing.T) {
	key := []byte(strings.Repeat("a", 32)) // 32-byte key
	svc, err := crypto.NewAESService(key)
	if err != nil {
		t.Fatalf("failed to init service: %v", err)
	}

	plaintext := []byte("secret AAA login token")

	ciphertext, err := svc.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("failed to encrypt: %v", err)
	}

	if bytes.Equal(ciphertext, plaintext) {
		t.Fatalf("ciphertext is equal to plaintext, encryption failed")
	}

	decrypted, err := svc.Decrypt(ciphertext)
	if err != nil {
		t.Fatalf("failed to decrypt: %v", err)
	}

	if !bytes.Equal(decrypted, plaintext) {
		t.Fatalf("decrypted text does not match original: got %q, want %q", decrypted, plaintext)
	}
}

func TestInvalidKeySize(t *testing.T) {
	_, err := crypto.NewAESService([]byte("too-short"))
	if err != crypto.ErrInvalidKeySize {
		t.Fatalf("expected ErrInvalidKeySize, got %v", err)
	}
}

func TestInvalidCiphertext(t *testing.T) {
	key := []byte(strings.Repeat("a", 32))
	svc, _ := crypto.NewAESService(key)

	// Try to decrypt something shorter than the nonce
	_, err := svc.Decrypt([]byte("short"))
	if err != crypto.ErrInvalidCiphertext {
		t.Fatalf("expected ErrInvalidCiphertext, got %v", err)
	}

	// Try to decrypt random bytes that are long enough but not valid GCM format
	_, err = svc.Decrypt([]byte(strings.Repeat("b", 50)))
	if err == nil {
		t.Fatalf("expected error when decrypting garbage ciphertext, got nil")
	}
}

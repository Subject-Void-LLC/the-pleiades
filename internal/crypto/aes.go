package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
)

var (
	ErrInvalidKeySize    = errors.New("master key must be exactly 32 bytes for AES-256")
	ErrInvalidCiphertext = errors.New("ciphertext is too short or malformed")
)

// Service defines the encryption boundary for Envelope Encryption
type Service interface {
	Encrypt(plaintext []byte) ([]byte, error)
	Decrypt(ciphertext []byte) ([]byte, error)
}

type aesService struct {
	aead cipher.AEAD
}

// NewAESService initializes the GCM cipher with a static Master Key.
// The key must be 32 bytes (256 bits).
func NewAESService(masterKey []byte) (Service, error) {
	if len(masterKey) != 32 {
		return nil, ErrInvalidKeySize
	}

	block, err := aes.NewCipher(masterKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create cipher block: %w", err)
	}

	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCM mode: %w", err)
	}

	return &aesService{aead: aead}, nil
}

func (s *aesService) Encrypt(plaintext []byte) ([]byte, error) {
	// Create a random nonce
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("failed to generate nonce: %w", err)
	}

	// Seal appends the ciphertext to the nonce, allowing us to store them together
	ciphertext := s.aead.Seal(nonce, nonce, plaintext, nil)
	return ciphertext, nil
}

func (s *aesService) Decrypt(ciphertext []byte) ([]byte, error) {
	nonceSize := s.aead.NonceSize()
	if len(ciphertext) < nonceSize {
		return nil, ErrInvalidCiphertext
	}

	// Extract the nonce and the actual encrypted payload
	nonce, ciphertextBytes := ciphertext[:nonceSize], ciphertext[nonceSize:]

	// Open decrypts and authenticates the ciphertext
	plaintext, err := s.aead.Open(nil, nonce, ciphertextBytes, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt: %w", err)
	}

	return plaintext, nil
}

package crypto_test

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/crypto"
)

func mustEnvelopeService(t *testing.T, currentKey []byte, currentVersion string, previousKey []byte, previousVersion string) *crypto.EnvelopeService {
	t.Helper()
	svc, err := crypto.NewEnvelopeService(currentKey, currentVersion, previousKey, previousVersion)
	if err != nil {
		t.Fatalf("NewEnvelopeService() error = %v", err)
	}
	return svc
}

func TestEnvelopeService_RoundTrip(t *testing.T) {
	key := []byte(strings.Repeat("k", 32))
	svc := mustEnvelopeService(t, key, "v1", nil, "")

	plaintext := []byte(`{"aaa_token":"Privilege15_Dynamic_Token_12345"}`)
	ciphertext, err := svc.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}
	if !strings.HasPrefix(ciphertext, "v1$AES256GCM$") {
		t.Fatalf("expected ciphertext to start with %q, got %q", "v1$AES256GCM$", ciphertext)
	}
	if strings.Contains(ciphertext, "Privilege15") {
		t.Fatalf("ciphertext leaked plaintext: %s", ciphertext)
	}

	got, err := svc.Decrypt(ciphertext)
	if err != nil {
		t.Fatalf("Decrypt() error = %v", err)
	}
	if string(got) != string(plaintext) {
		t.Fatalf("Decrypt() = %q, want %q", got, plaintext)
	}
}

func TestEnvelopeService_TwoEncryptsOfSamePlaintextDiffer(t *testing.T) {
	// A fresh DEK per call is the entire point of envelope encryption;
	// if two calls ever produced identical ciphertext for identical
	// plaintext, the DEK generation would not be contributing real
	// randomness.
	key := []byte(strings.Repeat("k", 32))
	svc := mustEnvelopeService(t, key, "v1", nil, "")

	plaintext := []byte("same plaintext both times")
	a, err := svc.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}
	b, err := svc.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}
	if a == b {
		t.Fatal("expected two independent encryptions of the same plaintext to differ")
	}
}

func TestEnvelopeService_DecryptViaPreviousSlot(t *testing.T) {
	oldKey := []byte(strings.Repeat("o", 32))
	newKey := []byte(strings.Repeat("n", 32))

	// Data encrypted under the old service (version v1)...
	oldSvc := mustEnvelopeService(t, oldKey, "v1", nil, "")
	plaintext := []byte("pre-rotation secret")
	ciphertext, err := oldSvc.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}

	// ...must still decrypt through a rotated service that knows v1 as
	// its previous slot, without needing a data migration first.
	rotatedSvc := mustEnvelopeService(t, newKey, "v2", oldKey, "v1")
	got, err := rotatedSvc.Decrypt(ciphertext)
	if err != nil {
		t.Fatalf("Decrypt() via previous slot error = %v", err)
	}
	if string(got) != string(plaintext) {
		t.Fatalf("Decrypt() = %q, want %q", got, plaintext)
	}

	// New writes through the rotated service must use the new current
	// version, not the previous one.
	newCiphertext, err := rotatedSvc.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}
	if !strings.HasPrefix(newCiphertext, "v2$") {
		t.Fatalf("expected new ciphertext to be stamped with the current version v2, got %q", newCiphertext)
	}
}

func TestEnvelopeService_UnknownVersionWithNoPreviousSlotConfigured(t *testing.T) {
	key := []byte(strings.Repeat("k", 32))
	svc := mustEnvelopeService(t, key, "v1", nil, "")

	otherKey := []byte(strings.Repeat("x", 32))
	otherSvc := mustEnvelopeService(t, otherKey, "v9", nil, "")
	foreignCiphertext, err := otherSvc.Encrypt([]byte("secret"))
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}

	_, err = svc.Decrypt(foreignCiphertext)
	if !errors.Is(err, crypto.ErrUnknownKeyVersion) {
		t.Fatalf("expected ErrUnknownKeyVersion, got %v", err)
	}
}

func TestEnvelopeService_TamperDetection(t *testing.T) {
	key := []byte(strings.Repeat("k", 32))
	svc := mustEnvelopeService(t, key, "v1", nil, "")

	ciphertext, err := svc.Encrypt([]byte("secret"))
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}
	parts := strings.SplitN(ciphertext, "$", 4)
	if len(parts) != 4 {
		t.Fatalf("expected 4 fields, got %d: %v", len(parts), parts)
	}

	tests := []struct {
		name     string
		tampered string
	}{
		{"flip wrapped DEK segment", strings.Join([]string{parts[0], parts[1], "AAAA" + parts[2][4:], parts[3]}, "$")},
		{"flip sealed data segment", strings.Join([]string{parts[0], parts[1], parts[2], "AAAA" + parts[3][4:]}, "$")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := svc.Decrypt(tt.tampered); err == nil {
				t.Fatal("expected tampered ciphertext to fail decryption, got nil error")
			}
		})
	}
}

func TestEnvelopeService_DecryptMalformedHeaders(t *testing.T) {
	key := []byte(strings.Repeat("k", 32))
	svc := mustEnvelopeService(t, key, "v1", nil, "")

	valid, err := svc.Encrypt([]byte("secret"))
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}
	parts := strings.SplitN(valid, "$", 4)

	tests := []struct {
		name       string
		ciphertext string
		wantErr    error
	}{
		{"too few fields", "v1$AES256GCM$onlyonefield", crypto.ErrMalformedEnvelope},
		{"empty string", "", crypto.ErrMalformedEnvelope},
		{"wrong algorithm tag", strings.Join([]string{parts[0], "AES128CBC", parts[2], parts[3]}, "$"), crypto.ErrUnknownAlgorithm},
		{"lowercase algorithm tag", strings.Join([]string{parts[0], "aes256gcm", parts[2], parts[3]}, "$"), crypto.ErrUnknownAlgorithm},
		{"unknown version", strings.Join([]string{"v99", parts[1], parts[2], parts[3]}, "$"), crypto.ErrUnknownKeyVersion},
		{"non-base64 wrapped key", strings.Join([]string{parts[0], parts[1], "not-valid-base64!!!", parts[3]}, "$"), crypto.ErrMalformedEnvelope},
		{"non-base64 sealed data", strings.Join([]string{parts[0], parts[1], parts[2], "not-valid-base64!!!"}, "$"), crypto.ErrMalformedEnvelope},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := svc.Decrypt(tt.ciphertext)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Decrypt(%q) error = %v, want errors.Is(_, %v)", tt.ciphertext, err, tt.wantErr)
			}
		})
	}
}

func TestNewEnvelopeService_ConstructionErrors(t *testing.T) {
	validKey := []byte(strings.Repeat("k", 32))
	shortKey := []byte("too-short")

	tests := []struct {
		name            string
		currentKey      []byte
		currentVersion  string
		previousKey     []byte
		previousVersion string
	}{
		{"current key wrong size", shortKey, "v1", nil, ""},
		{"current version empty", validKey, "", nil, ""},
		{"current version contains delimiter", validKey, "v$1", nil, ""},
		{"previous key set without previous version", validKey, "v1", validKey, ""},
		{"previous version set without previous key", validKey, "v1", nil, "v0"},
		{"previous key wrong size", validKey, "v1", shortKey, "v0"},
		{"previous version contains delimiter", validKey, "v1", validKey, "v$0"},
		{"previous version equals current version", validKey, "v1", validKey, "v1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := crypto.NewEnvelopeService(tt.currentKey, tt.currentVersion, tt.previousKey, tt.previousVersion)
			if err == nil {
				t.Fatal("expected a construction error, got nil")
			}
		})
	}
}

// TestEnvelopeService_DecryptWrappedKeyWrongLength covers Decrypt's
// defense-in-depth "unwrapped data key is invalid" branch: a wrapped
// segment that genuinely, authentically unwraps (under the correct KEK,
// passing GCM authentication) to something other than exactly a 32-byte
// key. Encrypt itself can never produce this shape (it always wraps a
// freshly generated 32-byte DEK), so this is built directly against the
// public Service returned by NewAESService, the same KEK primitive
// EnvelopeService itself uses internally, to prove the branch is real
// rather than dead code.
func TestEnvelopeService_DecryptWrappedKeyWrongLength(t *testing.T) {
	key := []byte(strings.Repeat("k", 32))
	kekSvc, err := crypto.NewAESService(key)
	if err != nil {
		t.Fatalf("NewAESService() error = %v", err)
	}

	wrongLengthDEK := []byte("too-short-to-be-a-real-dek")
	wrappedWrongLength, err := kekSvc.Encrypt(wrongLengthDEK)
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}

	// Any validly base64-encoded, non-empty "sealed data" segment is fine
	// here: resolveKEK/unwrap must fail before the data segment is ever
	// touched.
	ciphertext := "v1$AES256GCM$" +
		base64.StdEncoding.EncodeToString(wrappedWrongLength) +
		"$" +
		base64.StdEncoding.EncodeToString([]byte("irrelevant"))

	svc := mustEnvelopeService(t, key, "v1", nil, "")
	if _, err := svc.Decrypt(ciphertext); err == nil {
		t.Fatal("expected an error decrypting a wrapped key of the wrong length, got nil")
	}
}

func TestEnvelopeService_CurrentVersion(t *testing.T) {
	key := []byte(strings.Repeat("k", 32))
	svc := mustEnvelopeService(t, key, "v7", nil, "")
	if got := svc.CurrentVersion(); got != "v7" {
		t.Fatalf("CurrentVersion() = %q, want %q", got, "v7")
	}
}

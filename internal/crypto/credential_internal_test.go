package crypto

import (
	"errors"
	"strings"
	"testing"
)

// The unexported halves of the credential encryption path.
//
// These are an internal test package because the functions are unexported
// and because their interesting behavior is not reachable from the ent
// hook: the hook only ever calls them on inputs it just read out of a
// mutation, so the idempotence and already-plaintext branches, which exist
// for real migration and round-trip cases, have no route from outside.

// internalTestService builds a service for these tests.
func internalTestService(t *testing.T) *EnvelopeService {
	t.Helper()

	svc, err := NewEnvelopeService([]byte(strings.Repeat("k", 32)), "v1", nil, "")
	if err != nil {
		t.Fatalf("NewEnvelopeService() error = %v", err)
	}
	return svc
}

// TestEncryptCredentialInputsIsIdempotent covers the branch that makes the
// hook safe to run twice.
//
// It matters because of how a caller updates a credential: read it (the
// interceptor decrypts), change one field, write it back (the hook
// encrypts). If the caller instead writes back a map it never decrypted,
// still carrying the marker, sealing it again would produce a ciphertext
// whose plaintext is itself a ciphertext, and the row would still open,
// once, into garbage.
func TestEncryptCredentialInputsIsIdempotent(t *testing.T) {
	t.Parallel()

	svc := internalTestService(t)
	const binding = "row-uuid-1"

	sealed, err := encryptCredentialInputs(svc, map[string]string{"token": "a-secret-value"}, binding)
	if err != nil {
		t.Fatalf("encryptCredentialInputs() error = %v", err)
	}

	again, err := encryptCredentialInputs(svc, sealed, binding)
	if err != nil {
		t.Fatalf("encryptCredentialInputs() on already-sealed input error = %v", err)
	}
	if again[EncryptedKeyMarker] != sealed[EncryptedKeyMarker] {
		t.Error("sealing an already-sealed map changed it, so the operation is not idempotent")
	}

	opened, err := decryptCredentialInputs(svc, again, binding)
	if err != nil {
		t.Fatalf("decryptCredentialInputs() error = %v", err)
	}
	if opened["token"] != "a-secret-value" {
		t.Errorf("round trip gave %q, want the original value", opened["token"])
	}
}

// TestEncryptCredentialInputsLeavesAnEmptyMapAlone covers the empty case,
// which a credential resolved entirely from an external secret manager has.
// Sealing an empty map would write a ciphertext that decrypts to "{}" and
// make an absent value indistinguishable from a present empty one.
func TestEncryptCredentialInputsLeavesAnEmptyMapAlone(t *testing.T) {
	t.Parallel()

	svc := internalTestService(t)

	for _, empty := range []map[string]string{nil, {}} {
		got, err := encryptCredentialInputs(svc, empty, "row-uuid-1")
		if err != nil {
			t.Fatalf("encryptCredentialInputs() error = %v", err)
		}
		if len(got) != 0 {
			t.Errorf("encryptCredentialInputs() = %v on an empty map, want it untouched", got)
		}
	}
}

// TestDecryptCredentialInputsTolerantOfPlaintext is what lets this be
// turned on against a database that already has rows.
//
// A map with no marker is left exactly as it is, mirroring what
// decryptPropertiesMap already does for Device. Without this, enabling
// encryption would make every pre-existing credential unreadable at once.
func TestDecryptCredentialInputsTolerantOfPlaintext(t *testing.T) {
	t.Parallel()

	svc := internalTestService(t)
	plain := map[string]string{"token": "written-before-encryption-existed"}

	got, err := decryptCredentialInputs(svc, plain, "row-uuid-1")
	if err != nil {
		t.Fatalf("decryptCredentialInputs() on a plaintext map error = %v", err)
	}
	if got["token"] != "written-before-encryption-existed" {
		t.Errorf("decryptCredentialInputs() = %v, want the plaintext untouched", got)
	}
}

// TestDecryptCredentialInputsRefusesASealedMapWithNoBinding covers the
// case where a row carries ciphertext and lost its binding.
//
// Returning the sealed map as though it were plaintext would be the worst
// available outcome: the caller would inject a base64 envelope string as
// though it were a password, and the authentication failure downstream
// would be attributed to the device.
func TestDecryptCredentialInputsRefusesASealedMapWithNoBinding(t *testing.T) {
	t.Parallel()

	svc := internalTestService(t)

	sealed, err := encryptCredentialInputs(svc, map[string]string{"token": "a-secret-value"}, "row-uuid-1")
	if err != nil {
		t.Fatalf("encryptCredentialInputs() error = %v", err)
	}

	got, err := decryptCredentialInputs(svc, sealed, "")
	if err == nil {
		t.Fatalf("decryptCredentialInputs() with no binding succeeded, returning %v", got)
	}
	if got != nil {
		t.Error("decryptCredentialInputs() returned a map alongside an error")
	}
}

// TestDecryptCredentialInputsRefusesAWrongBinding is the unit-level form of
// the relocation attack the ent-level test performs with raw SQL.
func TestDecryptCredentialInputsRefusesAWrongBinding(t *testing.T) {
	t.Parallel()

	svc := internalTestService(t)

	sealed, err := encryptCredentialInputs(svc, map[string]string{"token": "victim-secret-value"}, "victim-row")
	if err != nil {
		t.Fatalf("encryptCredentialInputs() error = %v", err)
	}

	if _, err := decryptCredentialInputs(svc, sealed, "attacker-row"); err == nil {
		t.Fatal("a ciphertext opened under the wrong binding")
	}
}

// TestUnmarshalStringMapRefusesCorruptPlaintext covers the decode failure,
// which is reachable when a ciphertext opens successfully and its contents
// are not what this package wrote (a key reused across two schemas, say).
func TestUnmarshalStringMapRefusesCorruptPlaintext(t *testing.T) {
	t.Parallel()

	if _, err := unmarshalStringMap([]byte("not json at all")); err == nil {
		t.Error("unmarshalStringMap() accepted input that is not JSON")
	}
	// A JSON document of the wrong shape is the more likely real case.
	if _, err := unmarshalStringMap([]byte(`{"token": 12345}`)); err == nil {
		t.Error("unmarshalStringMap() accepted a document whose values are not strings")
	}
}

// TestNewGCMRefusesAKeyOfTheWrongLength covers the guard on the helper
// both bound methods build their cipher through.
func TestNewGCMRefusesAKeyOfTheWrongLength(t *testing.T) {
	t.Parallel()

	if _, err := newGCM([]byte("too short")); err == nil {
		t.Error("newGCM() accepted a key AES cannot use")
	}
	if _, err := newGCM(nil); err == nil {
		t.Error("newGCM() accepted a nil key")
	}
}

// TestEncryptBoundRefusesAServiceWithAnUnusableKEK covers the wrap failure
// inside EncryptBound, reached when the current KEK cannot seal.
func TestEncryptBoundRefusesAServiceWithAnUnusableKEK(t *testing.T) {
	t.Parallel()

	svc := internalTestService(t)
	// Replace the KEK with one that always fails, which is the only way to
	// reach the wrap error without a broken cipher implementation.
	svc.currentKEK = failingService{}

	if _, err := svc.EncryptBound([]byte("x"), []byte("row-1")); err == nil {
		t.Error("EncryptBound() succeeded with a KEK that cannot encrypt")
	}
}

// TestDecryptBoundReportsAnUnwrappableKey covers the matching failure on
// the read side.
func TestDecryptBoundReportsAnUnwrappableKey(t *testing.T) {
	t.Parallel()

	svc := internalTestService(t)
	sealed, err := svc.EncryptBound([]byte("x"), []byte("row-1"))
	if err != nil {
		t.Fatalf("EncryptBound() error = %v", err)
	}

	svc.currentKEK = failingService{}
	if _, err := svc.DecryptBound(sealed, []byte("row-1")); err == nil {
		t.Error("DecryptBound() succeeded with a KEK that cannot decrypt")
	}
}

// errCipherFailed is failingService's error.
var errCipherFailed = errors.New("cipher failed")

// failingService is a Service that always fails, so the wrap and unwrap
// error paths are reachable without breaking real cryptography.
type failingService struct{}

// Encrypt always fails.
func (failingService) Encrypt([]byte) ([]byte, error) { return nil, errCipherFailed }

// Decrypt always fails.
func (failingService) Decrypt([]byte) ([]byte, error) { return nil, errCipherFailed }

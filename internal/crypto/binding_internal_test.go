package crypto

import (
	"errors"
	"strings"
	"testing"
)

// The unexported half of the associated-data binding, tested directly.
//
// These three helpers decide whether a value is written relocatably, and
// two of their branches cannot be reached through a hook: the hooks always
// supply a binding, which is exactly the invariant being protected. Testing
// them from outside would mean testing that the hooks are correct, which is
// a different claim from testing that the helpers refuse the thing the
// hooks are careful not to do.

// TestEncryptPropertiesMapRefusesAnEmptyBinding covers the refusal that
// stops a caller writing a relocatable ciphertext.
//
// It matters more than its size. The tempting implementation is to fall
// back to the unbound envelope when no binding is available, and the
// failure that produces is invisible: the row still decrypts, and every
// write becomes a silent chance to undo a migration already done.
func TestEncryptPropertiesMapRefusesAnEmptyBinding(t *testing.T) {
	t.Parallel()

	svc, err := NewEnvelopeService([]byte(strings.Repeat("k", 32)), "v1", nil, "")
	if err != nil {
		t.Fatalf("NewEnvelopeService() error = %v", err)
	}

	if _, err := encryptPropertiesMap(svc, map[string]interface{}{"password": "x"}, ""); err == nil {
		t.Fatal("encryptPropertiesMap accepted an empty binding, which would store a relocatable ciphertext")
	}

	// The positive control, so the refusal above is not passing because
	// everything fails.
	got, err := encryptPropertiesMap(svc, map[string]interface{}{"password": "x"}, "a-binding")
	if err != nil {
		t.Fatalf("encryptPropertiesMap() with a binding error = %v", err)
	}
	sealed, ok := got[EncryptedKeyMarker].(string)
	if !ok {
		t.Fatalf("encryptPropertiesMap() = %v, want a marker document", got)
	}
	if !IsBoundEnvelope(sealed) {
		t.Errorf("encryptPropertiesMap() produced an unbound envelope: %s", sealed)
	}
}

// TestDecryptEitherReadsBothFormsAndRefusesTheImpossibleOne covers the
// dispatch that makes the migration window readable.
func TestDecryptEitherReadsBothFormsAndRefusesTheImpossibleOne(t *testing.T) {
	t.Parallel()

	svc, err := NewEnvelopeService([]byte(strings.Repeat("k", 32)), "v1", nil, "")
	if err != nil {
		t.Fatalf("NewEnvelopeService() error = %v", err)
	}

	unbound, err := svc.Encrypt([]byte("legacy"))
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}
	bound, err := svc.EncryptBound([]byte("modern"), []byte("a-binding"))
	if err != nil {
		t.Fatalf("EncryptBound() error = %v", err)
	}

	// A pre-migration row opens with no binding at all, which is what lets
	// an upgrade read the rows it inherits.
	got, err := decryptEither(svc, unbound, "")
	if err != nil || string(got) != "legacy" {
		t.Errorf("decryptEither(unbound) = %q, %v, want the legacy value", got, err)
	}

	// A migrated row opens with its binding.
	got, err = decryptEither(svc, bound, "a-binding")
	if err != nil || string(got) != "modern" {
		t.Errorf("decryptEither(bound) = %q, %v, want the modern value", got, err)
	}

	// A bound ciphertext on a row with no binding is its own error rather
	// than a generic decrypt failure, because it means the value and the
	// column that binds it were written apart, which no path in this
	// package can do.
	if _, err := decryptEither(svc, bound, ""); err == nil {
		t.Error("decryptEither opened a bound ciphertext with no binding")
	} else if !strings.Contains(err.Error(), "no secret binding") {
		t.Errorf("decryptEither() error = %v, want it to name the missing binding", err)
	}

	// And a bound ciphertext with the WRONG binding fails, which is the
	// relocation attack reduced to one call.
	if _, err := decryptEither(svc, bound, "somebody-elses-binding"); err == nil {
		t.Error("decryptEither opened a bound ciphertext under the wrong binding")
	}
}

// TestIsUndecryptedInputsIsStrictAboutTheShape covers the check that
// decides whether a rotation pass skips a row.
//
// The strictness is the point, and it is the same argument
// isAlreadyEncryptedShape already makes for the Device side: a laxer test
// that merely looked for the marker's presence would let a credential
// holding a real input named EncryptedKeyMarker, alongside others, be
// mistaken for an unreadable row and silently skipped by every rotation
// forever.
func TestIsUndecryptedInputsIsStrictAboutTheShape(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		inputs map[string]string
		want   bool
	}{
		{name: "the sealed shape", inputs: map[string]string{EncryptedKeyMarker: "v1$x$y$z"}, want: true},
		{name: "ordinary decrypted inputs", inputs: map[string]string{"api_token": "x"}},
		{name: "nothing at all", inputs: map[string]string{}},
		{
			name:   "a colliding key beside real inputs is NOT the sealed shape",
			inputs: map[string]string{EncryptedKeyMarker: "x", "api_token": "y"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := isUndecryptedInputs(tt.inputs); got != tt.want {
				t.Errorf("isUndecryptedInputs(%v) = %v, want %v", tt.inputs, got, tt.want)
			}
		})
	}
}

// TestIsBoundEnvelopeAnswersFalseForAnythingMalformed pins the deliberate
// leniency in the tag reader.
//
// Anything unparseable must reach Decrypt and fail there with the
// malformed-envelope error that names the real problem, rather than fail as
// a binding mismatch and send somebody hunting a relocation attack that did
// not happen.
func TestIsBoundEnvelopeAnswersFalseForAnythingMalformed(t *testing.T) {
	t.Parallel()

	for _, raw := range []string{
		"",
		"not an envelope",
		"v1$AES256GCM$a$b",
		"v1$AES256GCM-AAD",
		"$$$",
	} {
		if IsBoundEnvelope(raw) {
			t.Errorf("IsBoundEnvelope(%q) = true, want false", raw)
		}
	}
	if !IsBoundEnvelope("v1$AES256GCM-AAD$a$b") {
		t.Error("IsBoundEnvelope did not recognise a well formed bound envelope")
	}
}

// TestBulkRefusalsAreDistinctErrors covers the three sentinels callers
// switch on. One shared error would make a handler unable to say which
// entity refused.
func TestBulkRefusalsAreDistinctErrors(t *testing.T) {
	t.Parallel()

	for _, pair := range [][2]error{
		{ErrBulkCredentialInputs, ErrBulkDeviceProperties},
		{ErrBulkDeviceProperties, ErrBulkLaunchConfigAnswers},
		{ErrBulkLaunchConfigAnswers, ErrBulkCredentialInputs},
	} {
		if errors.Is(pair[0], pair[1]) {
			t.Errorf("%v and %v are the same error", pair[0], pair[1])
		}
	}
}

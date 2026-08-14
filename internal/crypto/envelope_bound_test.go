package crypto_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/crypto"
)

// boundTestKey is a valid 32-byte KEK.
var boundTestKey = []byte("0123456789abcdef0123456789abcdef")

// newBoundService builds a service with no rotation in progress.
func newBoundService(t *testing.T) *crypto.EnvelopeService {
	t.Helper()

	svc, err := crypto.NewEnvelopeService(boundTestKey, "v1", nil, "")
	if err != nil {
		t.Fatalf("NewEnvelopeService() failed: %v", err)
	}
	return svc
}

// TestEncryptBoundRoundTrips is the baseline: the binding must not break
// ordinary use.
func TestEncryptBoundRoundTrips(t *testing.T) {
	t.Parallel()

	svc := newBoundService(t)
	const secret = "hunter2-the-real-secret"
	aad := []byte("credential-row-uuid-1")

	sealed, err := svc.EncryptBound([]byte(secret), aad)
	if err != nil {
		t.Fatalf("EncryptBound() failed: %v", err)
	}
	if strings.Contains(sealed, secret) {
		t.Fatal("the sealed string contains the plaintext")
	}

	opened, err := svc.DecryptBound(sealed, aad)
	if err != nil {
		t.Fatalf("DecryptBound() failed: %v", err)
	}
	if string(opened) != secret {
		t.Errorf("DecryptBound() = %q, want %q", opened, secret)
	}
}

// TestRelocatingABoundCiphertextFails is the whole point of this file.
//
// EnvelopeService's own doc comment records the residual it was written
// with: a ciphertext carries nothing tying it to its record, so copying one
// row's envelope onto another and decrypting succeeds. That was verified
// concretely at the time with two Device rows, and accepted, because the
// threat model in scope was a database dump rather than database write
// access.
//
// A credential row makes that unacceptable. Relocating one organization's
// encrypted inputs onto another organization's credential means the second
// organization's jobs get injected with the first organization's secrets,
// and the attacker never had to read anything: one UPDATE on an opaque
// column, and the platform does the exfiltration.
//
// This test performs that exact relocation and requires it to fail.
func TestRelocatingABoundCiphertextFails(t *testing.T) {
	t.Parallel()

	svc := newBoundService(t)

	const victimSecret = "victim-organization-secret"
	victimAAD := []byte("credential-row-uuid-victim")
	attackerAAD := []byte("credential-row-uuid-attacker")

	sealed, err := svc.EncryptBound([]byte(victimSecret), victimAAD)
	if err != nil {
		t.Fatalf("EncryptBound() failed: %v", err)
	}

	// The relocation: the attacker's own row now carries the victim's
	// ciphertext, which is a single UPDATE against a column whose contents
	// mean nothing to them.
	opened, err := svc.DecryptBound(sealed, attackerAAD)
	if err == nil {
		t.Fatalf("a ciphertext relocated to another record decrypted successfully, yielding %q", opened)
	}
	if opened != nil {
		t.Error("DecryptBound() returned plaintext alongside an error")
	}

	// The failure must not explain itself in a way that helps: it names
	// the effect, not the material.
	if strings.Contains(err.Error(), victimSecret) || strings.Contains(err.Error(), string(victimAAD)) {
		t.Errorf("the error quotes the material involved: %v", err)
	}
}

// TestTheUnboundFormStillRelocates is the negative control, and it is why
// the test above proves something.
//
// LESSONS_LEARNED.md #95: prove an assertion can fail before believing it
// passes. If the unbound form ALSO refused a relocation, the binding would
// be doing nothing and the test above would be passing for a reason nobody
// had identified.
//
// This is the documented residual, still present on Device.properties and
// SavedLaunchConfig.answers, asserted rather than described so that the two
// forms' difference is a fact in the test suite instead of a claim in a
// comment.
func TestTheUnboundFormStillRelocates(t *testing.T) {
	t.Parallel()

	svc := newBoundService(t)
	const secret = "unbound-relocatable-secret"

	sealed, err := svc.Encrypt([]byte(secret))
	if err != nil {
		t.Fatalf("Encrypt() failed: %v", err)
	}

	// No record identifier is involved on either side, so there is nothing
	// for the relocation to fail against.
	opened, err := svc.Decrypt(sealed)
	if err != nil {
		t.Fatalf("Decrypt() failed: %v", err)
	}
	if string(opened) != secret {
		t.Fatalf("Decrypt() = %q, want %q", opened, secret)
	}

	t.Log("the unbound form has no record binding, which is the residual EncryptBound exists to close for credentials")
}

// TestTheTwoFormsFailClosedAgainstEachOther covers the property the
// distinct algorithm tag exists for.
//
// The dangerous direction is the first one: if Decrypt accepted a bound
// ciphertext, an attacker could relocate a bound row and then open it
// through the unbound path, and the binding would be decorative.
func TestTheTwoFormsFailClosedAgainstEachOther(t *testing.T) {
	t.Parallel()

	svc := newBoundService(t)
	aad := []byte("record-1")

	bound, err := svc.EncryptBound([]byte("secret-value-here"), aad)
	if err != nil {
		t.Fatalf("EncryptBound() failed: %v", err)
	}
	unbound, err := svc.Encrypt([]byte("secret-value-here"))
	if err != nil {
		t.Fatalf("Encrypt() failed: %v", err)
	}

	if _, err := svc.Decrypt(bound); !errors.Is(err, crypto.ErrUnknownAlgorithm) {
		t.Errorf("Decrypt() on a bound ciphertext gave %v, want ErrUnknownAlgorithm: opening a bound ciphertext without its binding is the relocation attack", err)
	}
	if _, err := svc.DecryptBound(unbound, aad); !errors.Is(err, crypto.ErrUnknownAlgorithm) {
		t.Errorf("DecryptBound() on an unbound ciphertext gave %v, want ErrUnknownAlgorithm", err)
	}
}

// TestBoundEnvelopeRefusesEmptyAssociatedData covers the guard that stops
// a ciphertext carrying the bound tag while binding nothing, which would
// read as protected and not be.
func TestBoundEnvelopeRefusesEmptyAssociatedData(t *testing.T) {
	t.Parallel()

	svc := newBoundService(t)

	if _, err := svc.EncryptBound([]byte("x"), nil); err == nil {
		t.Error("EncryptBound() accepted empty associated data")
	}
	if _, err := svc.EncryptBound([]byte("x"), []byte{}); err == nil {
		t.Error("EncryptBound() accepted zero-length associated data")
	}

	sealed, err := svc.EncryptBound([]byte("x"), []byte("record-1"))
	if err != nil {
		t.Fatalf("EncryptBound() failed: %v", err)
	}
	if _, err := svc.DecryptBound(sealed, nil); err == nil {
		t.Error("DecryptBound() accepted empty associated data")
	}
}

// TestBoundEnvelopeSurvivesKeyRotation proves the binding composes with the
// rotation window, which is the one place two keys are live at once.
func TestBoundEnvelopeSurvivesKeyRotation(t *testing.T) {
	t.Parallel()

	oldKey := []byte("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	newKey := []byte("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	aad := []byte("record-1")
	const secret = "survives-rotation-secret"

	before, err := crypto.NewEnvelopeService(oldKey, "v1", nil, "")
	if err != nil {
		t.Fatalf("NewEnvelopeService() failed: %v", err)
	}
	sealed, err := before.EncryptBound([]byte(secret), aad)
	if err != nil {
		t.Fatalf("EncryptBound() failed: %v", err)
	}

	// Mid-rotation: the new key is current, the old one still accepted.
	during, err := crypto.NewEnvelopeService(newKey, "v2", oldKey, "v1")
	if err != nil {
		t.Fatalf("NewEnvelopeService() failed: %v", err)
	}

	opened, err := during.DecryptBound(sealed, aad)
	if err != nil {
		t.Fatalf("DecryptBound() failed across a rotation: %v", err)
	}
	if string(opened) != secret {
		t.Errorf("DecryptBound() = %q, want %q", opened, secret)
	}

	// And the binding still holds under the new key: rotation must not be
	// a way to launder a relocated ciphertext.
	if _, err := during.DecryptBound(sealed, []byte("another-record")); err == nil {
		t.Error("a relocated ciphertext opened during a key rotation")
	}
}

// TestBoundEnvelopeMalformedInput covers the parse failures, each of which
// must be a returned error rather than a panic or zero-value plaintext.
func TestBoundEnvelopeMalformedInput(t *testing.T) {
	t.Parallel()

	svc := newBoundService(t)
	aad := []byte("record-1")

	tests := []struct {
		name       string
		ciphertext string
	}{
		{name: "empty", ciphertext: ""},
		{name: "too few fields", ciphertext: "v1$AES256GCM-AAD$abc"},
		{name: "unknown version", ciphertext: "v9$AES256GCM-AAD$YWJj$YWJj"},
		{name: "wrapped key is not base64", ciphertext: "v1$AES256GCM-AAD$!!!$YWJj"},
		{name: "sealed data is not base64", ciphertext: "v1$AES256GCM-AAD$YWJj$!!!"},
		{name: "sealed data shorter than a nonce", ciphertext: "v1$AES256GCM-AAD$YWJj$YQ=="},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := svc.DecryptBound(tt.ciphertext, aad)
			if err == nil {
				t.Fatalf("DecryptBound(%q) succeeded, returning %q", tt.ciphertext, got)
			}
			if got != nil {
				t.Error("DecryptBound() returned plaintext alongside an error")
			}
		})
	}
}

// TestIsBound covers the accessor a migration path uses to tell the two
// forms apart.
func TestIsBound(t *testing.T) {
	t.Parallel()

	svc := newBoundService(t)

	bound, err := svc.EncryptBound([]byte("x"), []byte("record-1"))
	if err != nil {
		t.Fatalf("EncryptBound() failed: %v", err)
	}
	unbound, err := svc.Encrypt([]byte("x"))
	if err != nil {
		t.Fatalf("Encrypt() failed: %v", err)
	}

	if !crypto.IsBound(bound) {
		t.Error("IsBound() said a bound ciphertext was not bound")
	}
	if crypto.IsBound(unbound) {
		t.Error("IsBound() said an unbound ciphertext was bound")
	}
	for _, junk := range []string{"", "not-an-envelope", "a$b$c"} {
		if crypto.IsBound(junk) {
			t.Errorf("IsBound(%q) = true", junk)
		}
	}
}

// TestBoundCiphertextIsUniquePerCall pins that a fresh DEK and nonce are
// generated per seal. Two identical plaintexts under the identical binding
// must not produce identical ciphertext, or a reader learns which
// credentials share a value without decrypting anything.
func TestBoundCiphertextIsUniquePerCall(t *testing.T) {
	t.Parallel()

	svc := newBoundService(t)
	aad := []byte("record-1")

	first, err := svc.EncryptBound([]byte("same-value"), aad)
	if err != nil {
		t.Fatalf("EncryptBound() failed: %v", err)
	}
	second, err := svc.EncryptBound([]byte("same-value"), aad)
	if err != nil {
		t.Fatalf("EncryptBound() failed: %v", err)
	}

	if first == second {
		t.Error("two seals of the same plaintext produced identical ciphertext, so equal values are visible without decrypting")
	}
}
